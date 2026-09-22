// mock-upstream 是用于本地联调的模拟上游（OpenAI 兼容 + Responses）。
// 用法：go run ./testdata/mock-upstream -addr 127.0.0.1:9911
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9911", "监听地址")
	requireKey := flag.String("key", "sk-test", "要求 Authorization: Bearer <key>，空表示不校验")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r, *requireKey) {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"object": "list", "data": []map[string]any{
			{"id": "deepseek-chat", "object": "model", "display_name": "DeepSeek Chat"},
			{"id": "deepseek-reasoner", "object": "model", "display_name": "DeepSeek Reasoner"},
			{"id": "glm-5.3", "object": "model", "display_name": "GLM-5.3"},
			{"id": "qwen3.8-max", "object": "model", "display_name": "Qwen3.8 Max"},
			{"id": "grok-4.7", "object": "model", "display_name": "Grok 4.7"},
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r, *requireKey) {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		json.Unmarshal(body, &req)
		log.Printf("chat model=%s stream=%v", req.Model, req.Stream)
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			chunks := []string{
				`data: {"id":"mock-1","model":"` + req.Model + `","choices":[{"delta":{"role":"assistant"}}]}`,
				`data: {"choices":[{"delta":{"content":"你好，"}}]}`,
				`data: {"choices":[{"delta":{"content":"来自模拟上游"}}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":6}}`,
				`data: [DONE]`,
			}
			for _, c := range chunks {
				fmt.Fprintf(w, "%s\n\n", c)
				if fl != nil {
					fl.Flush()
				}
				time.Sleep(30 * time.Millisecond)
			}
			return
		}
		writeJSON(w, map[string]any{
			"id": "mock-1", "object": "chat.completion", "model": req.Model,
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "来自模拟上游"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 12, "completion_tokens": 6, "total_tokens": 18},
		})
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r, *requireKey) {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"id": "resp-mock", "object": "response", "status": "completed", "model": "mock",
			"output": []map[string]any{{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "来自模拟上游"}}}},
			"usage":  map[string]any{"input_tokens": 12, "output_tokens": 6, "total_tokens": 18},
		})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })

	log.Printf("mock upstream listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func authOK(r *http.Request, key string) bool {
	if key == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+key || strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
