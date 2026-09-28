package compat

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"llm-switch/internal/proxy/protocol"
)

func TestBuiltinMatch(t *testing.T) {
	cases := []struct {
		name string
		up   Upstream
		want []string
	}{
		{"deepseek 预置", Upstream{Preset: "deepseek", BaseURL: "https://api.deepseek.com"}, []string{"reasoning-echo"}},
		{"opencode-go 预置", Upstream{Preset: "opencode-go", BaseURL: "https://opencode.ai/zen/go/v1"}, []string{"reasoning-echo", "opencode-go"}},
		{"地址含 opencode", Upstream{BaseURL: "https://opencode.ai/zen/v1"}, []string{"reasoning-echo", "opencode-go"}},
		{"地址含 deepseek", Upstream{BaseURL: "https://proxy.example.com/deepseek/v1"}, []string{"reasoning-echo"}},
		{"普通网关", Upstream{BaseURL: "https://api.example.com/v1"}, nil},
	}
	for _, tc := range cases {
		got := sortedJoin(New(tc.up, nil).Active())
		if got != sortedJoin(tc.want) {
			t.Fatalf("%s: 命中扩展 %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// sortedJoin 让比较与注册顺序（文件名顺序）无关。
func sortedJoin(items []string) string {
	out := append([]string(nil), items...)
	sort.Strings(out)
	return strings.Join(out, ",")
}

// 自动匹配判不出来的供应商（自建 DeepSeek 兼容网关）用显式挂载兜底。
func TestExplicitMount(t *testing.T) {
	up := Upstream{ProviderID: "my-gw", BaseURL: "https://llm.internal/v1"}
	if len(New(up, nil).Active()) != 0 {
		t.Fatal("普通网关不应自动命中任何扩展")
	}
	plan := New(up, []string{"reasoning-echo"})
	if names := plan.Active(); len(names) != 1 || names[0] != "reasoning-echo" {
		t.Fatalf("显式挂载未生效: %v", names)
	}
}

func TestUnknownNameReported(t *testing.T) {
	plan := New(Upstream{BaseURL: "https://api.example.com"}, []string{"", "  ", "no-such-extension"})
	if got := plan.Unknown(); len(got) != 1 || got[0] != "no-such-extension" {
		t.Fatalf("未知扩展名未如实上报: %v", got)
	}
	if len(plan.Active()) != 0 {
		t.Fatalf("未知扩展名不应产生效果: %v", plan.Active())
	}
}

// ---- 时机点：扩展自己决定做什么 ----

func TestApplyRunsBeforeRequestHooks(t *testing.T) {
	calls := []string{}
	first := &fakeExtension{name: "first", hook: func(*Request) error {
		calls = append(calls, "first")
		return nil
	}}
	second := &fakeExtension{name: "second", hook: func(r *Request) error {
		calls = append(calls, "second")
		r.HTTP.Header.Set("x-second", "1")
		r.Body = []byte(`{"patched":true}`)
		return nil
	}}
	// 不实现 BeforeRequest 的扩展不应被执行，也不应报错
	inert := &fakeExtension{name: "inert"}

	plan := Plan{exts: []Extension{first, second, inert}}
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	r := &Request{HTTP: req, Body: []byte("{}")}
	if err := plan.Apply(r); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "first,second" {
		t.Fatalf("时机点执行顺序错误: %v", calls)
	}
	if got := req.Header.Get("x-second"); got != "1" {
		t.Fatalf("扩展未改请求头: %q", got)
	}
	if string(r.Body) != `{"patched":true}` {
		t.Fatalf("扩展未改请求体: %s", r.Body)
	}
}

// ---- 扩展实例：reasoning-echo ----

func TestReasoningEchoFillsField(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"ok"}
	]}`)
	cases := []struct {
		name     string
		lookup   func(string) (string, bool)
		wantText string
	}{
		{"真实思考内容", func(id string) (string, bool) { return "真实推理", id == "call_1" }, "真实推理"},
		{"冷缓存回填占位", func(string) (string, bool) { return "", false }, "(reasoning omitted)"},
	}
	for _, tc := range cases {
		var warned []string
		r := &Request{
			UpstreamProtocol: protocol.Chat, Body: append([]byte(nil), body...),
			LookupReasoning: tc.lookup,
			Warn:            func(f string, a ...any) { warned = append(warned, f) },
		}
		if err := (reasoningEcho{}).BeforeRequest(r); err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Messages []struct {
				Role             string `json:"role"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []any  `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(r.Body, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Messages[0].ReasoningContent != "" {
			t.Fatalf("%s：普通消息不应被写入字段", tc.name)
		}
		if doc.Messages[1].ReasoningContent != tc.wantText {
			t.Fatalf("%s：字段值错误: %q", tc.name, doc.Messages[1].ReasoningContent)
		}
		if len(doc.Messages[1].ToolCalls) != 1 {
			t.Fatalf("%s：工具调用被破坏", tc.name)
		}
		// 其余字段必须保留
		if !strings.Contains(string(r.Body), `"model":"m"`) {
			t.Fatalf("%s：请求体其他字段丢失: %s", tc.name, r.Body)
		}
		if (len(warned) > 0) != (tc.wantText == "(reasoning omitted)") {
			t.Fatalf("%s：占位告警不符合预期: %v", tc.name, warned)
		}
	}
}

// 已有的真实内容不应被覆盖。
func TestReasoningEchoKeepsExistingValue(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","reasoning_content":"已有","tool_calls":[{"id":"c","type":"function"}]}]}`)
	r := &Request{UpstreamProtocol: protocol.Chat, Body: body, LookupReasoning: func(string) (string, bool) { return "新值", true }}
	if err := (reasoningEcho{}).BeforeRequest(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), `"reasoning_content":"已有"`) {
		t.Fatalf("不应覆盖已有字段: %s", r.Body)
	}
}

