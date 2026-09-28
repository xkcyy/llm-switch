package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// readSSE 逐事件读取 SSE 流。
func readSSE(r io.Reader, fn func(event string, data []byte) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var event string
	var data []byte
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(string(line), "\r\n")
			switch {
			case trimmed == "":
				if len(data) > 0 {
					if e := fn(event, data); e != nil {
						return e
					}
				}
				event, data = "", nil
			case strings.HasPrefix(trimmed, "event:"):
				event = strings.TrimSpace(trimmed[len("event:"):])
			case strings.HasPrefix(trimmed, "data:"):
				chunk := strings.TrimSpace(trimmed[len("data:"):])
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, chunk...)
			}
		}
		if err != nil {
			// 上游中断时也把已累积的数据交给回调：调用方会忽略无法解析的块，
			// 但完整的最后一块（例如工具调用）不该丢。
			if len(data) > 0 {
				_ = fn(event, data)
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

type sseWriter struct {
	w     io.Writer
	flush func()
}

func (s *sseWriter) event(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	if s.flush != nil {
		s.flush()
	}
	return nil
}

// data 仅输出 data 行（OpenAI Chat 风格）。
func (s *sseWriter) data(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return err
	}
	if s.flush != nil {
		s.flush()
	}
	return nil
}

// ---- Chat 流 → Responses 事件 ----

type chatChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			// Reasoning 是部分网关（OpenRouter 风格）的推理字段别名。
			Reasoning string `json:"reasoning"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type streamItem struct {
	outputIndex int
	id          string
	kind        string // message | function_call | custom_tool_call
	custom      bool   // 自由文本（custom）工具调用
	callID      string
	name        string
	text        strings.Builder
	args        strings.Builder
	started     bool
	closed      bool
}

// ---- Messages 流 → Responses 事件 ----

type messagesStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// StreamMessagesToResponses 把 Anthropic SSE 流转换为 Responses SSE 事件。
func StreamMessagesToResponses(r io.Reader, w io.Writer, flush func()) error {
	sw := &sseWriter{w: w, flush: flush}
	respID := "resp_" + fmt.Sprint(time.Now().UnixNano())
	created := time.Now().Unix()
	model := ""
	started := false
	msg := &streamItem{outputIndex: 0, id: "msg_1", kind: "message"}
	textStarted := false
	blocks := map[int]*streamItem{}
	var order []*streamItem
	stopReason := ""
	inputTokens, outputTokens := 0, 0

	ensureStarted := func() error {
		if started {
			return nil
		}
		started = true
		return sw.event("response.created", map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id": respID, "object": "response", "created_at": created, "status": "in_progress", "model": model, "output": []any{},
			},
		})
	}
	ensureMsg := func() error {
		if msg.started {
			return nil
		}
		msg.started = true
		order = append(order, msg)
		return sw.event("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"id": "msg_1", "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
		})
	}
	ensureTextPart := func() error {
		if textStarted {
			return nil
		}
		textStarted = true
		return sw.event("response.content_part.added", map[string]any{
			"type": "response.content_part.added", "item_id": "msg_1", "output_index": 0, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		})
	}

	err := readSSE(r, func(event string, data []byte) error {
		var ev messagesStreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				if ev.Message.Model != "" {
					model = ev.Message.Model
				}
				if ev.Message.Usage != nil {
					inputTokens = ev.Message.Usage.InputTokens
				}
			}
			return ensureStarted()
		case "content_block_start":
			if ev.ContentBlock == nil {
				return nil
			}
			switch ev.ContentBlock.Type {
			case "text":
				if err := ensureMsg(); err != nil {
					return err
				}
				if ev.ContentBlock.Text != "" {
					if err := ensureTextPart(); err != nil {
						return err
					}
					msg.text.WriteString(ev.ContentBlock.Text)
					if err := sw.event("response.output_text.delta", map[string]any{
						"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": ev.ContentBlock.Text,
					}); err != nil {
						return err
					}
				}
			case "tool_use":
				item := &streamItem{outputIndex: len(order), id: fmt.Sprintf("fc_%d", len(blocks)+1), kind: "function_call", callID: ev.ContentBlock.ID, name: ev.ContentBlock.Name, started: true}
				blocks[ev.Index] = item
				order = append(order, item)
				return sw.event("response.output_item.added", map[string]any{
					"type": "response.output_item.added", "output_index": item.outputIndex,
					"item": map[string]any{
						"id": item.id, "type": "function_call", "status": "in_progress",
						"call_id": item.callID, "name": item.name, "arguments": "",
					},
				})
			}
			return nil
		case "content_block_delta":
			if ev.Delta == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				if err := ensureMsg(); err != nil {
					return err
				}
				if err := ensureTextPart(); err != nil {
					return err
				}
				msg.text.WriteString(ev.Delta.Text)
				return sw.event("response.output_text.delta", map[string]any{
					"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": ev.Delta.Text,
				})
			case "input_json_delta":
				if item, ok := blocks[ev.Index]; ok {
					item.args.WriteString(ev.Delta.PartialJSON)
					return sw.event("response.function_call_arguments.delta", map[string]any{
						"type": "response.function_call_arguments.delta", "item_id": item.id,
						"output_index": item.outputIndex, "delta": ev.Delta.PartialJSON,
					})
				}
			}
			return nil
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				outputTokens = ev.Usage.OutputTokens
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := ensureStarted(); err != nil {
		return err
	}
	// 结束消息
	if msg.started {
		text := msg.text.String()
		if textStarted {
			if err := sw.event("response.output_text.done", map[string]any{
				"type": "response.output_text.done", "item_id": "msg_1", "output_index": 0, "content_index": 0, "text": text,
			}); err != nil {
				return err
			}
			if err := sw.event("response.content_part.done", map[string]any{
				"type": "response.content_part.done", "item_id": "msg_1", "output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
			}); err != nil {
				return err
			}
		}
		if err := sw.event("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": 0,
			"item": map[string]any{
				"id": "msg_1", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
			},
		}); err != nil {
			return err
		}
	}
	output := make([]any, 0, len(order))
	for _, item := range order {
		if item.kind == "message" {
			output = append(output, map[string]any{
				"id": "msg_1", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": item.text.String(), "annotations": []any{}}},
			})
			continue
		}
		args := item.args.String()
		if args == "" {
			args = "{}"
		}
		if err := sw.event("response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "item_id": item.id, "output_index": item.outputIndex, "arguments": args,
		}); err != nil {
			return err
		}
		if err := sw.event("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": item.outputIndex,
			"item": map[string]any{
				"id": item.id, "type": "function_call", "status": "completed",
				"call_id": item.callID, "name": item.name, "arguments": args,
			},
		}); err != nil {
			return err
		}
		output = append(output, map[string]any{
			"id": item.id, "type": "function_call", "status": "completed",
			"call_id": item.callID, "name": item.name, "arguments": args,
		})
	}
	status := "completed"
	if stopReason == "max_tokens" {
		status = "incomplete"
	}
	return sw.event("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": created, "status": status, "model": model,
			"output": output, "usage": usageObject(inputTokens, outputTokens),
		},
	})
}

// ---- Responses 流 → Chat 事件（Chat 入口 + Responses 上游） ----

type responsesStreamEvent struct {
	Type      string `json:"type"`
	Delta     string `json:"delta"`
	ItemID    string `json:"item_id"`
	Arguments string `json:"arguments"`
	Item      *struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	} `json:"item"`
	Response *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"response"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ---- Messages 流 → Chat 事件（Chat 入口 + Messages 上游） ----

// StreamMessagesToChat 把 Anthropic SSE 流转换为 Chat SSE 流。
func StreamMessagesToChat(r io.Reader, w io.Writer, flush func()) error {
	sw := &sseWriter{w: w, flush: flush}
	chatID := "chatcmpl_" + fmt.Sprint(time.Now().UnixNano())
	created := time.Now().Unix()
	model := ""
	sentRole := false
	toolIndex := -1
	toolByBlock := map[int]int{}
	inputTokens, outputTokens := 0, 0
	finish := "stop"

	emit := func(delta map[string]any, fr any) error {
		return sw.event("", map[string]any{
			"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": fr}},
		})
	}
	err := readSSE(r, func(event string, data []byte) error {
		var ev messagesStreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				if ev.Message.ID != "" {
					chatID = ensurePrefix(ev.Message.ID, "chatcmpl_")
				}
				model = ev.Message.Model
				if ev.Message.Usage != nil {
					inputTokens = ev.Message.Usage.InputTokens
				}
			}
			if !sentRole {
				sentRole = true
				return emit(map[string]any{"role": "assistant"}, nil)
			}
			return nil
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				toolIndex++
				toolByBlock[ev.Index] = toolIndex
				finish = "tool_calls"
				tc := map[string]any{
					"index": toolIndex, "id": ev.ContentBlock.ID, "type": "function",
					"function": map[string]any{"name": ev.ContentBlock.Name, "arguments": ""},
				}
				return emit(map[string]any{"tool_calls": []any{tc}}, nil)
			}
			return nil
		case "content_block_delta":
			if ev.Delta == nil {
				return nil
			}
			if ev.Delta.Type == "text_delta" {
				if !sentRole {
					sentRole = true
					if err := emit(map[string]any{"role": "assistant"}, nil); err != nil {
						return err
					}
				}
				return emit(map[string]any{"content": ev.Delta.Text}, nil)
			}
			if ev.Delta.Type == "input_json_delta" {
				idx, ok := toolByBlock[ev.Index]
				if !ok {
					return nil
				}
				tc := map[string]any{"index": idx, "function": map[string]any{"arguments": ev.Delta.PartialJSON}}
				return emit(map[string]any{"tool_calls": []any{tc}}, nil)
			}
			return nil
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason == "max_tokens" {
				finish = "length"
			}
			if ev.Usage != nil {
				outputTokens = ev.Usage.OutputTokens
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := sw.event("", map[string]any{
		"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": inputTokens, "completion_tokens": outputTokens, "total_tokens": inputTokens + outputTokens},
	}); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	if flush != nil {
		flush()
	}
	return nil
}
