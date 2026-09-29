package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-switch/internal/config"
)

const (
	chatSSEWithUsage = "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hi\"},\"index\":0}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15,\"prompt_tokens_details\":{\"cached_tokens\":3},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n" +
		"data: [DONE]\n\n"
	chatSSEWithoutUsage = "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hi\"},\"index\":0}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"
	responsesSSEWithUsage = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"m\",\"status\":\"in_progress\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"model\":\"m\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15,\"input_tokens_details\":{\"cached_tokens\":3},\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n"
)

const (
	chatStreamEntryBody      = `{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}],"stream":true}`
	responsesStreamEntryBody = `{"model":"deepseek/deepseek-chat","input":"hi","stream":true}`
)

// startSSEProxy 起一个返回固定 SSE 的上游，并记录上游收到的请求体。
func startSSEProxy(t *testing.T, protocols []string, sse string) (*httptest.Server, *Server, *string) {
	t.Helper()
	var lastBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sse))
	}))
	t.Cleanup(upstream.Close)
	ts, srv := newTestProxyServer(t, upstream.URL, func(c *config.Config) {
		c.Providers[0].Protocols = protocols
		c.Providers[0].Protocol = protocols[0]
	})
	return ts, srv, &lastBody
}

// TestUsageStreamingCollected 覆盖流式直通与转换：用量从 SSE 事件里旁路采集。
func TestUsageStreamingCollected(t *testing.T) {
	cases := []struct {
		name      string
		entryPath string
		entryBody string
		protocols []string
		sse       string
	}{
		{"responses 直通", "/v1/responses", responsesStreamEntryBody, []string{"responses"}, responsesSSEWithUsage},
		{"chat 直通", "/v1/chat/completions", chatStreamEntryBody, []string{"chat"}, chatSSEWithUsage},
		{"responses → chat 转换", "/v1/responses", responsesStreamEntryBody, []string{"chat"}, chatSSEWithUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, srv, _ := startSSEProxy(t, tc.protocols, tc.sse)
			postEntry(t, ts, tc.entryPath, tc.entryBody)

			from, to := usageRange()
			tot := srv.Usage().Totals(from, to)
			if tot.Requests != 1 || tot.OK != 1 {
				t.Fatalf("requests/ok = %d/%d", tot.Requests, tot.OK)
			}
			if tot.Input != 10 || tot.Output != 5 || tot.Total != 15 {
				t.Fatalf("tokens = in %d out %d total %d", tot.Input, tot.Output, tot.Total)
			}
			if tot.Cached != 3 || tot.Reasoning != 2 {
				t.Fatalf("cached/reasoning = %d/%d", tot.Cached, tot.Reasoning)
			}
			if tot.MissingUsage != 0 {
				t.Fatalf("missing_usage = %d", tot.MissingUsage)
			}
		})
	}
}

// TestUsageChatStreamInjectsIncludeUsage chat 直通必须给上游补上 include_usage，否则拿不到用量。
func TestUsageChatStreamInjectsIncludeUsage(t *testing.T) {
	ts, _, body := startSSEProxy(t, []string{"chat"}, chatSSEWithUsage)
	postEntry(t, ts, "/v1/chat/completions", chatStreamEntryBody)
	if !strings.Contains(*body, `"include_usage":true`) {
		t.Fatalf("上游请求未注入 include_usage：%s", *body)
	}

	// 客户端已显式打开时不重复写入
	ts2, _, body2 := startSSEProxy(t, []string{"chat"}, chatSSEWithUsage)
	postEntry(t, ts2, "/v1/chat/completions",
		`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`)
	if n := strings.Count(*body2, "include_usage"); n != 1 {
		t.Fatalf("include_usage 出现 %d 次：%s", n, *body2)
	}
}

// TestUsageStreamingMissingCounted 流式没有 usage 时记 missing_usage。
func TestUsageStreamingMissingCounted(t *testing.T) {
	ts, srv, _ := startSSEProxy(t, []string{"chat"}, chatSSEWithoutUsage)
	postEntry(t, ts, "/v1/chat/completions", chatStreamEntryBody)

	from, to := usageRange()
	tot := srv.Usage().Totals(from, to)
	if tot.Requests != 1 || tot.OK != 1 || tot.MissingUsage != 1 {
		t.Fatalf("requests/ok/missing = %d/%d/%d", tot.Requests, tot.OK, tot.MissingUsage)
	}
	if tot.Total != 0 {
		t.Fatalf("缺用量时 token 应为 0，实际 %d", tot.Total)
	}
}
