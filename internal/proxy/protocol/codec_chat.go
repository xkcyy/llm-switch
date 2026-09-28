package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ---- 请求：Chat → IR ----

// chatDecodeRequest 把 Chat Completions 请求读成 IR。
// Chat 入口的 reasoning_effort 现状不转发给 Responses 上游，等价重构范围内不改变行为。
func chatDecodeRequest(body []byte) (*irRequest, error) {
	var in simpleChatRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Chat 请求失败: %w", err)
	}
	req := &irRequest{
		Model:           in.Model,
		ToolChoice:      chatToolChoiceToIR(in.ToolChoice),
		MaxOutputTokens: in.MaxTokens,
		Temperature:     in.Temperature,
		TopP:            in.TopP,
		Stream:          in.Stream,
	}
	for _, t := range in.Tools {
		req.Tools = append(req.Tools, irTool{
			Kind: "function", Name: t.Function.Name,
			Description: t.Function.Description, Parameters: t.Function.Parameters,
		})
	}
	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			if s := contentString(m.Content); s != "" {
				if req.Instructions == "" {
					req.Instructions = s
				} else {
					req.Instructions += "\n" + s
				}
			}
		case "tool":
			req.Items = append(req.Items, irItem{Kind: irCallOutput, CallID: m.ToolCallID, Output: contentString(m.Content)})
		case "assistant":
			if text, images := splitChatContent(m.Content); text != "" || len(images) > 0 {
				req.Items = append(req.Items, irItem{Kind: irMessage, Role: "assistant", Text: text, Images: images})
			}
			for _, tc := range m.ToolCalls {
				req.Items = append(req.Items, irItem{
					Kind: irFunctionCall, CallID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
				})
			}
		default:
			text, images := splitChatContent(m.Content)
			req.Items = append(req.Items, irItem{Kind: irMessage, Role: "user", Text: text, Images: images})
		}
	}
	return req, nil
}

func chatToolChoiceToIR(v any) *irToolChoice {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return &irToolChoice{Mode: t}
	case map[string]any:
		if fn, ok := t["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return &irToolChoice{Mode: "tool", Name: name}
			}
		}
		if ty, _ := t["type"].(string); ty != "" {
			return &irToolChoice{Mode: ty}
		}
	}
	return nil
}

// ---- 请求：IR → Chat ----

