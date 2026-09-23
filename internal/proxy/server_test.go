package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"llm-switch/internal/config"
)

func newTestProxy(t *testing.T, upstream string) *httptest.Server {
	return newTestProxyWith(t, upstream, nil)
}

func newTestProxyWith(t *testing.T, upstream string, modify func(*config.Config)) *httptest.Server {
	t.Helper()
	ts, _ := newTestProxyServer(t, upstream, modify)
	return ts
}

// newTestProxyServer 同时返回代理实例，便于断言记录（Record）。
func newTestProxyServer(t *testing.T, upstream string, modify func(*config.Config)) (*httptest.Server, *Server) {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Providers = []config.Provider{{
			ID: "deepseek", Name: "DeepSeek", BaseURL: upstream + "/v1", Protocol: "chat",
			Auth: config.Auth{Type: "bearer", APIKey: "sk-test"}, Enabled: true,
		}}
		c.Models = []config.Model{{ID: "deepseek-chat", ProviderID: "deepseek", Enabled: true}}
		if modify != nil {
			modify(c)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot()
	index := &Holder{}
	index.Store(BuildIndex(&cfg))
	srv := NewServer(store, index)
	ts := httptest.NewServer(srv.mux())
	t.Cleanup(ts.Close)
	return ts, srv
}

// lastRecord 返回最近一条代理记录。
func lastRecord(t *testing.T, srv *Server) Record {
	t.Helper()
	recs := srv.Recent(1)
	if len(recs) == 0 {
		t.Fatal("没有代理记录")
	}
	return recs[0]
}

func TestProxyModelProtocolOverride(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r1","object":"response","status":"completed","model":"m","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	// 供应商默认 chat，模型级覆盖为 responses（如 OpenCode Go 的多端点模型）
	ts := newTestProxyWith(t, upstream.URL, func(c *config.Config) {
		c.Models[0].Protocol = "responses"
	})
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"deepseek/deepseek-chat","input":"ping","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if gotPath != "/v1/responses" {
		t.Fatalf("模型协议覆盖未生效，上游路径 = %s", gotPath)
	}
}

func TestProxyForwardsSessionAndUserAgent(t *testing.T) {
	var gotSession, gotUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("x-opencode-session")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"deepseek-chat","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	ts := newTestProxy(t, upstream.URL)
	body := `{"model":"deepseek/deepseek-chat","input":"hello world","stream":false}`

	// 客户端带会话头：必须原样透传
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("session_id", "sess-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotSession != "sess-123" {
		t.Fatalf("会话头未透传: %q", gotSession)
	}
	if !strings.HasPrefix(gotUA, "llm-switch") {
		t.Fatalf("User-Agent 未设置: %q", gotUA)
	}

	// 客户端没有会话头：生成稳定兜底值（同一会话两次请求应一致）
	resp2, _ := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	resp2.Body.Close()
	first := gotSession
	if !strings.HasPrefix(first, "ls-") {
		t.Fatalf("兜底会话 ID 格式不对: %q", first)
	}
	resp3, _ := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	resp3.Body.Close()
	if gotSession != first {
		t.Fatalf("兜底会话 ID 不稳定: %q -> %q", first, gotSession)
	}
}

func TestProxyResponsesToChatNonStream(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"deepseek-chat","choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	ts := newTestProxy(t, upstream.URL)
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"deepseek/deepseek-chat","input":"ping","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("上游路径 = %s", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("上游认证 = %q", gotAuth)
	}
	if gotModel != "deepseek-chat" {
		t.Fatalf("上游 model = %q", gotModel)
	}
	var out struct {
		Object string `json:"object"`
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "response" || len(out.Output) == 0 || out.Output[0].Content[0].Text != "pong" {
		t.Fatalf("响应转换错误: %+v", out)
	}
}

func TestProxyResponsesToChatStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		chunks := []string{
			`data: {"id":"c1","model":"deepseek-chat","choices":[{"delta":{"role":"assistant"}}]}`,
			`data: {"choices":[{"delta":{"content":"po"}}]}`,
			`data: {"choices":[{"delta":{"content":"ng"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			io.WriteString(w, c+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer upstream.Close()

	ts := newTestProxy(t, upstream.URL)
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"DeepSeek/deepseek-chat","input":"ping","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "event-stream") {
		t.Fatalf("content-type = %s", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	for _, want := range []string{"response.created", `"delta":"po"`, `"delta":"ng"`, "response.completed", `"input_tokens":4`} {
		if !strings.Contains(out, want) {
			t.Fatalf("流式输出缺少 %q\n%s", want, out)
		}
	}
}

func TestProxyUpstreamErrorConversion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"bad key","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	ts := newTestProxy(t, upstream.URL)
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"DeepSeek/deepseek-chat","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if !strings.Contains(out.Error.Message, "bad key") {
		t.Fatalf("错误信息未透传: %+v", out)
	}
}

func TestProxyMappingErrors(t *testing.T) {
	ts := newTestProxy(t, "http://127.0.0.1:1")

	// 供应商不存在 → 400
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"Nope/x","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("供应商不存在 status = %d, want 400", resp.StatusCode)
	}

	// 模型不存在 → 404
	resp2, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"DeepSeek/not-exist","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("模型不存在 status = %d, want 404", resp2.StatusCode)
	}
}

func TestProxyModelsEndpoint(t *testing.T) {
	ts := newTestProxy(t, "http://127.0.0.1:1")
	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Data) != 1 || out.Data[0].ID != "deepseek/deepseek-chat" {
		t.Fatalf("模型清单错误: %+v", out)
	}
}

// 供应商同时声明多种协议时：入口协议命中即直通，未命中才转换。
func TestProxyPrefersNativeProtocol(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.Write([]byte(`{"id":"r1","object":"response","status":"completed","model":"m","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
			return
		}
		w.Write([]byte(`{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	ts := newTestProxyWith(t, upstream.URL, func(c *config.Config) {
		c.Providers[0].Protocol = "chat"
		c.Providers[0].Protocols = []string{"chat", "responses"}
	})

	// Responses 入口：供应商原生支持 → 直通 /v1/responses（不做转换）
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"deepseek/deepseek-chat","input":"ping","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotPath != "/v1/responses" {
		t.Fatalf("原生协议未优先直通，上游路径 = %s", gotPath)
	}

	// Chat 入口：同样直通 /v1/chat/completions
	resp2, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"ping"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("chat 入口未直通，上游路径 = %s", gotPath)
	}
}

// 无法转换的入口协议要给出明确错误。
func TestProxyIncompatibleProtocol(t *testing.T) {
	ts := newTestProxy(t, "http://127.0.0.1:1")
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"ping"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "协议不兼容") {
		t.Fatalf("错误信息不明确: %s", b)
	}
}

// 思考型上游：第一轮缓存 reasoning_content，第二轮带工具结果时原样回传。
func TestProxyReasoningEcho(t *testing.T) {
	var lastBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","reasoning_content":"思考过程","tool_calls":[{"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	ts := newTestProxyWith(t, upstream.URL, func(c *config.Config) {
		c.Providers[0].Preset = "custom" // 冷缓存时不做兜底，先验证缓存回填
	})

	// 第一轮：入口 Responses，上游返回带 reasoning_content 的工具调用
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"deepseek/deepseek-chat","input":"跑一下","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 第二轮：携带工具结果，代理应把 reasoning_content 回填到 assistant 消息
	body := `{"model":"deepseek/deepseek-chat","stream":false,"input":[
		{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"call_1"},
		{"type":"function_call_output","call_id":"call_1","output":"done"}
	]}`
	resp2, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	var sent struct {
		Messages []struct {
			Role             string `json:"role"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []any  `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(lastBody), &sent); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range sent.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			found = true
			if m.ReasoningContent != "思考过程" {
				t.Fatalf("reasoning_content 未回传: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("未找到 assistant 工具调用消息: %s", lastBody)
	}
}

// 冷缓存 + 已知思考型供应商：必须写入非空 reasoning_content（部分网关把空串视为未回传）。
func TestProxyReasoningColdCacheFallback(t *testing.T) {
	var lastBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	ts := newTestProxyWith(t, upstream.URL, func(c *config.Config) {
		c.Providers[0].Preset = "deepseek"
	})
	body := `{"model":"deepseek/deepseek-chat","stream":false,"input":[
		{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"call_x"},
		{"type":"function_call_output","call_id":"call_x","output":"done"}
	]}`
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var sent struct {
		Messages []struct {
			Role             string `json:"role"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(lastBody), &sent); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range sent.Messages {
		if m.Role != "assistant" {
			continue
		}
		found = true
		if strings.TrimSpace(m.ReasoningContent) == "" {
			t.Fatalf("冷缓存必须回填非空占位: %s", lastBody)
		}
	}
	if !found {
		t.Fatalf("未找到 assistant 消息: %s", lastBody)
	}
}

// 非思考型供应商不应被塞入 reasoning_content（避免未知字段被严格网关拒绝）。
func TestProxyReasoningSkippedForPlainProvider(t *testing.T) {
	var lastBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	ts := newTestProxyWith(t, upstream.URL, func(c *config.Config) {
		c.Providers[0].Preset = "custom"
		c.Providers[0].BaseURL = upstream.URL + "/v1"
	})
	body := `{"model":"deepseek/deepseek-chat","stream":false,"input":[
		{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"call_x"},
		{"type":"function_call_output","call_id":"call_x","output":"done"}
	]}`
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.Contains(lastBody, "reasoning_content") {
		t.Fatalf("普通供应商不应写入 reasoning_content: %s", lastBody)
	}
}

// 上游 4xx：记录里要有错误体摘要与 request id，便于定位。
func TestProxyLogsUpstreamErrorDetail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-123")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	ts, srv := newTestProxyServer(t, upstream.URL, nil)
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"deepseek/deepseek-chat","input":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	rec := lastRecord(t, srv)
	if !strings.Contains(rec.Error, "reasoning_content") || !strings.Contains(rec.Error, "req-123") {
		t.Fatalf("错误摘要不完整: %q", rec.Error)
	}
	if rec.Session == "" {
		t.Fatalf("记录缺少会话标识: %+v", rec)
	}
}
