package protocol

import (
	"encoding/json"
	"fmt"
	"io"
)

// ---- 请求：Responses → IR ----

func responsesDecodeRequest(body []byte) (*irRequest, error) {
	var in responsesRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Responses 请求失败: %w", err)
	}
	req := &irRequest{
		Model:             in.Model,
		Instructions:      textOf(in.Instructions),
		ToolChoice:        responsesToolChoiceToIR(in.ToolChoice),
		ParallelToolCalls: in.ParallelToolCalls,
		MaxOutputTokens:   in.MaxOutputTokens,
		Temperature:       in.Temperature,
		TopP:              in.TopP,
		Stream:            in.Stream,
	}
	if in.Reasoning != nil {
		req.ReasoningEffort = in.Reasoning.Effort
	}
	for _, t := range in.Tools {
		kind := t.Type
		if kind == "" {
			kind = "function"
		}
		req.Tools = append(req.Tools, irTool{Kind: kind, Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}

	var items []responsesInputItem
	if len(in.Input) > 0 && string(in.Input) != "null" {
		if err := json.Unmarshal(in.Input, &items); err != nil {
			// input 为纯字符串
			var s string
			if err2 := json.Unmarshal(in.Input, &s); err2 == nil {
				items = []responsesInputItem{{Type: "message", Role: "user", Content: in.Input}}
			} else {
				return nil, fmt.Errorf("解析 input 失败: %w", err)
			}
		}
	}
	for _, it := range items {
		switch it.Type {
		case "", "message":
			text, images := splitResponsesContent(it.Content)
			req.Items = append(req.Items, irItem{Kind: irMessage, Role: it.Role, Text: text, Images: images})
		case "function_call":
			req.Items = append(req.Items, irItem{Kind: irFunctionCall, CallID: it.CallID, Name: it.Name, Arguments: it.Arguments})
		case "custom_tool_call":
			req.Items = append(req.Items, irItem{Kind: irCustomCall, CallID: it.CallID, Name: it.Name, Input: it.Input})
		case "function_call_output":
			req.Items = append(req.Items, irItem{Kind: irCallOutput, CallID: it.CallID, Output: textOf(it.Output)})
		case "custom_tool_call_output":
			req.Items = append(req.Items, irItem{Kind: irCustomOutput, CallID: it.CallID, Output: textOf(it.Output)})
		case "reasoning":
			// 标准路径：客户端把上一轮的 reasoning 条目原样带回，
			// 推理文本在 summary（人类可读）或 content 里。
			req.Items = append(req.Items, irItem{Kind: "reasoning", Text: reasoningText(it.Summary, it.Content)})
		default:
			// web_search_call 等条目原样保留，由编码器决定忽略。
			req.Items = append(req.Items, irItem{Kind: it.Type})
		}
	}
	return req, nil
}

// reasoningText 从 reasoning 条目的 summary / content 里取推理文本。
// 两者都是内容块数组（summary_text / reasoning_text），取纯文本拼接。
func reasoningText(summary, content json.RawMessage) string {
	text := textOf(summary)
	if text == "" {
		text = textOf(content)
	}
	return text
}

func responsesToolChoiceToIR(raw json.RawMessage) *irToolChoice {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return &irToolChoice{Mode: s}
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.Name != "" {
			return &irToolChoice{Mode: "tool", Name: obj.Name}
		}
		if obj.Type != "" {
			return &irToolChoice{Mode: obj.Type}
		}
	}
	return nil
}

// ---- 请求：IR → Responses ----

func responsesEncodeRequest(req *irRequest, model string, ctx *encodeContext) ([]byte, error) {
	out := simpleResponsesRequest{
		Model:           model,
		Instructions:    req.Instructions,
		MaxOutputTokens: req.MaxOutputTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		Stream:          req.Stream,
	}
	for _, it := range req.Items {
		switch it.Kind {
		case irMessage:
			out.Input = append(out.Input, messageItem(it.Role, it.Text, it.Images))
		case irFunctionCall:
			out.Input = append(out.Input, map[string]any{
				"type": "function_call", "call_id": it.CallID, "name": it.Name, "arguments": it.Arguments,
			})
		case irCustomCall:
			out.Input = append(out.Input, map[string]any{
				"type": "custom_tool_call", "call_id": it.CallID, "name": it.Name, "input": it.Input,
			})
		case irCallOutput:
			out.Input = append(out.Input, map[string]any{
				"type": "function_call_output", "call_id": it.CallID, "output": it.Output,
			})
		case irCustomOutput:
			out.Input = append(out.Input, map[string]any{
				"type": "custom_tool_call_output", "call_id": it.CallID, "output": it.Output,
			})
		}
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, map[string]any{
			"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters,
		})
	}
	if len(out.Input) == 0 {
		out.Input = []map[string]any{textMessageItem("user", "")}
	}
	return marshal(out)
}

