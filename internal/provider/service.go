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
	"llm-switch/internal/proxy/protocol"
	"llm-switch/internal/upstream"
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
	Protocol        string   `json:"protocol,omitempty"`     // 建议的上游协议（部分网关的模型分散在不同端点）
	Capabilities    []string `json:"capabilities,omitempty"` // 只含可确证的图片输入（vision）
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

// parseProtocol 把配置里的协议字符串转为协议值。
// UpstreamProtocols 已保证取值合法，这里的兜底只为保持历史行为（未知按 Chat 处理）。
func parseProtocol(s string) protocol.Protocol {
	if p, err := protocol.Parse(s); err == nil {
		return p
	}
	return protocol.Chat
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
func (s *Service) probeProtocol(ctx context.Context, p config.Provider, proto protocol.Protocol, model string) TestResult {
	body, err := minimalRequestBody(proto, model)
	if err != nil {
		return TestResult{ErrorType: "config", Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL(p.BaseURL, proto.UpstreamPath()), bytes.NewReader(body))
	if err != nil {
		return TestResult{ErrorType: "address", Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	upstream.SetAuth(req, p, proto)
	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		et, msg := classify(0, err)
		return TestResult{ErrorType: et, Message: msg, LatencyMS: time.Since(start).Milliseconds(), Protocol: string(proto)}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 400 {
		return TestResult{OK: true, LatencyMS: time.Since(start).Milliseconds(), Protocol: string(proto)}
	}
	et, msg := classify(resp.StatusCode, nil)
	if msg == "" {
		msg = strings.TrimSpace(string(b))
	}
	return TestResult{ErrorType: et, Message: msg, LatencyMS: time.Since(start).Milliseconds(), Protocol: string(proto)}
}

// probeAllProtocols 按配置顺序探测供应商声明的所有协议，返回第一个可用的。
func (s *Service) probeAllProtocols(ctx context.Context, p config.Provider, model string) TestResult {
	start := time.Now()
	failures := make([]string, 0, len(p.UpstreamProtocols()))
	var first TestResult
	for _, raw := range p.UpstreamProtocols() {
		res := s.probeProtocol(ctx, p, parseProtocol(raw), model)
		if res.OK {
			return res
		}
		if len(failures) == 0 {
			first = res
		}
		failures = append(failures, fmt.Sprintf("%s: %s", raw, res.Message))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL(p.BaseURL, "models"), nil)
	if err != nil {
		return TestResult{ErrorType: "address", Message: err.Error()}
	}
	upstream.SetAuth(req, p, parseProtocol(p.UpstreamProtocols()[0]))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL(p.BaseURL, "models"), nil)
	if err != nil {
		return nil, err
	}
	upstream.SetAuth(req, p, parseProtocol(p.UpstreamProtocols()[0]))
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
			ID      string   `json:"id"`
			Name    string   `json:"display_name"`
			RawName string   `json:"name"`
			Input   []string `json:"input_modalities"` // 部分上游（如 DeepSeek）在此自述输入模态
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
				// 只有命名空间可信时才用在线元数据判断能力：未知命名空间会全局匹配到
				// 别家供应商的同名模型，据此声明图片输入会误导 Codex。
				if s.meta.HasNamespace(p.Preset) {
					rm.Capabilities = capabilitiesFromModalities(meta.InputModalities)
				}
			}
		}
		// 上游自述优先于在线元数据
		if caps := capabilitiesFromModalities(m.Input); len(caps) > 0 {
			rm.Capabilities = caps
		}
		out = append(out, rm)
	}
	return out, nil
}

// capabilitiesFromModalities 把输入模态映射为模型能力，只认「图片输入」。
// 没有明确声明 image 时返回 nil，交由配置层保持「未知」。
func capabilitiesFromModalities(modalities []string) []string {
	for _, m := range modalities {
		if strings.EqualFold(strings.TrimSpace(m), "image") {
			return []string{"tools", "vision"}
		}
	}
	return nil
}

func minimalRequestBody(proto protocol.Protocol, model string) ([]byte, error) {
	switch proto {
	case protocol.Responses:
		return json.Marshal(map[string]any{
			"model":             model,
			"input":             "ping",
			"max_output_tokens": 16,
			"store":             false,
		})
	case protocol.Messages:
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
