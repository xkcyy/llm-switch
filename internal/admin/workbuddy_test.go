package admin

import (
	"net/http"
	"testing"
)

func TestWorkBuddyEndpoints(t *testing.T) {
	mux, store := newTestServer(t)
	seedModel(t, store)

	code, body := do(t, mux, http.MethodPost, "/api/workbuddy/sync", "{}")
	if code != 200 {
		t.Fatalf("sync = %d: %s", code, body)
	}

	code, body = do(t, mux, http.MethodGet, "/api/workbuddy", "")
	if code != 200 {
		t.Fatalf("status = %d: %s", code, body)
	}
	st := decodeInto[map[string]any](t, body)
	if st["connected"] != true {
		t.Fatalf("connected = %v (%s)", st["connected"], body)
	}
	if models, _ := st["models"].([]any); len(models) != 1 {
		t.Fatalf("models = %v (%s)", st["models"], body)
	}

	code, body = do(t, mux, http.MethodPost, "/api/workbuddy/disconnect", "")
	if code != 200 {
		t.Fatalf("disconnect = %d: %s", code, body)
	}
	code, body = do(t, mux, http.MethodGet, "/api/workbuddy", "")
	st = decodeInto[map[string]any](t, body)
	if code != 200 || st["connected"] != false {
		t.Fatalf("after disconnect = %d %v (%s)", code, st["connected"], body)
	}
}