// 非 Chat 上游体（例如直通 Responses）不得被改写。
func TestReasoningEchoSkipsNonChatUpstream(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","call_id":"c"}]}`)
	r := &Request{UpstreamProtocol: protocol.Responses, Body: body}
	if err := (reasoningEcho{}).BeforeRequest(r); err != nil {
		t.Fatal(err)
	}
	if string(r.Body) != string(body) {
		t.Fatalf("非 Chat 上游体被改写: %s", r.Body)
	}
}

// ---- 扩展实例：opencode-go ----

func TestOpenCodeGoSetsSessionHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://opencode.ai/zen/go/v1/chat/completions", nil)
	r := &Request{HTTP: req, Session: "sess-1"}
	if err := (opencodeGo{}).BeforeRequest(r); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get(SessionHeaderDefault); got != "sess-1" {
		t.Fatalf("会话标识头未写入: %q", got)
	}
	// 没有会话标识时不写头
	req2, _ := http.NewRequest(http.MethodPost, "https://opencode.ai/zen/go/v1/chat/completions", nil)
	if err := (opencodeGo{}).BeforeRequest(&Request{HTTP: req2}); err != nil {
		t.Fatal(err)
	}
	if got := req2.Header.Get(SessionHeaderDefault); got != "" {
		t.Fatalf("无会话标识时不应写头: %q", got)
	}
}

func TestNamesEnumeratesBuiltins(t *testing.T) {
	for _, want := range []string{"reasoning-echo", "opencode-go"} {
		if _, ok := ByName(want); !ok {
			t.Fatalf("内置扩展 %q 未注册", want)
		}
	}
	if len(Names()) < 2 {
		t.Fatalf("内置目录不完整: %v", Names())
	}
}

// fakeExtension 按需实现时机点接口，用于验证机制侧只按接口查找。
type fakeExtension struct {
	name string
	hook func(*Request) error
}

func (f *fakeExtension) Name() string        { return f.name }
func (f *fakeExtension) Match(Upstream) bool { return false }

func (f *fakeExtension) BeforeRequest(r *Request) error {
	if f.hook == nil {
		return nil
	}
	return f.hook(r)
}
