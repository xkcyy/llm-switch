package protocol

import "testing"

func kinds(doc *TraceDocument) []string {
	out := make([]string, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		out = append(out, r.Kind)
	}
	return out
}

func equalKinds(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestTraceResponses(t *testing.T) {
	body := []byte(`{
	  "model": "ocg/deepseek-v4.1-flash",
	  "instructions": "You are Codex.",
	  "tools": [{"type":"function","name":"read","description":"Read a file","parameters":{"type":"object"}}],
	  "input": [
	    {"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]},
	    {"type":"reasoning","content":[{"type":"reasoning_text","text":"think first"}]},
	    {"type":"function_call","call_id":"c1","name":"read","arguments":"{\"path\":\"a.go\"}"},
	    {"type":"function_call_output","call_id":"c1","output":"package main"},
	    {"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}
	  ]
	}`)
	doc, err := TraceRequest(Responses, body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.System != "You are Codex." {
		t.Fatalf("系统提示词未解析: %q", doc.System)
	}
	if len(doc.Tools) != 1 || doc.Tools[0].Name != "read" {
		t.Fatalf("工具目录未解析: %+v", doc.Tools)
	}
	want := []string{traceSystem, traceUser, traceReasoning, traceToolCall, traceToolResult, traceAssistant}
	if got := kinds(doc); !equalKinds(got, want...) {
		t.Fatalf("轨迹行类型不符: %v", got)
	}
	if doc.Rows[2].Text != "think first" {
		t.Fatalf("Responses 思考内容未解析: %+v", doc.Rows[2])
	}
	if doc.Rows[3].Name != "read" || doc.Rows[3].CallID != "c1" {
		t.Fatalf("工具调用未解析: %+v", doc.Rows[3])
	}
	if doc.Rows[4].Output != "package main" {
		t.Fatalf("工具结果未解析: %+v", doc.Rows[4])
	}
}

func TestTraceResponsesMergesSystemAndContext(t *testing.T) {
	body := []byte(`{
	  "instructions":"system prompt",
	  "input":[
	    {"type":"message","role":"developer","content":"environment context"},
	    {"type":"message","role":"user","content":"hi"}
	  ]
	}`)
	doc, err := TraceRequest(Responses, body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.System != "system prompt\n\nenvironment context" {
		t.Fatalf("系统提示词与上下文未合并: %q", doc.System)
	}
	if !equalKinds(kinds(doc), traceSystem, traceUser) {
		t.Fatalf("上下文应并入系统提示词而非单独显示: %v", kinds(doc))
	}
	if doc.Rows[0].Text != doc.System {
		t.Fatalf("系统轨迹行未包含合并文本: %q", doc.Rows[0].Text)
	}
}

func TestTraceResponsesMovesSystemReminderIntoSystemPrompt(t *testing.T) {
	body := []byte(`{
	  "instructions":"base instructions",
	  "input":[
	    {"type":"message","role":"user","content":"Please inspect this.\n<system-reminder>Follow the repository instructions.</system-reminder>\nKeep the fix small."}
	  ]
	}`)
	doc, err := TraceRequest(Responses, body)
	if err != nil {
		t.Fatal(err)
	}
	wantSystem := "base instructions\n\n<system-reminder>Follow the repository instructions.</system-reminder>"
	if doc.System != wantSystem {
		t.Fatalf("system-reminder 未并入系统提示词: %q", doc.System)
	}
	if len(doc.Rows) != 2 || doc.Rows[1].Text != "Please inspect this.\n\nKeep the fix small." {
		t.Fatalf("原消息除 system-reminder 外应保留: %+v", doc.Rows)
	}
}

func TestTraceResponsesEncryptedReasoningRemainsBlank(t *testing.T) {
	body := []byte(`{"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`)
	doc, err := TraceRequest(Responses, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) != 1 || doc.Rows[0].Text != "" {
		t.Fatalf("不可读思考内容应保持空白: %+v", doc.Rows)
	}
}

func TestTraceChat(t *testing.T) {
	body := []byte(`{
	  "model": "deepseek-chat",
	  "messages": [
	    {"role":"system","content":"sys"},
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":"","tool_calls":[{"id":"t1","type":"function","function":{"name":"read","arguments":"{\"p\":1}"}}]},
	    {"role":"tool","tool_call_id":"t1","content":"data"}
	  ]
	}`)
	doc, err := TraceRequest(Chat, body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.System != "sys" {
		t.Fatalf("系统提示词未解析: %q", doc.System)
	}
	want := []string{traceSystem, traceUser, traceToolCall, traceToolResult}
	if got := kinds(doc); !equalKinds(got, want...) {
		t.Fatalf("轨迹行类型不符: %v", got)
	}
}

func TestTraceChatReasoning(t *testing.T) {
	body := []byte(`{
	  "messages":[
	    {"role":"assistant","reasoning_content":"thinking text","content":"answer"}
	  ]
	}`)
	doc, err := TraceRequest(Chat, body)
	if err != nil {
		t.Fatal(err)
	}
	if !equalKinds(kinds(doc), traceReasoning, traceAssistant) {
		t.Fatalf("Chat 思考应出现在助手正文前: %v", kinds(doc))
	}
	if doc.Rows[0].Text != "thinking text" {
		t.Fatalf("Chat 思考内容未解析: %+v", doc.Rows[0])
	}
}

func TestTraceMessages(t *testing.T) {
	body := []byte(`{
	  "model": "claude",
	  "system": "sys",
	  "tools": [{"name":"read","description":"d","input_schema":{"type":"object"}}],
	  "messages": [
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"ok"},{"type":"tool_use","id":"t1","name":"read","input":{"path":"a"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"data"}]}
	  ]
	}`)
	doc, err := TraceRequest(Messages, body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.System != "sys" {
		t.Fatalf("系统提示词未解析: %q", doc.System)
	}
	if len(doc.Tools) != 1 || doc.Tools[0].Name != "read" {
		t.Fatalf("工具目录未解析: %+v", doc.Tools)
	}
	want := []string{traceSystem, traceUser, traceReasoning, traceReasoning, traceAssistant, traceToolCall, traceToolResult}
	if got := kinds(doc); !equalKinds(got, want...) {
		t.Fatalf("轨迹行类型不符: %v", got)
	}
	if doc.Rows[2].Text != "hmm" || doc.Rows[3].Text != "" {
		t.Fatalf("思考文本解析/隐藏内容处理错误: %+v %+v", doc.Rows[2], doc.Rows[3])
	}
	if doc.Rows[5].CallID != "t1" {
		t.Fatalf("工具调用未解析: %+v", doc.Rows[5])
	}
}
