package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llm-switch/internal/buildinfo"
	"llm-switch/internal/config"
	"llm-switch/internal/metadata"
)

type Service struct {
	store  *config.Store
	client *http.Client
	meta   *metadata.Client
}

type TestResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	ErrorType string `json:"error_type,omitempty"`
	Message   string `json:"message,omitempty"`
	// Protocol 是实际可用的协议（多协议供应商会按配置顺序探测）。
	Protocol string `json:"protocol,omitempty"`
}

type RemoteModel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name,omitempty"`
	ContextWindow   *int     `json:"context_window,omitempty"`
	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
	Levels          []string `json:"levels,omitempty"`
	Description     string   `json:"description,omitempty"`
	Protocol        string   `json:"protocol,omitempty"` // 建议的上游协议（部分网关的模型分散在不同端点）
}

// suggestProtocol 针对多端点网关给出模型级协议建议。
// OpenCode Go 的模型分布在 chat / messages / responses 三种端点上（见官方文档 Endpoints 一节）。
func suggestProtocol(preset, modelID string) string {
	if preset != "opencode-go" {
		return ""
	}
	id := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(id, "minimax"), strings.HasPrefix(id, "qwen3"):
		return "messages"
	case strings.HasPrefix(id, "grok"), strings.HasPrefix(id, "gpt-5.6-luna"), strings.HasPrefix(id, "muse-spark"):
		return "responses"
	default:
		return "" // 其余模型走供应商默认（chat）
	}
}

func NewService(store *config.Store, meta *metadata.Client) *Service {
	return &Service{
		store:  store,
		client: &http.Client{Timeout: 30 * time.Second},
		meta:   meta,
	}
}

func joinURL(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

func applyAuth(req *http.Request, p config.Provider, proto string) {
	switch p.Auth.Type {
	case "api_key_header", "custom":
		h := p.Auth.Header
		if h == "" {
			h = "x-api-key"
		}
		if p.Auth.APIKey != "" {
			req.Header.Set(h, p.Auth.APIKey)
		}
	case "bearer", "":
		if p.Auth.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.Auth.APIKey)
		}
	}
	if proto == "messages" {
		if req.Header.Get("anthropic-version") == "" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	}
}

// protocolPath 返回协议对应的上游路径（相对 base_url）。
func protocolPath(proto string) string {
	switch proto {
	case "responses":
		return "responses"
	case "messages":
		return "messages"
	default:
		return "chat/completions"
	}
}

func classify(status int, err error) (string, string) {
	switch {
	case err != nil:
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return "network", "请求超时：" + err.Error()
		}
		return "network", "网络错误：" + err.Error()
	case status == 401 || status == 403:
		return "auth", "认证失败（HTTP " + fmt.Sprint(status) + "），请检查 API Key"
	case status == 429:
		return "rate_limit", "请求被限流（HTTP 429）"
	case status == 404:
		return "address", "地址或路径不存在（HTTP 404），请检查接口地址"
	case status >= 500:
		return "network", "上游服务异常（HTTP " + fmt.Sprint(status) + "）"
	case status >= 400:
		return "protocol", "请求被拒绝（HTTP " + fmt.Sprint(status) + "），可能是协议或参数不匹配"
	}
	return "", ""
}

