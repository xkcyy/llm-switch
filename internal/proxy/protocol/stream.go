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

// StreamChatToResponses 把 Chat SSE 流转换为 Responses SSE 事件。
func StreamChatToResponses(r io.Reader, w io.Writer, flush func()) error {
	return StreamChatToResponsesWith(r, w, flush, ResponseConvertOptions{})
}

// StreamChatToResponsesWith 支持把自定义（自由文本）工具调用还原为 custom_tool_call，
// 并把上游的思考内容回调给调用方（用于后续请求回填）。
func StreamChatToResponsesWith(r io.Reader, w io.Writer, flush func(), opts ResponseConvertOptions) error {
	sw := &sseWriter{w: w, flush: flush}
	respID := "resp_" + fmt.Sprint(time.Now().UnixNano())
	created := time.Now().Unix()
	model := ""
	started := false
	var msg *streamItem
	tools := map[int]*streamItem{}
	var order []*streamItem
	textStarted := false
	finish := ""
	inputTokens, outputTokens := 0, 0
	var reasoning strings.Builder

	ensureStarted := func() error {
		if started {
			return nil
		}
		started = true
		if err := sw.event("response.created", map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id": respID, "object": "response", "created_at": created,
				"status": "in_progress", "model": model, "output": []any{},
			},
		}); err != nil {
			return err
		}
		return sw.event("response.in_progress", map[string]any{
			"type":     "response.in_progress",
			"response": map[string]any{"id": respID, "object": "response", "created_at": created, "status": "in_progress", "model": model},
		})
	}

	ensureMsg := func() error {
		if msg != nil {
			return nil
		}
		msg = &streamItem{outputIndex: 0, id: "msg_1", kind: "message"}
		err := sw.event("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"id": "msg_1", "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
		})
		if err != nil {
			return err
		}
		msg.started = true
		order = append(order, msg)
		return nil
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

	closeMsg := func() error {
		if msg == nil {
			return nil
		}
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
		msg.closed = true
		return sw.event("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": 0,
			"item": map[string]any{
				"id": "msg_1", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
			},
		})
	}

	// startItem 在拿到工具名后（或首个参数分片时）再发出 output_item.added，
	// 以便按工具类型区分 function_call 与自定义工具的 custom_tool_call。
	startItem := func(item *streamItem) error {
		if item.started {
			return nil
		}
		item.started = true
		if item.custom {
			item.kind = "custom_tool_call"
			return sw.event("response.output_item.added", map[string]any{
				"type": "response.output_item.added", "output_index": item.outputIndex,
				"item": map[string]any{
					"id": item.id, "type": "custom_tool_call", "status": "in_progress",
					"call_id": item.callID, "name": item.name, "input": "",
				},
			})
		}
		return sw.event("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": item.outputIndex,
			"item": map[string]any{
				"id": item.id, "type": "function_call", "status": "in_progress",
				"call_id": item.callID, "name": item.name, "arguments": "",
			},
		})
	}

	handleChunk := func(c *chatChunk) error {
		if c.Model != "" {
			model = c.Model
		}
		if err := ensureStarted(); err != nil {
			return err
		}
		if c.Usage != nil {
			inputTokens, outputTokens = c.Usage.PromptTokens, c.Usage.CompletionTokens
		}
		if len(c.Choices) == 0 {
			return nil
		}
		ch := c.Choices[0]
		if ch.Delta.ReasoningContent != "" {
			reasoning.WriteString(ch.Delta.ReasoningContent)
		}
		if ch.Delta.Reasoning != "" {
			reasoning.WriteString(ch.Delta.Reasoning)
		}
		if ch.Delta.Content != "" {
			if err := ensureMsg(); err != nil {
				return err
			}
			if err := ensureTextPart(); err != nil {
				return err
			}
			msg.text.WriteString(ch.Delta.Content)
			if err := sw.event("response.output_text.delta", map[string]any{
				"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": ch.Delta.Content,
			}); err != nil {
				return err
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			item, ok := tools[tc.Index]
			if !ok {
				item = &streamItem{outputIndex: len(order), id: fmt.Sprintf("fc_%d", len(tools)+1), kind: "function_call"}
				tools[tc.Index] = item
				order = append(order, item)
			}
			if tc.ID != "" {
				item.callID = tc.ID
			}
			if tc.Function.Name != "" {
				item.name = tc.Function.Name
				if opts.CustomTools[tc.Function.Name] {
					item.custom = true
					item.kind = "custom_tool_call"
					item.id = fmt.Sprintf("ctc_%d", len(tools))
				}
			}
			if item.name != "" || tc.Function.Arguments != "" {
				if err := startItem(item); err != nil {
					return err
				}
			}
			if tc.Function.Arguments != "" {
				item.args.WriteString(tc.Function.Arguments)
				if item.custom {
					if err := sw.event("response.custom_tool_call_input.delta", map[string]any{
						"type": "response.custom_tool_call_input.delta", "item_id": item.id,
						"output_index": item.outputIndex, "delta": tc.Function.Arguments,
					}); err != nil {
						return err
					}
					continue
				}
				if err := sw.event("response.function_call_arguments.delta", map[string]any{
					"type": "response.function_call_arguments.delta", "item_id": item.id,
					"output_index": item.outputIndex, "delta": tc.Function.Arguments,
				}); err != nil {
					return err
				}
			}
		}
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
		return nil
	}

	// finalizeReasoning 回调本次响应的思考内容（仅在确实产生了工具调用时有意义）。
	// 即使流被中断也要调用：缓存能救下一轮的请求。
	finalizeReasoning := func() {
		if opts.OnReasoning == nil || reasoning.Len() == 0 {
			return
		}
		ids := make([]string, 0, len(order))
		for _, item := range order {
			if item.kind != "message" {
				ids = append(ids, item.callID)
			}
		}
		if len(ids) > 0 {
			opts.OnReasoning(reasoning.String(), ids)
		}
	}

	readErr := readSSE(r, func(event string, data []byte) error {
		if strings.TrimSpace(string(data)) == "[DONE]" {
			return nil
		}
		var c chatChunk
		if err := json.Unmarshal(data, &c); err != nil {
			return nil // 忽略无法解析的块
		}
		return handleChunk(&c)
	})
	finalizeReasoning()
	if readErr != nil {
		return readErr
	}

	if !started {
		if err := ensureStarted(); err != nil {
			return err
		}
	}
	if err := closeMsg(); err != nil {
		return err
	}
	for _, item := range order {
		if item.kind == "message" || item.closed {
			continue
		}
		item.closed = true
		if err := startItem(item); err != nil {
			return err
		}
		args := item.args.String()
		if item.custom {
			input := freeformInput(args)
			if err := sw.event("response.custom_tool_call_input.done", map[string]any{
				"type": "response.custom_tool_call_input.done", "item_id": item.id,
				"output_index": item.outputIndex, "input": input,
			}); err != nil {
				return err
			}
			if err := sw.event("response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": item.outputIndex,
				"item": map[string]any{
					"id": item.id, "type": "custom_tool_call", "status": "completed",
					"call_id": item.callID, "name": item.name, "input": input,
				},
			}); err != nil {
				return err
			}
			continue
		}
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
	}

	status := "completed"
	if finish == "length" {
		status = "incomplete"
	}
	output := make([]any, 0, len(order))
	for _, item := range order {
		switch item.kind {
		case "message":
			output = append(output, map[string]any{
				"id": "msg_1", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": item.text.String(), "annotations": []any{}}},
			})
		case "custom_tool_call":
			output = append(output, map[string]any{
				"id": item.id, "type": "custom_tool_call", "status": "completed",
				"call_id": item.callID, "name": item.name, "input": freeformInput(item.args.String()),
			})
		default:
			args := item.args.String()
			if args == "" {
				args = "{}"
			}
			output = append(output, map[string]any{
				"id": item.id, "type": "function_call", "status": "completed",
				"call_id": item.callID, "name": item.name, "arguments": args,
			})
		}
	}
	return sw.event("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": created, "status": status, "model": model,
			"output": output, "usage": usageObject(inputTokens, outputTokens),
		},
	})
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

