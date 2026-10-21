package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"llm-switch/internal/dsh"
	"llm-switch/internal/proxy/protocol"
	"llm-switch/internal/workbuddy"
)

// AgentVerification 是一次「经本机代理调用模型」的链路验证结果。
// 它验证 Agent 使用的入口协议、本机代理、协议转换与上游模型，而不只是直连上游。
type AgentVerification struct {
	OK         bool      `json:"ok"`
	Agent      string    `json:"agent"`
	Entry      string    `json:"entry"`
	Model      string    `json:"model"`
	URL        string    `json:"url"`
	Status     int       `json:"status"`
	LatencyMS  int64     `json:"latency_ms"`
	Message    string    `json:"message"`
	CheckedAt  time.Time `json:"checked_at"`
	Configured bool      `json:"configured"`
	ConfigPath string    `json:"config_path,omitempty"`
}

type agentVerifySpec struct {
	label      string
	entry      protocol.Protocol
	path       string
	userAgent  string
	configured func(*Server) bool
	configPath func(*Server) string
}

var agentVerifySpecs = map[string]agentVerifySpec{
	"codex": {
		label: "Codex", entry: protocol.Responses, path: "/v1/responses", userAgent: "codex llm-switch-verify",
		configured: func(s *Server) bool { return s.codex.Connected() },
		configPath: func(s *Server) string { return s.codex.Paths().ConfigTOML },
	},
	"opencode": {
		label: "OpenCode", entry: protocol.Chat, path: "/v1/chat/completions", userAgent: "opencode llm-switch-verify",
		configured: func(s *Server) bool { return s.opencode.Connected() },
		configPath: func(s *Server) string { return s.opencode.Paths().Config },
	},
	"trae": {
		label: "Trae SOLO", entry: protocol.Chat, path: "/v1/chat/completions", userAgent: "trae llm-switch-verify",
		configured: func(s *Server) bool { return s.trae.Status(context.Background()).Ready },
		configPath: func(s *Server) string { return s.trae.Paths().ArgvJSON },
	},
	"dsh": {
		label: "DeepSeek Harness", entry: protocol.Chat, path: "/v1/chat/completions", userAgent: "dsh llm-switch-verify",
		configured: func(s *Server) bool {
			return s.dsh.Connected(s.store.Snapshot().Settings.DshProfile)
		},
		configPath: func(s *Server) string {
			return dsh.ResolvePaths(s.store.Snapshot().Settings.DshProfile).Patch
		},
	},
	"workbuddy": {
		label: "WorkBuddy", entry: protocol.Chat, path: "/v1/chat/completions", userAgent: "workbuddy llm-switch-verify",
		configured: func(s *Server) bool { return s.workbuddy.Connected() },
		configPath: func(*Server) string { return workbuddy.ModelsPath() },
	},
}

func (s *Server) handleAgentTest(w http.ResponseWriter, r *http.Request) {
	agent := strings.ToLower(strings.TrimSpace(r.PathValue("agent")))
	spec, ok := agentVerifySpecs[agent]
	if !ok {
		apiErr(w, http.StatusNotFound, "未知 Agent")
		return
	}
	res := s.verifyAgent(r.Context(), agent, spec)
	s.verifyMu.Lock()
	s.verifications[agent] = res
	s.verifyMu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) verifyAgent(ctx context.Context, agent string, spec agentVerifySpec) AgentVerification {
	start := time.Now()
	cfg := s.store.Snapshot()
	model := cfg.Settings.DefaultModel
	if _, ok := cfg.LookupAvailable(model); !ok {
		if list := cfg.AvailableModels(); len(list) > 0 {
			model = list[0].Slug
		}
	}
	res := AgentVerification{
		Agent:      agent,
		Entry:      string(spec.entry),
		Model:      model,
		CheckedAt:  start,
		Configured: spec.configured(s),
		ConfigPath: spec.configPath(s),
	}
	addr := net.JoinHostPort(cfg.Settings.Proxy.Host, fmt.Sprint(cfg.Settings.Proxy.Port))
	res.URL = "http://" + addr + spec.path

	if model == "" {
		res.Message = "没有可用模型，先在「供应商与模型」里添加并启用模型"
		res.LatencyMS = time.Since(start).Milliseconds()
		return res
	}
	if !s.proxy.Running() {
		res.Message = "本机代理未运行，先启动代理再验证"
		res.LatencyMS = time.Since(start).Milliseconds()
		return res
	}

	body, err := agentVerifyBody(spec.entry, model)
	if err != nil {
		res.Message = "构造验证请求失败：" + err.Error()
		res.LatencyMS = time.Since(start).Milliseconds()
		return res
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, res.URL, bytes.NewReader(body))
	if err != nil {
		res.Message = "构造验证请求失败：" + err.Error()
		res.LatencyMS = time.Since(start).Milliseconds()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer llm-switch-local")
	req.Header.Set("X-Api-Key", "llm-switch-local")
	req.Header.Set("User-Agent", spec.userAgent)
	req.Header.Set("X-LLM-Switch-Verify", agent)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = "本机代理请求失败：" + err.Error()
		return res
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	res.Status = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		res.OK = true
		res.Message = fmt.Sprintf("%s 入口协议经本机代理调用 %s 成功", spec.label, model)
		return res
	}
	if msg := agentVerifyError(raw); msg != "" {
		res.Message = fmt.Sprintf("HTTP %d：%s", resp.StatusCode, msg)
	} else {
		res.Message = fmt.Sprintf("本机代理返回 HTTP %d", resp.StatusCode)
	}
	return res
}

func agentVerifyBody(entry protocol.Protocol, model string) ([]byte, error) {
	switch entry {
	case protocol.Responses:
		return json.Marshal(map[string]any{
			"model":             model,
			"input":             "Reply with OK.",
			"max_output_tokens": 16,
			"store":             false,
		})
	case protocol.Messages:
		return json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 8,
			"messages": []map[string]any{{
				"role": "user", "content": "Reply with OK.",
			}},
		})
	default:
		return json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 16,
			"messages": []map[string]any{{
				"role": "user", "content": "Reply with OK.",
			}},
		})
	}
}

func agentVerifyError(body []byte) string {
	var payload struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && len(payload.Error) > 0 {
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(payload.Error, &nested) == nil && nested.Message != "" {
			return truncateVerifyMessage(nested.Message)
		}
		var text string
		if json.Unmarshal(payload.Error, &text) == nil && text != "" {
			return truncateVerifyMessage(text)
		}
	}
	var top struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &top) == nil && top.Message != "" {
		return truncateVerifyMessage(top.Message)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	if text == "" {
		return ""
	}
	return truncateVerifyMessage(text)
}

func truncateVerifyMessage(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		return text[:300] + "…"
	}
	return text
}

func (s *Server) clearAgentVerification(agent string) {
	s.verifyMu.Lock()
	delete(s.verifications, agent)
	s.verifyMu.Unlock()
}

func (s *Server) verificationSnapshot() map[string]AgentVerification {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	out := make(map[string]AgentVerification, len(s.verifications))
	for key, value := range s.verifications {
		out[key] = value
	}
	return out
}
