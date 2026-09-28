package protocol

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- 测试辅助：走新接口（Prepare / Deliver） ----

const testProvider = "test-provider"

// toChatRequest 等价于旧的 Responses→Chat 请求转换。
func toChatRequest(body []byte, model string) ([]byte, error) {
	_, out, err := Prepare(Responses, Chat, body, Identity{ProviderID: testProvider, ModelID: model})
	return out, err
}

// chatToResponsesResponse 等价于旧的 Chat 上游响应 → Responses 入口响应。
func chatToResponsesResponse(body []byte) ([]byte, error) {
	relay, _, err := Prepare(Responses, Chat, []byte(`{"model":"m","input":"hi"}`), Identity{ProviderID: testProvider, ModelID: "m"})
	if err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	if _, err := relay.Deliver(200, h, strings.NewReader(string(body)), rec); err != nil {
		return nil, err
	}
	return rec.Body.Bytes(), nil
}

// streamChatToResponses 等价于旧的 Chat 上游流 → Responses 入口流。
func streamChatToResponses(in string, w io.Writer) error {
	relay, _, err := Prepare(Responses, Chat, []byte(`{"model":"m","input":"hi","stream":true}`), Identity{ProviderID: testProvider, ModelID: "m"})
	if err != nil {
		return err
	}
	rec := httptest.NewRecorder()
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	if _, err := relay.Deliver(200, h, strings.NewReader(in), rec); err != nil {
		return err
	}
	_, err = w.Write(rec.Body.Bytes())
	return err
}

// deliver 把上游响应交给 Relay，返回写给客户端的字节。
func deliver(t *testing.T, relay *Relay, body, contentType string) ([]byte, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	_, err := relay.Deliver(200, h, strings.NewReader(body), rec)
	return rec.Body.Bytes(), err
}

// streamDeliver 同上，用于上游 SSE 流。
func streamDeliver(t *testing.T, relay *Relay, in string, w io.Writer) error {
	t.Helper()
	rec := httptest.NewRecorder()
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	if _, err := relay.Deliver(200, h, strings.NewReader(in), rec); err != nil {
		return err
	}
	_, err := w.Write(rec.Body.Bytes())
	return err
}

func TestResponsesToChatRequest(t *testing.T) {
	body := []byte(`{
		"model": "DeepSeek/deepseek-chat",
		"instructions": "be nice",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"type": "function_call", "name": "read_file", "arguments": "{\"path\":\"a.txt\"}", "call_id": "call_1"},
			{"type": "function_call_output", "call_id": "call_1", "output": "done"}
		],
		"tools": [{"type": "function", "name": "read_file", "description": "read", "parameters": {"type": "object"}}],
		"max_output_tokens": 123,
		"stream": true
	}`)
	out, err := toChatRequest(body, "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools         []any `json:"tools"`
		MaxTokens     int   `json:"max_tokens"`
		Stream        bool  `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "deepseek-chat" {
		t.Fatalf("model = %s", req.Model)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("want 4 messages, got %d: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "be nice" {
		t.Fatalf("system message wrong: %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content != "hi" {
		t.Fatalf("user message wrong: %+v", req.Messages[1])
	}
	if req.Messages[2].Role != "assistant" || len(req.Messages[2].ToolCalls) != 1 || req.Messages[2].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool call message wrong: %+v", req.Messages[2])
	}
	if req.Messages[3].Role != "tool" || req.Messages[3].ToolCallID != "call_1" {
		t.Fatalf("tool result message wrong: %+v", req.Messages[3])
	}
	if len(req.Tools) != 1 || req.MaxTokens != 123 || !req.Stream || !req.StreamOptions.IncludeUsage {
		t.Fatalf("tools/limits wrong: %+v", req)
	}
}

func TestResponsesToMessagesRequest(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"instructions": "sys",
		"input": [{"type": "message", "role": "user", "content": "hello"}],
		"max_output_tokens": 512,
		"reasoning": {"effort": "high"}
	}`)
	out, err := ResponsesToMessagesRequest(body, "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Model     string `json:"model"`
		System    string `json:"system"`
		MaxTokens int    `json:"max_tokens"`
		Thinking  *struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		} `json:"thinking"`
		Messages []struct {
			Role    string `json:"role"`
			Content []any  `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "claude-sonnet-4-6" || req.System != "sys" || req.MaxTokens != 512 {
		t.Fatalf("wrong request: %+v", req)
	}
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens <= 0 {
		t.Fatalf("thinking not mapped: %+v", req.Thinking)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("messages wrong: %+v", req.Messages)
	}
}

func TestChatToResponsesResponse(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl-1", "model": "deepseek-chat",
		"choices": [{"message": {"role": "assistant", "content": "hello"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}
	}`)
	out, err := chatToResponsesResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Object != "response" || resp.Status != "completed" {
		t.Fatalf("wrong object/status: %+v", resp)
	}
	if len(resp.Output) != 1 || resp.Output[0].Content[0].Text != "hello" {
		t.Fatalf("output wrong: %+v", resp.Output)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("usage wrong: %+v", resp.Usage)
	}
}

