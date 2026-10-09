package admin

import (
	"net/http"
	"testing"

	"llm-switch/internal/config"
)

func seedModel(t *testing.T, store *config.Store) {
	t.Helper()
	err := store.Update(func(c *config.Config) error {
		if _, err := c.AddProvider(config.ProviderDraft{
			ID: "ds", Name: "DeepSeek", BaseURL: "https://api.deepseek.com",
			Protocols: []string{"chat"}, Auth: config.AuthDraft{Type: "bearer", APIKey: "sk-x"},
		}); err != nil {
			return err
		}
		_, err := c.AddModel("ds", config.ModelDraft{ID: "deepseek-chat"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDshEndpoints(t *testing.T) {
	mux, store := newTestServer(t)
	seedModel(t, store)

	code, body := do(t, mux, http.MethodPost, "/api/dsh/sync", "{}")
	if code != 200 {
		t.Fatalf("sync = %d: %s", code, body)
	}

	code, body = do(t, mux, http.MethodGet, "/api/dsh", "")
	if code != 200 {
		t.Fatalf("status = %d: %s", code, body)
	}
	st := decodeInto[map[string]any](t, body)
	if st["connected"] != true {
		t.Fatalf("connected = %v (%s)", st["connected"], body)
	}

	code, body = do(t, mux, http.MethodPost, "/api/dsh/disconnect", "")
	if code != 200 {
		t.Fatalf("disconnect = %d: %s", code, body)
	}
	code, body = do(t, mux, http.MethodGet, "/api/dsh", "")
	st = decodeInto[map[string]any](t, body)
	if code != 200 || st["connected"] != false {
		t.Fatalf("after disconnect = %d %v (%s)", code, st["connected"], body)
	}
}