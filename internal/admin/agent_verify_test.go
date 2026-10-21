package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"llm-switch/internal/codex"
	"llm-switch/internal/config"
	"llm-switch/internal/dsh"
	"llm-switch/internal/opencode"
	"llm-switch/internal/provider"
	"llm-switch/internal/proxy"
	"llm-switch/internal/trae"
	"llm-switch/internal/workbuddy"
)

func TestAgentVerifyThroughLocalProxy(t *testing.T) {
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("WORKBUDDY_CONFIG_DIR", t.TempDir())

	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-verify","object":"chat.completion","created":1,"model":"verify-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer upstream.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Settings.Proxy.Host = "127.0.0.1"
		c.Settings.Proxy.Port = port
		if _, err := c.AddProvider(config.ProviderDraft{
			ID: "test", Name: "Test", BaseURL: upstream.URL,
			Protocols: []string{"chat"}, Auth: config.AuthDraft{Type: "bearer", APIKey: "sk-test"},
		}); err != nil {
			return err
		}
		if _, err := c.AddModel("test", config.ModelDraft{ID: "verify-model"}); err != nil {
			return err
		}
		c.Settings.DefaultModel = "test/verify-model"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	cfg := store.Snapshot()
	index := &proxy.Holder{}
	index.Store(proxy.BuildIndex(&cfg))
	px := proxy.NewServer(store, index)
	if err := px.Start(); err != nil {
		t.Fatal(err)
	}
	defer px.Stop(context.Background())
	defer px.Close()

	svc := New(store, provider.NewService(store, nil), px,
		codex.NewService(store), opencode.NewService(store), trae.NewService(store), dsh.NewService(store),
		workbuddy.NewService(store), fstest.MapFS{}, t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.registerAPI(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/codex/test", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d: %s", w.Code, w.Body.Bytes())
	}
	var res AgentVerification
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Status != http.StatusOK {
		t.Fatalf("verify failed: %+v", res)
	}
	if res.Entry != "responses" || res.Model != "test/verify-model" {
		t.Fatalf("unexpected verification: %+v", res)
	}
	if gotModel != "verify-model" {
		t.Fatalf("upstream model = %q", gotModel)
	}
	if !strings.Contains(res.URL, "/v1/responses") {
		t.Fatalf("url = %q", res.URL)
	}
	if got := svc.verificationSnapshot()["codex"]; !got.OK {
		t.Fatalf("verification not stored: %+v", got)
	}
}

func TestAgentVerifyUnknownAgent(t *testing.T) {
	mux, _ := newTestServer(t)
	code, body := do(t, mux, http.MethodPost, "/api/agents/unknown/test", "")
	if code != http.StatusNotFound {
		t.Fatalf("unknown agent = %d: %s", code, body)
	}
}