// probeProtocol 用最小请求探测某个协议是否可用。
func (s *Service) probeProtocol(ctx context.Context, p config.Provider, proto, model string) TestResult {
	body, err := minimalRequestBody(proto, model)
	if err != nil {
		return TestResult{ErrorType: "config", Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(p.BaseURL, protocolPath(proto)), bytes.NewReader(body))
	if err != nil {
		return TestResult{ErrorType: "address", Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	applyAuth(req, p, proto)
	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		et, msg := classify(0, err)
		return TestResult{ErrorType: et, Message: msg, LatencyMS: time.Since(start).Milliseconds(), Protocol: proto}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 400 {
		return TestResult{OK: true, LatencyMS: time.Since(start).Milliseconds(), Protocol: proto}
	}
	et, msg := classify(resp.StatusCode, nil)
	if msg == "" {
		msg = strings.TrimSpace(string(b))
	}
	return TestResult{ErrorType: et, Message: msg, LatencyMS: time.Since(start).Milliseconds(), Protocol: proto}
}

// probeAllProtocols 按配置顺序探测供应商声明的所有协议，返回第一个可用的。
func (s *Service) probeAllProtocols(ctx context.Context, p config.Provider, model string) TestResult {
	start := time.Now()
	failures := make([]string, 0, len(p.UpstreamProtocols()))
	var first TestResult
	for _, proto := range p.UpstreamProtocols() {
		res := s.probeProtocol(ctx, p, proto, model)
		if res.OK {
			return res
		}
		if len(failures) == 0 {
			first = res
		}
		failures = append(failures, fmt.Sprintf("%s: %s", proto, res.Message))
	}
	if first.ErrorType == "" && len(failures) == 0 {
		return TestResult{ErrorType: "config", Message: "未配置任何协议"}
	}
	first.Message = "所有协议均不可用（" + strings.Join(failures, "；") + "）"
	first.LatencyMS = time.Since(start).Milliseconds()
	return first
}

// Test 执行供应商连接测试：按配置顺序探测各协议，返回第一个可用的协议。
func (s *Service) Test(ctx context.Context, providerID string) TestResult {
	cfg := s.store.Snapshot()
	p, ok := cfg.FindProvider(providerID)
	if !ok {
		return TestResult{ErrorType: "config", Message: "供应商不存在"}
	}
	start := time.Now()

	models := cfg.ModelsOf(p.ID)
	if len(models) > 0 {
		return s.probeAllProtocols(ctx, p, models[0].ID)
	}

	// 尚无模型：用 /models 做连通性探测（无法判定具体协议）
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(p.BaseURL, "models"), nil)
	if err != nil {
		return TestResult{ErrorType: "address", Message: err.Error()}
	}
	applyAuth(req, p, p.UpstreamProtocols()[0])
	resp, err := s.client.Do(req)
	if err != nil {
		et, msg := classify(0, err)
		return TestResult{ErrorType: et, Message: msg, LatencyMS: time.Since(start).Milliseconds()}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 400 {
		return TestResult{OK: true, LatencyMS: time.Since(start).Milliseconds(), Protocol: p.UpstreamProtocols()[0]}
	}
	if resp.StatusCode == 404 || resp.StatusCode == 405 {
		et, msg := classify(http.StatusNotFound, nil)
		return TestResult{ErrorType: et, Message: "无法通过 /models 探测，请先添加模型后再测试：" + msg}
	}
	et, msg := classify(resp.StatusCode, nil)
	return TestResult{ErrorType: et, Message: msg, LatencyMS: time.Since(start).Milliseconds()}
}

// TestModel 执行模型调用测试（最小请求），多协议供应商按顺序探测。
func (s *Service) TestModel(ctx context.Context, providerID, modelID string) TestResult {
	cfg := s.store.Snapshot()
	p, ok := cfg.FindProvider(providerID)
	if !ok {
		return TestResult{ErrorType: "config", Message: "供应商不存在"}
	}
	m, ok := cfg.FindModel(providerID, modelID)
	if !ok {
		return TestResult{ErrorType: "config", Message: "模型不存在"}
	}
	return s.probeAllProtocols(ctx, p, m.ID)
}

// FetchModels 从上游拉取模型列表。
func (s *Service) FetchModels(ctx context.Context, providerID string) ([]RemoteModel, error) {
	cfg := s.store.Snapshot()
	p, ok := cfg.FindProvider(providerID)
	if !ok {
		return nil, fmt.Errorf("供应商不存在")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(p.BaseURL, "models"), nil)
	if err != nil {
		return nil, err
	}
	applyAuth(req, p, p.UpstreamProtocols()[0])
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		et, msg := classify(resp.StatusCode, nil)
		return nil, fmt.Errorf("%s: %s", et, msg)
	}
	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"display_name"`
			RawName string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	out := make([]RemoteModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.RawName
		}
		rm := RemoteModel{ID: m.ID, Name: name, Protocol: suggestProtocol(p.Preset, m.ID)}
		if s.meta != nil {
			if meta := s.meta.Lookup(p.Preset, m.ID); meta != nil {
				rm.ContextWindow = meta.ContextWindow
				rm.MaxOutputTokens = meta.OutputTokens
				rm.Levels = meta.Levels
				if rm.Description == "" {
					rm.Description = meta.Description
				}
			}
		}
		out = append(out, rm)
	}
	return out, nil
}

func minimalRequestBody(protocol, model string) ([]byte, error) {
	switch protocol {
	case "responses":
		return json.Marshal(map[string]any{
			"model":             model,
			"input":             "ping",
			"max_output_tokens": 16,
			"store":             false,
		})
	case "messages":
		return json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 1,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
		})
	default:
		return json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 1,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
		})
	}
}