// chatEncodeRequest 把 IR 写成 Chat Completions 请求。
//
// Chat Completions 要求：带 tool_calls 的 assistant 消息必须被对应 tool_call_id 的
// tool 消息立即跟随。客户端的一轮对话可能包含多个并行的工具调用，因此这里把连续的
// 工具调用合并到同一条 assistant 消息，并让它与工具结果成组输出；孤立的结果条目会被
// 丢弃并告警，避免上游 400。
func chatEncodeRequest(req *irRequest, model string, ctx *encodeContext) ([]byte, error) {
	out := chatRequest{
		Model:             model,
		Tools:             irToolsToChat(req.Tools, ctx),
		ToolChoice:        irToolChoiceToChat(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
		MaxTokens:         req.MaxOutputTokens,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		Stream:            req.Stream,
	}
	if req.ReasoningEffort != "" && req.ReasoningEffort != "none" {
		out.ReasoningEffort = req.ReasoningEffort
	}
	if out.Stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if req.Instructions != "" {
		out.Messages = append(out.Messages, chatMessage{Role: "system", Content: req.Instructions})
	}

	// 工具结果索引：call_id → 结果文本；缺少 call_id 的按出现顺序排队，
	// 供同样缺少 call_id 的工具调用按顺序配对。
	results := map[string]string{}
	var emptyIDResults []string
	for _, it := range req.Items {
		switch it.Kind {
		case irCallOutput, irCustomOutput:
			if it.CallID == "" {
				emptyIDResults = append(emptyIDResults, it.Output)
			} else if _, ok := results[it.CallID]; !ok {
				results[it.CallID] = it.Output
			}
		}
	}
	consumed := map[string]bool{}
	emptyConsumed, emptySeen := 0, 0

	for i := 0; i < len(req.Items); i++ {
		it := req.Items[i]

		// 工具调用：把紧随其后的连续调用条目合并成一条 assistant 消息。
		if it.Kind == irFunctionCall || it.Kind == irCustomCall {
			type callItem struct {
				id      string
				name    string
				args    string
				virtual bool // 原始条目缺少 call_id，为合法成组临时生成
			}
			var calls []callItem
			for i < len(req.Items) {
				cur := req.Items[i]
				switch {
				case cur.Kind == irFunctionCall:
					calls = append(calls, callItem{id: cur.CallID, name: cur.Name, args: cur.Arguments})
				case cur.Kind == irCustomCall:
					calls = append(calls, callItem{id: cur.CallID, name: cur.Name, args: freeformArguments(cur.Input)})
				case droppedItem(cur.Kind):
					i++
					continue
				default:
					goto groupDone
				}
				i++
			}
		groupDone:
			i--

			msg := chatMessage{Role: "assistant"}
			virtualIDs := map[string]bool{}
			for idx, c := range calls {
				id := c.id
				if id == "" {
					id = fmt.Sprintf("call_%d_%s", idx, c.name)
					c.virtual = true
				}
				if c.virtual {
					virtualIDs[id] = true
				}
				var tc chatToolCall
				tc.ID = id
				tc.Type = "function"
				tc.Function.Name = c.name
				tc.Function.Arguments = c.args
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
			out.Messages = append(out.Messages, msg)

			// 结果必须紧跟 assistant 消息，缺结果时补占位，避免上游拒绝。
			for _, tc := range msg.ToolCalls {
				text, ok := results[tc.ID]
				switch {
				case ok:
					consumed[tc.ID] = true
				case virtualIDs[tc.ID] && len(emptyIDResults) > emptyConsumed:
					text, ok = emptyIDResults[emptyConsumed], true
					emptyConsumed++
				}
				if !ok {
					ctx.warn("工具调用 %s（%s）缺少结果，已补空结果", tc.ID, tc.Function.Name)
					text = "(no output)"
				}
				out.Messages = append(out.Messages, chatMessage{Role: "tool", ToolCallID: tc.ID, Content: text})
			}
			continue
		}

		switch it.Kind {
		case irMessage:
			role := it.Role
			if role == "developer" {
				role = "system"
			}
			if role == "" {
				role = "user"
			}
			out.Messages = append(out.Messages, chatMessage{Role: role, Content: chatContent(it.Text, it.Images)})
		case irCallOutput, irCustomOutput:
			if it.CallID != "" {
				if consumed[it.CallID] {
					continue // 已随对应工具调用成组输出
				}
				ctx.warn("丢弃没有对应工具调用的结果条目（call_id=%s）", it.CallID)
				continue
			}
			emptySeen++
			if emptySeen <= emptyConsumed {
				continue
			}
			ctx.warn("丢弃没有对应工具调用的结果条目（无 call_id）")
		default:
			// reasoning 等条目忽略
		}
	}
	if len(out.Messages) == 0 {
		out.Messages = []chatMessage{{Role: "user", Content: ""}}
	}
	return marshal(out)
}

func irToolsToChat(tools []irTool, ctx *encodeContext) []chatTool {
	out := make([]chatTool, 0, len(tools))
	for _, t := range tools {
		switch t.Kind {
		case "", "function":
			ct := chatTool{Type: "function"}
			ct.Function.Name = t.Name
			ct.Function.Description = t.Description
			ct.Function.Parameters = t.Parameters
			out = append(out, ct)
		case "custom":
			// 自定义（自由文本）工具降级为「单个 input 字符串」的函数工具，
			// 否则上游模型看不到工具定义，历史里的调用与结果也会被丢弃。
			ct := chatTool{Type: "function"}
			ct.Function.Name = t.Name
			desc := t.Description
			if desc == "" {
				desc = "Freeform tool."
			}
			ct.Function.Description = desc + " Provide the full content in the `input` string field."
			ct.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"Full freeform input (raw text: script or patch)."}},"required":["input"]}`)
			out = append(out, ct)
		default:
			ctx.warn("忽略不支持的工具类型 %q（%s）", t.Kind, t.Name)
		}
	}
	return out
}

func irToolChoiceToChat(tc *irToolChoice) any {
	if tc == nil {
		return nil
	}
	if tc.Name != "" {
		return map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
	}
	if tc.Mode != "" {
		return tc.Mode
	}
	return nil
}

// ---- 响应：Chat → IR ----

func chatDecodeResponse(body []byte) (*irResponse, error) {
	var in chatResponse
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Chat 响应失败: %w", err)
	}
	resp := &irResponse{ID: in.ID, Model: in.Model, Status: "completed"}
	if in.Usage != nil {
		resp.InputTokens, resp.OutputTokens = in.Usage.PromptTokens, in.Usage.CompletionTokens
	}
	if len(in.Choices) > 0 {
		ch := in.Choices[0]
		if ch.FinishReason == "length" {
			resp.Status = "incomplete"
		}
		resp.Text = contentString(ch.Message.Content)
		for _, tc := range ch.Message.ToolCalls {
			resp.Calls = append(resp.Calls, irItem{
				Kind: irFunctionCall, CallID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			})
		}
		reasoning := ch.Message.Reasoning
		if ch.Message.ReasoningContent != nil {
			reasoning = *ch.Message.ReasoningContent
		}
		resp.Reasoning = reasoning
	}
	return resp, nil
}

// ---- 响应：IR → Chat ----

func chatEncodeResponse(resp *irResponse, ctx *encodeContext) ([]byte, error) {
	msg := chatMessage{Role: "assistant"}
	if resp.Text != "" {
		msg.Content = resp.Text
	}
	for _, c := range resp.Calls {
		var tc chatToolCall
		tc.ID = c.CallID
		tc.Type = "function"
		tc.Function.Name = c.Name
		tc.Function.Arguments = c.Arguments
		msg.ToolCalls = append(msg.ToolCalls, tc)
	}
	finish := "stop"
	if len(msg.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	if resp.Status == "incomplete" {
		finish = "length"
	}
	// 上游未给出用量时保持与历史一致的零值结构。
	out := map[string]any{
		"id":      ensurePrefix(resp.ID, "chatcmpl_"),
		"object":  "chat.completion",
		"created": nowUnix(),
		"model":   resp.Model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   chatUsage(resp.InputTokens, resp.OutputTokens),
	}
	return marshal(out)
}

func chatUsage(in, out int) map[string]any {
	return map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}
}

// ---- 流：Chat → 事件 ----

func chatDecodeStream(r io.Reader, emit func(irEvent) error) error {
	seen := map[int]bool{}
	return readSSE(r, func(event string, data []byte) error {
		if strings.TrimSpace(string(data)) == "[DONE]" {
			return nil
		}
		var c chatChunk
		if err := json.Unmarshal(data, &c); err != nil {
			return nil // 忽略无法解析的块
		}
		if c.Model != "" {
			if err := emit(irEvent{Kind: irStart, Model: c.Model}); err != nil {
				return err
			}
		}
		if c.Usage != nil {
			if err := emit(irEvent{Kind: irUsage, InputTokens: c.Usage.PromptTokens, OutputTokens: c.Usage.CompletionTokens}); err != nil {
				return err
			}
		}
		if len(c.Choices) == 0 {
			return nil
		}
		ch := c.Choices[0]
		if ch.Delta.ReasoningContent != "" {
			if err := emit(irEvent{Kind: irReasoning, Text: ch.Delta.ReasoningContent}); err != nil {
				return err
			}
		}
		if ch.Delta.Reasoning != "" {
			if err := emit(irEvent{Kind: irReasoning, Text: ch.Delta.Reasoning}); err != nil {
				return err
			}
		}
		if ch.Delta.Content != "" {
			if err := emit(irEvent{Kind: irText, Text: ch.Delta.Content}); err != nil {
				return err
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			// 首次出现即建立调用；后续分片补全 id / 名称时重复上报，由编码器幂等更新。
			if !seen[tc.Index] || tc.ID != "" || tc.Function.Name != "" {
				seen[tc.Index] = true
				if err := emit(irEvent{Kind: irCallStart, Index: tc.Index, CallID: tc.ID, Name: tc.Function.Name}); err != nil {
					return err
				}
			}
			if tc.Function.Arguments != "" {
				if err := emit(irEvent{Kind: irCallArgs, Index: tc.Index, Text: tc.Function.Arguments}); err != nil {
					return err
				}
			}
		}
		if ch.FinishReason != "" {
			if err := emit(irEvent{Kind: irFinish, Finish: ch.FinishReason}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- 流：事件 → Chat ----

type chatStreamEncoder struct {
	sw        *sseWriter
	chatID    string
	model     string
	sentRole  bool
	toolIndex int
	hasCall   bool
	inputTok  int
	outputTok int
}

func newChatStreamEncoder(w io.Writer, flush func()) *chatStreamEncoder {
	return &chatStreamEncoder{
		sw:     &sseWriter{w: w, flush: flush},
		chatID: "chatcmpl_" + fmt.Sprint(nowUnixNano()),
	}
}

func (e *chatStreamEncoder) chunk(delta map[string]any, finish any) error {
	return e.sw.data(map[string]any{
		"id": e.chatID, "object": "chat.completion.chunk", "created": nowUnix(), "model": e.model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	})
}

func (e *chatStreamEncoder) sendRole() error {
	if e.sentRole {
		return nil
	}
	e.sentRole = true
	return e.chunk(map[string]any{"role": "assistant"}, nil)
}

func (e *chatStreamEncoder) Emit(ev irEvent) error {
	switch ev.Kind {
	case irStart:
		if ev.ID != "" {
			e.chatID = ensurePrefix(ev.ID, "chatcmpl_")
		}
		if ev.Model != "" {
			e.model = ev.Model
		}
		return e.sendRole()
	case irText:
		if err := e.sendRole(); err != nil {
			return err
		}
		return e.chunk(map[string]any{"content": ev.Text}, nil)
	case irCallStart:
		e.hasCall = true
		tc := map[string]any{
			"index": e.toolIndex, "id": ev.CallID, "type": "function",
			"function": map[string]any{"name": ev.Name, "arguments": ""},
		}
		e.toolIndex++
		return e.chunk(map[string]any{"tool_calls": []any{tc}}, nil)
	case irCallArgs:
		tc := map[string]any{"index": e.toolIndex - 1, "function": map[string]any{"arguments": ev.Text}}
		return e.chunk(map[string]any{"tool_calls": []any{tc}}, nil)
	case irUsage:
		e.inputTok, e.outputTok = ev.InputTokens, ev.OutputTokens
	}
	return nil
}

func (e *chatStreamEncoder) Close() error {
	finish := "stop"
	if e.hasCall {
		finish = "tool_calls"
	}
	if err := e.sw.data(map[string]any{
		"id": e.chatID, "object": "chat.completion.chunk", "created": nowUnix(), "model": e.model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
		"usage":   chatUsage(e.inputTok, e.outputTok),
	}); err != nil {
		return err
	}
	if _, err := io.WriteString(e.sw.w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	if e.sw.flush != nil {
		e.sw.flush()
	}
	return nil
}
