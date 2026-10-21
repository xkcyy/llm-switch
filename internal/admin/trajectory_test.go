package admin

import (
	"net/http"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"llm-switch/internal/codex"
	"llm-switch/internal/config"
	"llm-switch/internal/dsh"
	"llm-switch/internal/opencode"
	"llm-switch/internal/provider"
	"llm-switch/internal/proxy"
	"llm-switch/internal/proxy/protocol"
	"llm-switch/internal/trace"
	"llm-switch/internal/trae"
	"llm-switch/internal/workbuddy"
)

func newTrajectoryServer(t *testing.T) (*http.ServeMux, *trace.Store) {
	t.Helper()
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("WORKBUDDY_CONFIG_DIR", t.TempDir())
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	index := &proxy.Holder{}
	index.Store(proxy.BuildIndex(&config.Config{}))
	px := proxy.NewServer(store, index)
	svc := New(store, provider.NewService(store, nil), px,
		codex.NewService(store), opencode.NewService(store), trae.NewService(store), dsh.NewService(store),
		workbuddy.NewService(store),
		fstest.MapFS{}, t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.registerAPI(mux)
	t.Cleanup(func() { _ = px.Usage().Close() })
	return mux, px.Traces()
}

func TestTrajectoryListDetailRaw(t *testing.T) {
	mux, traces := newTrajectoryServer(t)
	body := []byte(`{"model":"deepseek/deepseek-chat","instructions":"sys","input":[{"type":"message","role":"user","content":"hi"}]}`)
	id := traces.Add(trace.Entry{
		Time: time.Now(), Protocol: "responses", Source: "Codex", Session: "sess-1",
		RequestModel: "deepseek/deepseek-chat", Provider: "DeepSeek", Model: "deepseek-chat", Body: body,
		Method: "POST", URL: "http://127.0.0.1:8317/v1/responses", Proto: "HTTP/1.1",
		Headers: []trace.Header{{Name: "Authorization", Value: "***"}},
	})
	traces.SetResponse(id, trace.Response{
		Status: 200, Proto: "HTTP/1.1", Bytes: 7,
		Headers: []trace.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"ok":1}`),
	})
	traces.Complete(id, 200, 12)

	code, raw := do(t, mux, http.MethodGet, "/api/trajectory", "")
	if code != 200 {
		t.Fatalf("列表失败: %d %s", code, raw)
	}
	list := decodeInto[struct {
		Capacity int           `json:"capacity"`
		Requests []trace.Entry `json:"requests"`
	}](t, raw)
	if list.Capacity != trace.Capacity || len(list.Requests) != 1 || list.Requests[0].ID != id {
		t.Fatalf("列表内容不对: %+v", list)
	}
	if list.Requests[0].Status != 200 || list.Requests[0].DurationMS != 12 {
		t.Fatalf("列表未回填状态: %+v", list.Requests[0])
	}

	code, raw = do(t, mux, http.MethodGet, "/api/trajectory/1", "")
	if code != 200 {
		t.Fatalf("详情失败: %d %s", code, raw)
	}
	detail := decodeInto[struct {
		Entry    trace.Entry            `json:"entry"`
		Document protocol.TraceDocument `json:"document"`
	}](t, raw)
	if detail.Document.System != "sys" || len(detail.Document.Rows) != 2 {
		t.Fatalf("轨迹文档不对: system=%q rows=%d", detail.Document.System, len(detail.Document.Rows))
	}
	if detail.Document.Rows[0].Kind != "system" || detail.Document.Rows[1].Kind != "user" {
		t.Fatalf("轨迹行类型不对: %+v", detail.Document.Rows)
	}

	code, raw = do(t, mux, http.MethodGet, "/api/trajectory/1/raw", "")
	if code != 200 {
		t.Fatalf("原始请求失败: %d %s", code, raw)
	}
	rawResp := decodeInto[struct {
		Entry    trace.Entry    `json:"entry"`
		Headers  []trace.Header `json:"headers"`
		Body     string         `json:"body"`
		Response *struct {
			Status  int            `json:"status"`
			Proto   string         `json:"proto"`
			Headers []trace.Header `json:"headers"`
			Body    string         `json:"body"`
			Bytes   int            `json:"bytes"`
		} `json:"response"`
	}](t, raw)
	if rawResp.Body != string(body) {
		t.Fatalf("原始请求体不一致: %q", rawResp.Body)
	}
	if rawResp.Entry.Method != "POST" || rawResp.Entry.URL == "" {
		t.Fatalf("原始请求缺少方法或地址: %+v", rawResp.Entry)
	}
	if len(rawResp.Headers) != 1 || rawResp.Headers[0].Name != "Authorization" {
		t.Fatalf("原始请求缺少请求头: %+v", rawResp.Headers)
	}
	if rawResp.Response == nil {
		t.Fatal("原始响应缺失")
	}
	if rawResp.Response.Status != 200 || rawResp.Response.Proto != "HTTP/1.1" || rawResp.Response.Body != `{"ok":1}` {
		t.Fatalf("原始响应内容不对: %+v", rawResp.Response)
	}
	if len(rawResp.Response.Headers) != 1 || rawResp.Response.Headers[0].Name != "Content-Type" {
		t.Fatalf("原始响应缺少响应头: %+v", rawResp.Response.Headers)
	}

	if code, _ := do(t, mux, http.MethodGet, "/api/trajectory/999", ""); code != http.StatusNotFound {
		t.Fatalf("不存在的记录应返回 404，实际 %d", code)
	}
}