// ---- 响应：Responses → IR ----

type responsesResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Output []struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// responsesDecodeResponse 只取 Chat 客户端能表达的条目（文本与函数调用）；
// custom_tool_call 等条目在 Chat 侧无对应形态，与历史行为一致地忽略。
func responsesDecodeResponse(body []byte) (*irResponse, error) {
	var in responsesResponse
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("解析 Responses 响应失败: %w", err)
	}
	resp := &irResponse{ID: in.ID, Model: in.Model, Status: in.Status}
	if resp.Status == "" {
		resp.Status = "completed"
	}
	if in.Usage != nil {
		resp.InputTokens, resp.OutputTokens = in.Usage.InputTokens, in.Usage.OutputTokens
	}
	for _, item := range in.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" || c.Type == "text" {
					resp.Text += c.Text
				}
			}
		case "function_call":
			resp.Calls = append(resp.Calls, irItem{
				Kind: irFunctionCall, CallID: item.CallID, Name: item.Name, Arguments: item.Arguments,
			})
		}
	}
	return resp, nil
}

// ---- 响应：IR → Responses ----

func responsesEncodeResponse(resp *irResponse, ctx *encodeContext) ([]byte, error) {
	out := map[string]any{
		"id":         ensurePrefix(resp.ID, "resp_"),
		"object":     "response",
		"created_at": nowUnix(),
		"status":     "completed",
		"model":      resp.Model,
	}
	var output []any
	// 标准路径：把上游的思考内容作为 reasoning 条目下发，客户端下一轮原样回传，
	// 因此不再依赖进程内缓存（见 ADR-0001）。
	if resp.Reasoning != "" {
		output = append(output, reasoningItem("rs_1", resp.Reasoning))
	}
	if resp.Text != "" {
		output = append(output, map[string]any{
			"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": resp.Text, "annotations": []any{}}},
		})
	}
	for i, c := range resp.Calls {
		if ctx.customTools[c.Name] {
			output = append(output, map[string]any{
				"type": "custom_tool_call", "id": fmt.Sprintf("ctc_%d", i+1), "status": "completed",
				"call_id": c.CallID, "name": c.Name, "input": freeformInput(c.Arguments),
			})
			continue
		}
		output = append(output, map[string]any{
			"type": "function_call", "id": fmt.Sprintf("fc_%d", i+1), "status": "completed",
			"call_id": c.CallID, "name": c.Name, "arguments": c.Arguments,
		})
	}
	if output == nil {
		output = []any{}
	}
	if resp.Status == "incomplete" {
		out["status"] = "incomplete"
	}
	out["output"] = output
	out["usage"] = usageObject(resp.InputTokens, resp.OutputTokens)
	return marshal(out)
}

// reasoningItem 构造 Responses 的 reasoning 条目：推理文本放在 summary 里
// （与 OpenAI 的 reasoning summary 同形）。上游给不出 encrypted_content，
// 该字段只在存储关闭的官方端点上出现，这里不伪造。
func reasoningItem(id, text string) map[string]any {
	return map[string]any{
		"type": "reasoning", "id": id,
		"summary": []map[string]any{{"type": "summary_text", "text": text}},
	}
}

// ---- 流：Responses → 事件 ----

func responsesDecodeStream(r io.Reader, emit func(irEvent) error) error {
	return readSSE(r, func(event string, data []byte) error {
		var ev responsesStreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "response.created", "response.in_progress":
			e := irEvent{Kind: irStart}
			if ev.Response != nil {
				e.ID, e.Model = ev.Response.ID, ev.Response.Model
			}
			return emit(e)
		case "response.output_text.delta":
			return emit(irEvent{Kind: irText, Text: ev.Delta})
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				return emit(irEvent{Kind: irCallStart, CallID: ev.Item.CallID, Name: ev.Item.Name})
			}
			return nil
		case "response.function_call_arguments.delta":
			return emit(irEvent{Kind: irCallArgs, Text: ev.Delta})
		case "response.completed":
			if ev.Response != nil && ev.Response.Usage != nil {
				return emit(irEvent{
					Kind:         irUsage,
					InputTokens:  ev.Response.Usage.InputTokens,
					OutputTokens: ev.Response.Usage.OutputTokens,
				})
			}
			return nil
		}
		return nil
	})
}

// ---- 流：事件 → Responses ----

