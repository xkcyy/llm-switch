package upstream

import (
	"net/http"
	"testing"

	"llm-switch/internal/config"
	"llm-switch/internal/proxy/protocol"
)

func newReq(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:8317", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// URL 的斜杠容忍行为（原先 provider 与 proxy 各写一份，这里锁定合并后的行为）。
func TestURL(t *testing.T) {
	cases := []struct{ base, suffix, want string }{
		{"https://api.deepseek.com", "chat/completions", "https://api.deepseek.com/chat/completions"},
		{"https://api.openai.com/v1/", "/responses", "https://api.openai.com/v1/responses"},
		{"https://gw.example.com/v1", "models", "https://gw.example.com/v1/models"},
	}
	for _, c := range cases {
		if got := URL(c.base, c.suffix); got != c.want {
			t.Fatalf("URL(%q, %q) = %q, want %q", c.base, c.suffix, got, c.want)
		}
	}
}

func TestSetAuthBearer(t *testing.T) {
	req := newReq(t)
	p := config.Provider{Auth: config.Auth{Type: "bearer", APIKey: "sk-1"}}
	SetAuth(req, p, protocol.Chat)
	if got := req.Header.Get("Authorization"); got != "Bearer sk-1" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("anthropic-version"); got != "" {
		t.Fatalf("非 messages 不应写 anthropic-version: %q", got)
	}
}

// 认证类型留空按 bearer 处理（历史行为）。
func TestSetAuthEmptyTypeUsesBearer(t *testing.T) {
	req := newReq(t)
	SetAuth(req, config.Provider{Auth: config.Auth{APIKey: "sk-2"}}, protocol.Chat)
	if got := req.Header.Get("Authorization"); got != "Bearer sk-2" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestSetAuthHeaderType(t *testing.T) {
	req := newReq(t)
	SetAuth(req, config.Provider{Auth: config.Auth{Type: "api_key_header", Header: "X-Api-Key", APIKey: "k"}}, protocol.Chat)
	if got := req.Header.Get("X-Api-Key"); got != "k" {
		t.Fatalf("X-Api-Key = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("不应写 Authorization: %q", got)
	}
}

// 头名留空时回退 x-api-key（历史行为）。
func TestSetAuthHeaderTypeDefaultsHeaderName(t *testing.T) {
	req := newReq(t)
	SetAuth(req, config.Provider{Auth: config.Auth{Type: "custom", APIKey: "k"}}, protocol.Chat)
	if got := req.Header.Get("x-api-key"); got != "k" {
		t.Fatalf("x-api-key = %q", got)
	}
}

func TestSetAuthMessagesAddsVersion(t *testing.T) {
	req := newReq(t)
	SetAuth(req, config.Provider{Auth: config.Auth{Type: "api_key_header", Header: "x-api-key", APIKey: "k"}}, protocol.Messages)
	if got := req.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version = %q", got)
	}

	// 调用方已显式设置时不得覆盖。
	req2 := newReq(t)
	req2.Header.Set("anthropic-version", "2024-01-01")
	SetAuth(req2, config.Provider{}, protocol.Messages)
	if got := req2.Header.Get("anthropic-version"); got != "2024-01-01" {
		t.Fatalf("已存在的 anthropic-version 被覆盖: %q", got)
	}
}

// 没有 API Key 时不写任何认证头（供应商可匿名访问的网关）。
func TestSetAuthWithoutKey(t *testing.T) {
	req := newReq(t)
	SetAuth(req, config.Provider{Auth: config.Auth{Type: "bearer"}}, protocol.Chat)
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("无 Key 不应写 Authorization: %q", got)
	}
}