func TestStreamChatToResponses(t *testing.T) {
	in := strings.Join([]string{
		`data: {"id":"c1","model":"deepseek-chat","choices":[{"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"lo"}}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	var sb strings.Builder
	if err := streamChatToResponses(in, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{"response.created", "response.output_text.delta", `"delta":"Hel"`, `"delta":"lo"`, "response.output_item.done", "response.completed", `"input_tokens":7`, `"output_tokens":2`} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q\n%s", want, out)
		}
	}
}

func TestStreamMessagesToResponses(t *testing.T) {
	in := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m1","model":"claude-sonnet-4-6","usage":{"input_tokens":11,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a.txt\"}"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	var sb strings.Builder
	if err := StreamMessagesToResponses(strings.NewReader(in), &sb, nil); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{"response.created", `"delta":"Hi"`, "response.function_call_arguments.delta", "response.completed", `"input_tokens":11`, `"output_tokens":9`, "read_file"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q\n%s", want, out)
		}
	}
}

func TestPatchModelKeepsOtherFields(t *testing.T) {
	out, err := PatchModel([]byte(`{"model":"A/b","input":"x","stream":true}`), "real-model")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	if m["model"] != "real-model" || m["input"] != "x" || m["stream"] != true {
		t.Fatalf("patch 失败: %v", m)
	}
}

// ---- 并行工具调用 / 工具结果配对 ----

type parsedChatMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
	ToolCallID       string `json:"tool_call_id"`
	ToolCalls        []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

func parseChatMessages(t *testing.T, body []byte) []parsedChatMessage {
	t.Helper()
	var req struct {
		Messages []parsedChatMessage `json:"messages"`
		Tools    []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return req.Messages
}

// 两个并行 function_call 必须合并为一条 assistant 消息，且紧跟两条 tool 消息。
func TestResponsesToChatParallelToolCalls(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"input": [
			{"type": "message", "role": "user", "content": "go"},
			{"type": "function_call", "name": "exec_command", "arguments": "{\"cmd\":\"a\"}", "call_id": "call_1"},
			{"type": "reasoning", "id": "rs_1"},
			{"type": "function_call", "name": "exec_command", "arguments": "{\"cmd\":\"b\"}", "call_id": "call_2"},
			{"type": "function_call_output", "call_id": "call_1", "output": "out-1"},
			{"type": "function_call_output", "call_id": "call_2", "output": "out-2"}
		]
	}`)
	out, err := toChatRequest(body, "m")
	if err != nil {
		t.Fatal(err)
	}
	msgs := parseChatMessages(t, out)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 2 {
		t.Fatalf("并行调用未合并到同一条 assistant 消息: %+v", msgs[1])
	}
	if msgs[1].ToolCalls[0].ID != "call_1" || msgs[1].ToolCalls[1].ID != "call_2" {
		t.Fatalf("tool_calls 顺序错误: %+v", msgs[1].ToolCalls)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" || msgs[2].Content != "out-1" {
		t.Fatalf("tool 消息未紧跟: %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "call_2" || msgs[3].Content != "out-2" {
		t.Fatalf("tool 消息未紧跟: %+v", msgs[3])
	}
}

// 顺序错乱（结果在另一段落）时也要把结果并回 assistant 消息之后，保证合法。
func TestResponsesToChatOutputFoundAcrossMessages(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"input": [
			{"type": "function_call", "name": "f", "arguments": "{}", "call_id": "call_1"},
			{"type": "message", "role": "assistant", "content": "思考中"},
			{"type": "function_call_output", "call_id": "call_1", "output": "ok"}
		]
	}`)
	out, err := toChatRequest(body, "m")
	if err != nil {
		t.Fatal(err)
	}
	msgs := parseChatMessages(t, out)
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "assistant" || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("assistant 消息错误: %+v", msgs[0])
	}
	if msgs[1].Role != "tool" || msgs[1].ToolCallID != "call_1" || msgs[1].Content != "ok" {
		t.Fatalf("tool 消息未紧跟 assistant: %+v", msgs[1])
	}
}

// 孤立工具结果应被丢弃并告警，不能让上游报错。
func TestResponsesToChatDropsOrphanToolOutput(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"input": [
			{"type": "message", "role": "user", "content": "hi"},
			{"type": "function_call_output", "call_id": "call_ghost", "output": "ghost"}
		]
	}`)
	relay, out, err := Prepare(Responses, Chat, body, Identity{ProviderID: testProvider, ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	warnings := relay.warnings
	msgs := parseChatMessages(t, out)
	for _, m := range msgs {
		if m.Role == "tool" {
			t.Fatalf("孤立工具结果未被丢弃: %+v", msgs)
		}
	}
	if len(warnings) == 0 {
		t.Fatal("丢弃孤立结果应产生告警")
	}
}

// 缺少结果的工具调用要补占位，避免上游 400。
func TestResponsesToChatSynthesizesMissingOutput(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"input": [
			{"type": "function_call", "name": "f", "arguments": "{}", "call_id": "call_1"}
		]
	}`)
	out, err := toChatRequest(body, "m")
	if err != nil {
		t.Fatal(err)
	}
	msgs := parseChatMessages(t, out)
	if len(msgs) != 2 || msgs[1].Role != "tool" || msgs[1].ToolCallID != "call_1" || msgs[1].Content == "" {
		t.Fatalf("未补占位结果: %+v", msgs)
	}
}

// ---- 自定义（自由文本）工具 ----

func TestResponsesToChatCustomTools(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"tools": [
			{"type": "custom", "name": "apply_patch", "description": "patch files"},
			{"type": "function", "name": "exec_command", "parameters": {"type": "object"}}
		],
		"input": [
			{"type": "custom_tool_call", "name": "apply_patch", "call_id": "call_p", "input": "*** Begin Patch\n*** End Patch"},
			{"type": "custom_tool_call_output", "call_id": "call_p", "output": "Done!"}
		]
	}`)
	out, err := toChatRequest(body, "m")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		Messages []parsedChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 2 {
		t.Fatalf("自定义工具定义被丢弃: %+v", req.Tools)
	}
	if !strings.Contains(string(req.Tools[0].Function.Parameters), `"input"`) {
		t.Fatalf("自定义工具未降级为 input 字符串参数: %s", req.Tools[0].Function.Parameters)
	}
	if len(req.Messages) != 2 || len(req.Messages[0].ToolCalls) != 1 {
		t.Fatalf("自定义工具调用未进入历史: %+v", req.Messages)
	}
	if req.Messages[0].ToolCalls[0].Function.Arguments != `{"input":"*** Begin Patch\n*** End Patch"}` {
		t.Fatalf("自由文本未包装为函数参数: %s", req.Messages[0].ToolCalls[0].Function.Arguments)
	}
	if req.Messages[1].Role != "tool" || req.Messages[1].ToolCallID != "call_p" {
		t.Fatalf("自定义工具结果未进入历史: %+v", req.Messages[1])
	}
}