// responsesStreamEncoder 维护 Responses 输出侧状态机：output_item 的生命周期、
// 自定义工具调用与函数调用的区分、content_part 与 usage 的收尾。
type responsesStreamEncoder struct {
	sw      *sseWriter
	ctx     *encodeContext
	respID  string
	created int64
	model   string
	started bool
	msg     *streamItem
	rs      *streamItem // reasoning 条目：思考内容的标准落点
	items   map[int]*streamItem
	order   []*streamItem
	textOn  bool
	finish  string
	inTok   int
	outTok  int
}

func newResponsesStreamEncoder(w io.Writer, flush func(), ctx *encodeContext) *responsesStreamEncoder {
	return &responsesStreamEncoder{
		sw:      &sseWriter{w: w, flush: flush},
		ctx:     ctx,
		respID:  "resp_" + fmt.Sprint(nowUnixNano()),
		created: nowUnix(),
		items:   map[int]*streamItem{},
	}
}

func (e *responsesStreamEncoder) ensureStarted() error {
	if e.started {
		return nil
	}
	e.started = true
	if err := e.sw.event("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": e.respID, "object": "response", "created_at": e.created,
			"status": "in_progress", "model": e.model, "output": []any{},
		},
	}); err != nil {
		return err
	}
	return e.sw.event("response.in_progress", map[string]any{
		"type":     "response.in_progress",
		"response": map[string]any{"id": e.respID, "object": "response", "created_at": e.created, "status": "in_progress", "model": e.model},
	})
}

func (e *responsesStreamEncoder) ensureMsg() error {
	if e.msg != nil {
		return nil
	}
	e.msg = &streamItem{outputIndex: len(e.order), id: "msg_1", kind: "message"}
	if err := e.sw.event("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": e.msg.outputIndex,
		"item": map[string]any{"id": e.msg.id, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
	}); err != nil {
		return err
	}
	e.msg.started = true
	e.order = append(e.order, e.msg)
	return nil
}

func (e *responsesStreamEncoder) ensureTextPart() error {
	if e.textOn {
		return nil
	}
	e.textOn = true
	return e.sw.event("response.content_part.added", map[string]any{
		"type": "response.content_part.added", "item_id": e.msg.id, "output_index": e.msg.outputIndex, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

// ensureReasoning 在首个思考内容分片时建立 reasoning 条目（排在正文之前）。
func (e *responsesStreamEncoder) ensureReasoning() error {
	if e.rs != nil {
		return nil
	}
	e.rs = &streamItem{outputIndex: len(e.order), id: "rs_1", kind: "reasoning"}
	e.order = append(e.order, e.rs)
	return e.sw.event("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": e.rs.outputIndex,
		"item": map[string]any{"id": e.rs.id, "type": "reasoning", "summary": []any{}},
	})
}

// closeReasoning 收尾 reasoning 条目，发出标准事件并标记已关闭。
func (e *responsesStreamEncoder) closeReasoning() error {
	if e.rs == nil {
		return nil
	}
	e.rs.closed = true
	text := e.rs.text.String()
	if err := e.sw.event("response.reasoning_summary_text.done", map[string]any{
		"type": "response.reasoning_summary_text.done", "item_id": e.rs.id,
		"output_index": e.rs.outputIndex, "summary_index": 0, "text": text,
	}); err != nil {
		return err
	}
	return e.sw.event("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": e.rs.outputIndex,
		"item": reasoningItem(e.rs.id, text),
	})
}

func (e *responsesStreamEncoder) closeMsg() error {
	if e.msg == nil {
		return nil
	}
	text := e.msg.text.String()
	if e.textOn {
		if err := e.sw.event("response.output_text.done", map[string]any{
			"type": "response.output_text.done", "item_id": e.msg.id, "output_index": e.msg.outputIndex, "content_index": 0, "text": text,
		}); err != nil {
			return err
		}
		if err := e.sw.event("response.content_part.done", map[string]any{
			"type": "response.content_part.done", "item_id": e.msg.id, "output_index": e.msg.outputIndex, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
		}); err != nil {
			return err
		}
	}
	e.msg.closed = true
	return e.sw.event("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": e.msg.outputIndex,
		"item": map[string]any{
			"id": e.msg.id, "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		},
	})
}

// startItem 在拿到工具名后（或首个参数分片时）再发出 output_item.added，
// 以便按工具类型区分 function_call 与自定义工具的 custom_tool_call。
func (e *responsesStreamEncoder) startItem(item *streamItem) error {
	if item.started {
		return nil
	}
	item.started = true
	if item.custom {
		item.kind = "custom_tool_call"
		return e.sw.event("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": item.outputIndex,
			"item": map[string]any{
				"id": item.id, "type": "custom_tool_call", "status": "in_progress",
				"call_id": item.callID, "name": item.name, "input": "",
			},
		})
	}
	return e.sw.event("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": item.outputIndex,
		"item": map[string]any{
			"id": item.id, "type": "function_call", "status": "in_progress",
			"call_id": item.callID, "name": item.name, "arguments": "",
		},
	})
}

