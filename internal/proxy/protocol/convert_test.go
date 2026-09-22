package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

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
	out, err := ResponsesToChatRequest(body, "deepseek-chat")
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
	out, err := ChatToResponsesResponse(body)
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
	if err := StreamChatToResponses(strings.NewReader(in), &sb, nil); err != nil {
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
	out, err := ResponsesToChatRequest(body, "m")
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
	out, err := ResponsesToChatRequest(body, "m")
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
	var warnings []string
	out, err := ResponsesToChatRequestWith(body, "m", ChatConvertOptions{OnWarning: func(s string) { warnings = append(warnings, s) }})
	if err != nil {
		t.Fatal(err)
	}
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
	out, err := ResponsesToChatRequest(body, "m")
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
	out, err := ResponsesToChatRequest(body, "m")
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
	var gotReasoning string
	var gotIDs []string
	out, err := ChatToResponsesResponseWith(body, ResponseConvertOptions{
		CustomTools: map[string]bool{"apply_patch": true},
		OnReasoning: func(reasoning string, callIDs []string) { gotReasoning, gotIDs = reasoning, callIDs },
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Output []struct {
			Type   string `json:"type"`
			Name   string `json:"name"`
			Input  string `json:"input"`
			CallID string `json:"call_id"`
		} `json:"output"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("output 数量错误: %+v", resp.Output)
	}
	if resp.Output[0].Type != "custom_tool_call" || resp.Output[0].Input != "*** Begin Patch" {
		t.Fatalf("自定义工具调用未按 custom_tool_call 下发: %+v", resp.Output[0])
	}
	if resp.Output[1].Type != "function_call" {
		t.Fatalf("普通函数调用被误判: %+v", resp.Output[1])
	}
	if gotReasoning != "想一想" || len(gotIDs) != 2 {
		t.Fatalf("思考内容未回调: %q %v", gotReasoning, gotIDs)
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
	var sb strings.Builder
	var gotReasoning string
	var gotIDs []string
	err := StreamChatToResponsesWith(strings.NewReader(in), &sb, nil, ResponseConvertOptions{
		CustomTools: map[string]bool{"apply_patch": true},
		OnReasoning: func(reasoning string, callIDs []string) { gotReasoning, gotIDs = reasoning, callIDs },
	})
	if err != nil {
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
	if gotReasoning != "推理" || len(gotIDs) != 1 || gotIDs[0] != "call_p" {
		t.Fatalf("思考内容未回调: %q %v", gotReasoning, gotIDs)
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
	out, err := ResponsesToChatRequestWith(body, "m", ChatConvertOptions{
		Reasoning: func(callID string) (string, bool) {
			if callID == "call_1" {
				return "deepseek thinking", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := parseChatMessages(t, out)
	if msgs[0].ReasoningContent != "deepseek thinking" {
		t.Fatalf("reasoning_content 未回填: %+v", msgs[0])
	}

	// 空值也要写入（上游要求字段存在）
	out2, err := ResponsesToChatRequestWith(body, "m", ChatConvertOptions{
		Reasoning: func(string) (string, bool) { return "", true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out2), `"reasoning_content":""`) {
		t.Fatalf("空思考内容应显式写入字段: %s", out2)
	}

	// 未命中且上游不需要时不应写入该字段
	out3, _ := ResponsesToChatRequest(body, "m")
	if strings.Contains(string(out3), "reasoning_content") {
		t.Fatalf("不需要时不应写入 reasoning_content: %s", out3)
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