// StreamResponsesToChat 把 Responses SSE 事件转换为 Chat SSE 流。
func StreamResponsesToChat(r io.Reader, w io.Writer, flush func()) error {
	sw := &sseWriter{w: w, flush: flush}
	chatID := "chatcmpl_" + fmt.Sprint(time.Now().UnixNano())
	created := time.Now().Unix()
	model := ""
	sentRole := false
	toolIndex := 0
	inputTokens, outputTokens := 0, 0
	finish := "stop"

	emit := func(delta map[string]any, fr any) error {
		return sw.data(map[string]any{
			"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": fr}},
		})
	}
	err := readSSE(r, func(event string, data []byte) error {
		var ev responsesStreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "response.created", "response.in_progress":
			if ev.Response != nil {
				if ev.Response.ID != "" {
					chatID = ensurePrefix(ev.Response.ID, "chatcmpl_")
				}
				if ev.Response.Model != "" {
					model = ev.Response.Model
				}
			}
			if !sentRole {
				sentRole = true
				return emit(map[string]any{"role": "assistant"}, nil)
			}
			return nil
		case "response.output_text.delta":
			if !sentRole {
				sentRole = true
				if err := emit(map[string]any{"role": "assistant"}, nil); err != nil {
					return err
				}
			}
			return emit(map[string]any{"content": ev.Delta}, nil)
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				tc := map[string]any{
					"index": toolIndex, "id": ev.Item.CallID, "type": "function",
					"function": map[string]any{"name": ev.Item.Name, "arguments": ""},
				}
				toolIndex++
				finish = "tool_calls"
				return emit(map[string]any{"tool_calls": []any{tc}}, nil)
			}
			return nil
		case "response.function_call_arguments.delta":
			tc := map[string]any{"index": toolIndex - 1, "function": map[string]any{"arguments": ev.Delta}}
			return emit(map[string]any{"tool_calls": []any{tc}}, nil)
		case "response.completed":
			if ev.Response != nil {
				if ev.Response.Usage != nil {
					inputTokens, outputTokens = ev.Response.Usage.InputTokens, ev.Response.Usage.OutputTokens
				}
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := sw.data(map[string]any{
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