func TestChatToResponsesCustomToolCallAndReasoning(t *testing.T) {
	body := []byte(`{
		"id": "c1", "model": "m",
		"choices": [{"message": {"role": "assistant", "reasoning_content": "想一想", "tool_calls": [
			{"id": "call_p", "type": "function", "function": {"name": "apply_patch", "arguments": "{\"input\":\"*** Begin Patch\"}"}},
			{"id": "call_f", "type": "function", "function": {"name": "exec_command", "arguments": "{}"}}
		]}, "finish_reason": "tool_calls"}]
	}`)
	// 自定义工具名来自客户端请求（Responses 入口），由 Relay 带到响应侧。
	req := []byte(`{"model":"m","input":"hi","tools":[{"type":"custom","name":"apply_patch","description":"patch"}]}`)
	relay, _, err := Prepare(Responses, Chat, req, Identity{ProviderID: "custom-tool-sync", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := deliver(t, relay, string(body), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Output []struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Input   string `json:"input"`
			CallID  string `json:"call_id"`
			Summary []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	// 思考内容按标准路径下发为 reasoning 条目（排在正文与工具调用之前）
	if len(resp.Output) != 3 {
		t.Fatalf("output 数量错误: %+v", resp.Output)
	}
	if resp.Output[0].Type != "reasoning" || len(resp.Output[0].Summary) != 1 || resp.Output[0].Summary[0].Text != "想一想" {
		t.Fatalf("思考内容未按 reasoning 条目下发: %+v", resp.Output[0])
	}
	if resp.Output[1].Type != "custom_tool_call" || resp.Output[1].Input != "*** Begin Patch" {
		t.Fatalf("自定义工具调用未按 custom_tool_call 下发: %+v", resp.Output[1])
	}
	if resp.Output[2].Type != "function_call" {
		t.Fatalf("普通函数调用被误判: %+v", resp.Output[2])
	}
	// 思考内容按「供应商 + 模型 + 调用 ID」落缓存，供下一轮回填
	for _, id := range []string{"call_p", "call_f"} {
		if v, ok := reasoningGet("custom-tool-sync", "m", id); !ok || v != "想一想" {
			t.Fatalf("思考内容未入缓存 %s: %q %v", id, v, ok)
		}
	}
}

func TestStreamChatToResponsesReasoningAndCustomTool(t *testing.T) {
	in := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"delta":{"role":"assistant","reasoning_content":"推理"}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_p","type":"function","function":{"name":"apply_patch"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"input\":\"PATCH\"}"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	req := []byte(`{"model":"m","input":"hi","stream":true,"tools":[{"type":"custom","name":"apply_patch","description":"patch"}]}`)
	relay, _, err := Prepare(Responses, Chat, req, Identity{ProviderID: "custom-tool-stream", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := streamDeliver(t, relay, in, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{"custom_tool_call", `"input":"PATCH"`, "response.completed", "call_p"} {
		if !strings.Contains(out, want) {
			t.Fatalf("流式输出缺少 %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "response.function_call_arguments.delta") {
		t.Fatalf("自定义工具不应产生 function_call 参数事件\n%s", out)
	}
	if v, ok := reasoningGet("custom-tool-stream", "m", "call_p"); !ok || v != "推理" {
		t.Fatalf("思考内容未入缓存: %q %v", v, ok)
	}
}

func TestResponsesToChatReasoningEcho(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"input": [
			{"type": "function_call", "name": "f", "arguments": "{}", "call_id": "call_1"},
			{"type": "function_call_output", "call_id": "call_1", "output": "ok"}
		]
	}`)
	// 转换只负责取出真实思考内容（键：供应商 + 模型 + 调用 ID）；
	// 是否写入上游请求体由兼容扩展决定，见 compat.BeforeRequest。
	reasoningStore("echo-hit", "m", []string{"call_1"}, "deepseek thinking")
	relay, out, err := Prepare(Responses, Chat, body, Identity{ProviderID: "echo-hit", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := relay.LookupReasoning("call_1"); !ok || text != "deepseek thinking" {
		t.Fatalf("缓存命中未暴露真实思考内容: %q %v", text, ok)
	}

	// 转换本身不再往请求体写 reasoning_content（那是供应商策略）
	if strings.Contains(string(out), "reasoning_content") {
		t.Fatalf("转换层不应写入 reasoning_content: %s", out)
	}

	// 冷缓存：查不到，由调用方（兼容扩展）决定兜底
	cold, _, err := Prepare(Responses, Chat, body, Identity{ProviderID: "echo-cold", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cold.LookupReasoning("call_1"); ok {
		t.Fatal("冷缓存不应返回思考内容")
	}
}

// 标准路径：客户端回传 reasoning 条目时，转换层把推理文本关联到对应工具调用，
// 不依赖进程内缓存，也不需要占位文本。
func TestResponsesToChatUsesEchoedReasoning(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"input": [
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "上一轮推理"}]},
			{"type": "function_call", "name": "f", "arguments": "{}", "call_id": "call_echo"},
			{"type": "function_call_output", "call_id": "call_echo", "output": "ok"}
		]
	}`)
	relay, _, err := Prepare(Responses, Chat, body, Identity{ProviderID: "echo-std", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := relay.LookupReasoning("call_echo"); !ok || text != "上一轮推理" {
		t.Fatalf("未关联客户端回传的推理内容: %q %v", text, ok)
	}
}

// 流式：思考内容按标准事件下发为 reasoning 条目，供客户端下一轮原样回传。
func TestStreamChatToResponsesEmitsReasoningItem(t *testing.T) {
	in := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"delta":{"role":"assistant","reasoning_content":"先想一想"}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_r","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	relay, _, err := Prepare(Responses, Chat, []byte(`{"model":"m","input":"hi","stream":true}`), Identity{ProviderID: "rs-item-stream", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := streamDeliver(t, relay, in, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		`"type":"reasoning"`,
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"先想一想",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("流式输出缺少 %q\n%s", want, out)
		}
	}
}

func TestCanConvertMatrix(t *testing.T) {
	all := []Protocol{Chat, Responses, Messages}
	for _, entry := range all {
		for _, up := range all {
			got := CanConvert(entry, up)
			if entry == up && !got {
				t.Fatalf("同协议直通应为 true: %s", entry)
			}
			if entry == Messages && up != Messages && got {
				t.Fatalf("Messages 入口暂无反向转换: %s → %s", entry, up)
			}
		}
	}
}

// failAfterReader 先返回数据、随后返回错误，用于模拟上游流中断。
type failAfterReader struct {
	data []byte
	err  error
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// 流被中断时也要把已拿到的思考内容交给调用方，缓存能救下一轮请求。
func TestStreamChatToResponsesCapturesReasoningOnAbortedStream(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"delta":{"role":"assistant","reasoning_content":"推理"}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"f"}}]}}]}`,
		``,
	}, "\n")
	relay, _, perr := Prepare(Responses, Chat, []byte(`{"model":"m","input":"hi","stream":true}`), Identity{ProviderID: "abort-test", ModelID: "m"})
	if perr != nil {
		t.Fatal(perr)
	}
	rec := httptest.NewRecorder()
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	if _, err := relay.Deliver(200, h, &failAfterReader{data: []byte(stream), err: io.ErrUnexpectedEOF}, rec); err == nil {
		t.Fatal("期望返回读取错误")
	}
	if v, ok := reasoningGet("abort-test", "m", "call_x"); !ok || v != "推理" {
		t.Fatalf("流中断也应捕获思考内容: %q %v", v, ok)
	}
}

// 部分网关用 reasoning 字段名（OpenRouter 风格）返回推理内容。
func TestStreamChatToResponsesReasoningAlias(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"delta":{"role":"assistant","reasoning":"R"}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_y","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	relay, _, err := Prepare(Responses, Chat, []byte(`{"model":"m","input":"hi","stream":true}`), Identity{ProviderID: "alias-stream", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := streamDeliver(t, relay, stream, &sb); err != nil {
		t.Fatal(err)
	}
	if v, ok := reasoningGet("alias-stream", "m", "call_y"); !ok || v != "R" {
		t.Fatalf("reasoning 别名未解析: %q %v", v, ok)
	}
}

// 非流式响应的 reasoning 别名同样要能捕获。
func TestChatToResponsesReasoningAlias(t *testing.T) {
	body := []byte(`{
		"id":"c1","model":"m",
		"choices":[{"message":{"role":"assistant","reasoning":"R","tool_calls":[
			{"id":"call_z","type":"function","function":{"name":"f","arguments":"{}"}}
		]},"finish_reason":"tool_calls"}]
	}`)
	relay, _, err := Prepare(Responses, Chat, []byte(`{"model":"m","input":"hi"}`), Identity{ProviderID: "alias-sync", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deliver(t, relay, string(body), "application/json"); err != nil {
		t.Fatal(err)
	}
	if v, ok := reasoningGet("alias-sync", "m", "call_z"); !ok || v != "R" {
		t.Fatalf("非流式 reasoning 别名未解析: %q %v", v, ok)
	}
}

// ---- 图片输入：三种协议互转不得丢图 ----

const testImageURI = "data:image/png;base64,iVBORw0KGgo="

// imageRequest 是带文本与图片的 Responses 入口请求。
func imageRequest() []byte {
	return []byte(`{
		"model": "m",
		"input": [{"type": "message", "role": "user", "content": [
			{"type": "input_text", "text": "看这张图"},
			{"type": "input_image", "image_url": "` + testImageURI + `", "detail": "high"}
		]}]
	}`)
}

func TestResponsesToChatKeepsImages(t *testing.T) {
	out, err := toChatRequest(imageRequest(), "m")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL    string `json:"url"`
					Detail string `json:"detail"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("want 1 message, got %d: %s", len(req.Messages), out)
	}
	parts := req.Messages[0].Content
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text != "看这张图" {
		t.Fatalf("文本块丢失: %s", out)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL.URL != testImageURI || parts[1].ImageURL.Detail != "high" {
		t.Fatalf("图片块丢失或变形: %s", out)
	}
}

func TestResponsesToMessagesKeepsImages(t *testing.T) {
	out, err := ResponsesToMessagesRequest(imageRequest(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("消息块数量不对: %s", out)
	}
	img := req.Messages[0].Content[1]
	if img.Type != "image" || img.Source.Type != "base64" || img.Source.MediaType != "image/png" {
		t.Fatalf("data URI 未转成 base64 图片块: %s", out)
	}
}

// Chat 入口（含图片数组）→ Responses 上游必须保留 input_image。
func TestChatToResponsesKeepsImages(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"看图"},
		{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}
	]}]}`)
	_, out, err := Prepare(Chat, Responses, body, Identity{ProviderID: testProvider, ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Input []struct {
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Input) != 1 || len(req.Input[0].Content) != 2 {
		t.Fatalf("content 块数量不对: %s", out)
	}
	img := req.Input[0].Content[1]
	if img.Type != "input_image" || img.ImageURL != "https://example.com/a.png" {
		t.Fatalf("图片未转成 input_image: %s", out)
	}
}

// Chat 入口 → Messages 上游：远程地址按 URL 图片块下发。
func TestChatToMessagesKeepsImages(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"看图"},
		{"type":"image_url","image_url":"https://example.com/a.png"}
	]}]}`)
	out, err := ChatToMessagesRequest(body, "claude")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Source struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("content 块数量不对: %s", out)
	}
	img := req.Messages[0].Content[1]
	if img.Type != "image" || img.Source.Type != "url" || img.Source.URL != "https://example.com/a.png" {
		t.Fatalf("远程图片未转成 URL 图片块: %s", out)
	}
}

// 只有图片没有文本时，content 里不应再补空文本块。
func TestChatImageOnlyMessageKeepsTextlessContent(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":[
		{"type":"input_image","image_url":"` + testImageURI + `"}
	]}]}`)
	out, err := toChatRequest(body, "m")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Type != "image_url" {
		t.Fatalf("纯图片消息内容块不对: %s", out)
	}
}