func (e *responsesStreamEncoder) Emit(ev irEvent) error {
	if ev.Kind == irStart && ev.Model != "" {
		e.model = ev.Model
	}
	if err := e.ensureStarted(); err != nil {
		return err
	}
	switch ev.Kind {
	case irReasoning:
		if err := e.ensureReasoning(); err != nil {
			return err
		}
		e.rs.text.WriteString(ev.Text)
		return e.sw.event("response.reasoning_summary_text.delta", map[string]any{
			"type": "response.reasoning_summary_text.delta", "item_id": e.rs.id,
			"output_index": e.rs.outputIndex, "summary_index": 0, "delta": ev.Text,
		})
	case irText:
		if err := e.ensureMsg(); err != nil {
			return err
		}
		if err := e.ensureTextPart(); err != nil {
			return err
		}
		e.msg.text.WriteString(ev.Text)
		return e.sw.event("response.output_text.delta", map[string]any{
			"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": ev.Text,
		})
	case irCallStart:
		item, ok := e.items[ev.Index]
		if !ok {
			item = &streamItem{outputIndex: len(e.order), id: fmt.Sprintf("fc_%d", len(e.items)+1), kind: "function_call"}
			e.items[ev.Index] = item
			e.order = append(e.order, item)
		}
		if ev.CallID != "" {
			item.callID = ev.CallID
		}
		if ev.Name != "" {
			item.name = ev.Name
			if e.ctx.customTools[ev.Name] {
				item.custom = true
				item.kind = "custom_tool_call"
				item.id = fmt.Sprintf("ctc_%d", len(e.items))
			}
		}
		if item.name != "" {
			return e.startItem(item)
		}
		return nil
	case irCallArgs:
		item, ok := e.items[ev.Index]
		if !ok {
			item = &streamItem{outputIndex: len(e.order), id: fmt.Sprintf("fc_%d", len(e.items)+1), kind: "function_call"}
			e.items[ev.Index] = item
			e.order = append(e.order, item)
		}
		if err := e.startItem(item); err != nil {
			return err
		}
		item.args.WriteString(ev.Text)
		if item.custom {
			return e.sw.event("response.custom_tool_call_input.delta", map[string]any{
				"type": "response.custom_tool_call_input.delta", "item_id": item.id,
				"output_index": item.outputIndex, "delta": ev.Text,
			})
		}
		return e.sw.event("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": item.id,
			"output_index": item.outputIndex, "delta": ev.Text,
		})
	case irFinish:
		e.finish = ev.Finish
	case irUsage:
		e.inTok, e.outTok = ev.InputTokens, ev.OutputTokens
	}
	return nil
}

func (e *responsesStreamEncoder) Close() error {
	if err := e.ensureStarted(); err != nil {
		return err
	}
	if err := e.closeReasoning(); err != nil {
		return err
	}
	if err := e.closeMsg(); err != nil {
		return err
	}
	for _, item := range e.order {
		if item.kind == "message" || item.closed {
			continue
		}
		item.closed = true
		if err := e.startItem(item); err != nil {
			return err
		}
		args := item.args.String()
		if item.custom {
			input := freeformInput(args)
			if err := e.sw.event("response.custom_tool_call_input.done", map[string]any{
				"type": "response.custom_tool_call_input.done", "item_id": item.id,
				"output_index": item.outputIndex, "input": input,
			}); err != nil {
				return err
			}
			if err := e.sw.event("response.output_item.done", map[string]any{
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
		if err := e.sw.event("response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "item_id": item.id, "output_index": item.outputIndex, "arguments": args,
		}); err != nil {
			return err
		}
		if err := e.sw.event("response.output_item.done", map[string]any{
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
	if e.finish == "length" {
		status = "incomplete"
	}
	output := make([]any, 0, len(e.order))
	for _, item := range e.order {
		switch item.kind {
		case "reasoning":
			output = append(output, reasoningItem(item.id, item.text.String()))
		case "message":
			output = append(output, map[string]any{
				"id": item.id, "type": "message", "status": "completed", "role": "assistant",
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
	return e.sw.event("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": e.respID, "object": "response", "created_at": e.created, "status": status, "model": e.model,
			"output": output, "usage": usageObject(e.inTok, e.outTok),
		},
	})
}
