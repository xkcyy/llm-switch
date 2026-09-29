package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-switch/internal/config"
)

const (
	chatUsageBody      = `{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`
	responsesUsageBody = `{"id":"r1","object":"response","status":"completed","model":"m","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}`
	messagesUsageBody  = `{"id":"m1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`
)

const (
	chatEntryBody      = `{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}],"stream":false}`
	responsesEntryBody = `{"model":"deepseek/deepseek-chat","input":"hi","stream":false}`
	messagesEntryBody  = `{"model":"deepseek/deepseek-chat","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
)

func usageRange() (time.Time, time.Time) {
	now := time.Now()
	return now.Add(-time.Hour), now.Add(time.Hour)
}

// startUsageProxy 起一个只会返回固定响应体的上游，并把代理的供应商协议设为 protocols。
func startUsageProxy(t *testing.T, protocols []string, upstreamBody string) (*httptest.Server, *Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(upstream.Close)
	return newTestProxyServer(t, upstream.URL, func(c *config.Config) {
		c.Providers[0].Protocols = protocols
		c.Providers[0].Protocol = protocols[0]
	})
}

func postEntry(t *testing.T, ts *httptest.Server, path, body string) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// TestUsageCollectedNonStream 覆盖三协议 × 直通/转换的非流式用量采集。
func TestUsageCollectedNonStream(t *testing.T) {
	cases := []struct {
		name         string
		entryPath    string
		entryBody    string
		protocols    []string
		upstreamBody string
		input        int
		output       int
		total        int
		cached       int
		reasoning    int
	}{
		{"chat 直通", "/v1/chat/completions", chatEntryBody, []string{"chat"}, chatUsageBody, 10, 5, 15, 3, 2},
		{"responses 直通", "/v1/responses", responsesEntryBody, []string{"responses"}, responsesUsageBody, 10, 5, 15, 3, 2},
		{"messages 直通", "/v1/messages", messagesEntryBody, []string{"messages"}, messagesUsageBody, 10, 5, 15, 0, 0},
		{"responses → chat 转换", "/v1/responses", responsesEntryBody, []string{"chat"}, chatUsageBody, 10, 5, 15, 3, 2},
		{"chat → responses 转换", "/v1/chat/completions", chatEntryBody, []string{"responses"}, responsesUsageBody, 10, 5, 15, 3, 2},
		{"responses → messages 转换", "/v1/responses", responsesEntryBody, []string{"messages"}, messagesUsageBody, 10, 5, 15, 0, 0},
		{"responses 上游回 chat 形状", "/v1/responses", responsesEntryBody, []string{"responses"},
			`{"id":"r1","usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`, 7, 2, 9, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, srv := startUsageProxy(t, tc.protocols, tc.upstreamBody)
			postEntry(t, ts, tc.entryPath, tc.entryBody)

			from, to := usageRange()
			tot := srv.Usage().Totals(from, to)
			if tot.Requests != 1 || tot.OK != 1 || tot.Err != 0 {
				t.Fatalf("requests/ok/err = %d/%d/%d", tot.Requests, tot.OK, tot.Err)
			}
			if tot.Input != tc.input || tot.Output != tc.output || tot.Total != tc.total {
				t.Fatalf("tokens = in %d out %d total %d，期望 in %d out %d total %d",
					tot.Input, tot.Output, tot.Total, tc.input, tc.output, tc.total)
			}
			if tot.Cached != tc.cached || tot.Reasoning != tc.reasoning {
				t.Fatalf("cached/reasoning = %d/%d，期望 %d/%d", tot.Cached, tot.Reasoning, tc.cached, tc.reasoning)
			}
			if tot.MissingUsage != 0 {
				t.Fatalf("missing_usage = %d，期望 0", tot.MissingUsage)
			}
		})
	}
}

// TestUsageMissingCounted 上游不回 usage 时记 missing_usage，token 记 0。
func TestUsageMissingCounted(t *testing.T) {
	ts, srv := startUsageProxy(t, []string{"chat"},
		`{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	postEntry(t, ts, "/v1/chat/completions", chatEntryBody)

	from, to := usageRange()
	tot := srv.Usage().Totals(from, to)
	if tot.Requests != 1 || tot.OK != 1 || tot.Err != 0 {
		t.Fatalf("requests/ok/err = %d/%d/%d", tot.Requests, tot.OK, tot.Err)
	}
	if tot.MissingUsage != 1 {
		t.Fatalf("missing_usage = %d，期望 1", tot.MissingUsage)
	}
	if tot.Input != 0 || tot.Output != 0 || tot.Total != 0 {
		t.Fatalf("缺用量时 token 应为 0，实际 %d/%d/%d", tot.Input, tot.Output, tot.Total)
	}
}

// TestUsageUpstreamErrorCountsErr 上游失败计入 err，不算 missing_usage。
func TestUsageUpstreamErrorCountsErr(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer upstream.Close()
	ts, srv := newTestProxyServer(t, upstream.URL, nil)
	postEntry(t, ts, "/v1/chat/completions", chatEntryBody)

	from, to := usageRange()
	tot := srv.Usage().Totals(from, to)
	if tot.Requests != 1 || tot.Err != 1 || tot.OK != 0 {
		t.Fatalf("requests/ok/err = %d/%d/%d", tot.Requests, tot.OK, tot.Err)
	}
	if tot.MissingUsage != 0 {
		t.Fatalf("失败调用不应计入 missing_usage，实际 %d", tot.MissingUsage)
	}
}
