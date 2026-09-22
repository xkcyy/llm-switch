package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"llm-switch/internal/buildinfo"
	"llm-switch/internal/codex"
	"llm-switch/internal/config"
	"llm-switch/internal/opencode"
	"llm-switch/internal/provider"
	"llm-switch/internal/proxy"
	"llm-switch/internal/trae"
	"llm-switch/internal/winutil"
)

type Server struct {
	store     *config.Store
	providers *provider.Service
	proxy     *proxy.Server
	codex     *codex.Service
	opencode  *opencode.Service
	trae      *trae.Service
	token     string
	webFS     fs.FS

	onAutoStart func(bool) error
	logDir      string

	http    *http.Server
	ln      net.Listener
	running bool
}

func New(store *config.Store, prov *provider.Service, px *proxy.Server, cx *codex.Service, oc *opencode.Service, tr *trae.Service, webFS fs.FS, logDir string, onAutoStart func(bool) error) *Server {
	return &Server{
		store: store, providers: prov, proxy: px, codex: cx, opencode: oc, trae: tr,
		token: randomToken(), webFS: webFS, logDir: logDir, onAutoStart: onAutoStart,
	}
}

func (s *Server) Token() string { return s.token }

func (s *Server) Start() error {
	cfg := s.store.Snapshot()
	addr := net.JoinHostPort(cfg.Settings.Admin.Host, strconv.Itoa(cfg.Settings.Admin.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("管理端口 %s 无法监听：%w", addr, err)
	}
	mux := http.NewServeMux()
	s.registerAPI(mux)
	s.registerStatic(mux)
	srv := &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 20 * time.Second}
	s.http, s.ln, s.running = srv, ln, true
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("管理服务退出", "error", err)
		}
	}()
	slog.Info("管理界面已启动", "url", "http://"+addr)
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	err := s.http.Shutdown(ctx)
	s.http, s.running = nil, false
	return err
}

func (s *Server) URL() string {
	cfg := s.store.Snapshot()
	return fmt.Sprintf("http://%s:%d", cfg.Settings.Admin.Host, cfg.Settings.Admin.Port)
}

// guard 校验 Host（防 DNS Rebinding）与令牌。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		host = strings.Trim(host, "[]")
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("X-Admin-Token") != s.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌无效，请从托盘重新打开管理页"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerStatic(mux *http.ServeMux) {
	sub, err := fs.Sub(s.webFS, "dist")
	if err != nil {
		sub = s.webFS
	}
	files := http.FileServer(http.FS(sub))
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(strings.ReplaceAll(string(b), "__ADMIN_TOKEN__", s.token)))
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			serveIndex(w, r)
			return
		}
		if f, err := sub.Open(p); err == nil {
			f.Close()
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r) // 单页应用回退
	})
}

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/overview", s.handleOverview)

	mux.HandleFunc("GET /api/providers", s.handleProviderList)
	mux.HandleFunc("POST /api/providers", s.handleProviderCreate)
	mux.HandleFunc("GET /api/providers/presets", s.handlePresets)
	mux.HandleFunc("PUT /api/providers/{id}", s.handleProviderUpdate)
	mux.HandleFunc("DELETE /api/providers/{id}", s.handleProviderDelete)
	mux.HandleFunc("POST /api/providers/{id}/test", s.handleProviderTest)
	mux.HandleFunc("GET /api/providers/{id}/secret", s.handleProviderSecret)
	mux.HandleFunc("POST /api/providers/{id}/fetch-models", s.handleFetchModels)

	mux.HandleFunc("GET /api/providers/{providerID}/models", s.handleModelList)
	mux.HandleFunc("POST /api/providers/{providerID}/models", s.handleModelCreate)
	mux.HandleFunc("PUT /api/providers/{providerID}/models/{modelID}", s.handleModelUpdate)
	mux.HandleFunc("DELETE /api/providers/{providerID}/models/{modelID}", s.handleModelDelete)
	mux.HandleFunc("POST /api/providers/{providerID}/models/{modelID}/test", s.handleModelTest)
	mux.HandleFunc("GET /api/models/available", s.handleAvailableModels)

	mux.HandleFunc("GET /api/proxy/status", s.handleProxyStatus)
	mux.HandleFunc("POST /api/proxy/start", s.handleProxyStart)
	mux.HandleFunc("POST /api/proxy/stop", s.handleProxyStop)
	mux.HandleFunc("POST /api/proxy/restart", s.handleProxyRestart)

	mux.HandleFunc("GET /api/codex", s.handleCodexStatus)
	mux.HandleFunc("PUT /api/codex/auto", s.handleCodexAuto)
	mux.HandleFunc("POST /api/codex/sync", s.handleCodexSync)
	mux.HandleFunc("POST /api/codex/disconnect", s.handleCodexDisconnect)

	mux.HandleFunc("GET /api/opencode", s.handleOpenCodeStatus)
	mux.HandleFunc("PUT /api/opencode/auto", s.handleOpenCodeAuto)
	mux.HandleFunc("PUT /api/opencode/shape", s.handleOpenCodeShape)
	mux.HandleFunc("POST /api/opencode/sync", s.handleOpenCodeSync)
	mux.HandleFunc("POST /api/opencode/disconnect", s.handleOpenCodeDisconnect)

	mux.HandleFunc("GET /api/trae", s.handleTraeStatus)
	mux.HandleFunc("GET /api/trae/progress", s.handleTraeProgress)
	mux.HandleFunc("POST /api/trae/sync", s.handleTraeSync)
	mux.HandleFunc("POST /api/trae/reorder", s.handleTraeReorder)
	mux.HandleFunc("POST /api/trae/restart", s.handleTraeRestart)

	mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	mux.HandleFunc("PUT /api/settings", s.handleSettingsUpdate)
	mux.HandleFunc("POST /api/system/open-config-dir", s.handleOpenConfigDir)
	mux.HandleFunc("POST /api/system/open-log-dir", s.handleOpenLogDir)
}

// ---- 概览 ----

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	enabled := 0
	for _, m := range cfg.Models {
		if m.Enabled {
			enabled++
		}
	}
	writeJSON(w, 200, map[string]any{
		"proxy": map[string]any{
			"running": s.proxy.Running(), "host": cfg.Settings.Proxy.Host,
			"port": cfg.Settings.Proxy.Port, "protocols": []string{"chat", "responses", "messages"},
		},
		"counts": map[string]any{"providers": len(cfg.Providers), "models": len(cfg.Models), "enabled_models": enabled},
		"codex": map[string]any{
			"connected": s.codex.Connected(), "auto_sync": cfg.Settings.CodexAutoSync,
			"config_path": s.codex.Paths().ConfigTOML,
		},
		"opencode": map[string]any{
			"connected": s.opencode.Connected(), "auto_sync": cfg.Settings.OpenCodeAutoSync,
			"config_path": s.opencode.Paths().Config,
		},
		"default_model":   cfg.Settings.DefaultModel,
		"recent_requests": s.proxy.Recent(8),
		"recent_errors":   s.proxy.RecentErrors(5),
		"repairs":         s.store.LastRepairs(),
	})
}

// ---- 供应商 ----

type authDTO struct {
	Type         string `json:"type"`
	APIKeyMasked string `json:"api_key_masked,omitempty"`
	Header       string `json:"header,omitempty"`
}

type providerDTO struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Preset     string   `json:"preset,omitempty"`
	BaseURL    string   `json:"base_url"`
	Protocol   string   `json:"protocol"`
	Protocols  []string `json:"protocols"`
	Auth       authDTO  `json:"auth"`
	Enabled    bool     `json:"enabled"`
	ModelCount int      `json:"model_count"`
}

func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "••••"
	}
	if len(key) <= 10 {
		return key[:2] + "…" + key[len(key)-2:]
	}
	return key[:4] + "…" + key[len(key)-4:]
}

func (s *Server) toProviderDTO(p config.Provider, modelCount int) providerDTO {
	return providerDTO{
		ID: p.ID, Name: p.Name, Preset: p.Preset, BaseURL: p.BaseURL,
		Protocol: p.Protocol, Protocols: p.UpstreamProtocols(),
		Auth:    authDTO{Type: p.Auth.Type, APIKeyMasked: maskKey(p.Auth.APIKey), Header: p.Auth.Header},
		Enabled: p.Enabled, ModelCount: modelCount,
	}
}

func (s *Server) handleProviderList(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	out := []providerDTO{}
	for _, p := range cfg.Providers {
		out = append(out, s.toProviderDTO(p, len(cfg.ModelsOf(p.ID))))
	}
	writeJSON(w, 200, out)
}

type providerInput struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Preset    string   `json:"preset"`
	BaseURL   string   `json:"base_url"`
	Protocol  string   `json:"protocol"`  // 兼容旧字段（单值）
	Protocols []string `json:"protocols"` // 多协议，按优先级排列
	Auth      authDTO  `json:"auth"`
	APIKey    string   `json:"api_key"`
	Header    string   `json:"header"`
	Enabled   *bool    `json:"enabled"`
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// normalizedProtocols 归一化协议列表：兼容旧的单值 protocol 字段，去重并校验。
func (in *providerInput) normalizedProtocols() ([]string, error) {
	raw := in.Protocols
	if len(raw) == 0 && strings.TrimSpace(in.Protocol) != "" {
		raw = []string{in.Protocol}
	}
	out := []string{}
	for _, r := range raw {
		proto := strings.ToLower(strings.TrimSpace(r))
		if proto == "" {
			continue
		}
		if !config.ValidProtocol(proto) {
			return nil, fmt.Errorf("协议 %q 无效，必须是 chat、responses 或 messages", r)
		}
		if !containsString(out, proto) {
			out = append(out, proto)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("请至少选择一个协议")
	}
	return out, nil
}

func normalizeProviderID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.ReplaceAll(id, "/", "")
	return strings.ToLower(id)
}

// deriveProviderID 在未填写时由名称推导一个可用的供应商 ID。
func deriveProviderID(name string) string {
	if id := config.DeriveProviderID(name); id != "" {
		return id
	}
	return randomID("prv_")
}

func (in *providerInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("供应商名称不能为空")
	}
	if strings.TrimSpace(in.BaseURL) == "" || !strings.HasPrefix(in.BaseURL, "http") {
		return errors.New("接口地址必须以 http(s):// 开头")
	}
	if _, err := in.normalizedProtocols(); err != nil {
		return err
	}
	switch in.Auth.Type {
	case "", "bearer", "api_key_header", "custom":
	default:
		return errors.New("认证类型不支持")
	}
	return nil
}

func normAuth(in *providerInput) config.Auth {
	a := config.Auth{Type: in.Auth.Type, APIKey: in.APIKey, Header: in.Header}
	if a.Type == "" {
		a.Type = "bearer"
	}
	if a.Header == "" && (a.Type == "api_key_header" || a.Type == "custom") {
		a.Header = "x-api-key"
	}
	return a
}

func (s *Server) handleProviderCreate(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := in.validate(); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	protocols, err := in.normalizedProtocols()
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	var created config.Provider
	err = s.store.Update(func(c *config.Config) error {
		if _, ok := c.FindProviderByName(in.Name); ok {
			return fmt.Errorf("供应商名称 %q 已存在", in.Name)
		}
		id := normalizeProviderID(in.ID)
		if id == "" {
			id = deriveProviderID(in.Name)
		}
		for _, p := range c.Providers {
			if strings.EqualFold(p.ID, id) {
				return fmt.Errorf("供应商 ID %q 已存在，请换一个", id)
			}
		}
		created = config.Provider{
			ID: id, Name: in.Name, Preset: in.Preset, BaseURL: strings.TrimRight(in.BaseURL, "/"),
			Protocol: protocols[0], Protocols: protocols, Auth: normAuth(&in), Enabled: enabled,
		}
		c.Providers = append(c.Providers, created)
		return nil
	})
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.toProviderDTO(created, 0))
}

func (s *Server) handleProviderUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in providerInput
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := in.validate(); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	protocols, err := in.normalizedProtocols()
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	var updated config.Provider
	err = s.store.Update(func(c *config.Config) error {
		for i := range c.Providers {
			if c.Providers[i].ID != id {
				continue
			}
			for _, other := range c.Providers {
				if other.ID != id && other.Name == in.Name {
					return fmt.Errorf("供应商名称 %q 已存在", in.Name)
				}
			}
			p := &c.Providers[i]
			oldID := p.ID
			newID := normalizeProviderID(in.ID)
			if newID == "" {
				newID = oldID
			}
			if !strings.EqualFold(newID, oldID) {
				for _, other := range c.Providers {
					if strings.EqualFold(other.ID, newID) {
						return fmt.Errorf("供应商 ID %q 已存在，请换一个", newID)
					}
				}
			}
			p.ID = newID
			p.Name, p.Preset = in.Name, in.Preset
			p.Protocols, p.Protocol = protocols, protocols[0]
			p.BaseURL = strings.TrimRight(in.BaseURL, "/")
			auth := normAuth(&in)
			if auth.APIKey == "" {
				auth.APIKey = p.Auth.APIKey // 留空表示保留原 Key
			}
			p.Auth = auth
			if in.Enabled != nil {
				p.Enabled = *in.Enabled
			}
			if newID != oldID {
				// 供应商 ID 变更：级联更新模型归属与默认模型
				for j := range c.Models {
					if c.Models[j].ProviderID == oldID {
						c.Models[j].ProviderID = newID
					}
				}
				if strings.HasPrefix(c.Settings.DefaultModel, oldID+"/") {
					c.Settings.DefaultModel = newID + "/" + strings.TrimPrefix(c.Settings.DefaultModel, oldID+"/")
				}
			}
			updated = *p
			return nil
		}
		return errors.New("供应商不存在")
	})
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	cfg := s.store.Snapshot()
	writeJSON(w, 200, s.toProviderDTO(updated, len(cfg.ModelsOf(updated.ID))))
}

func (s *Server) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	removed := 0
	err := s.store.Update(func(c *config.Config) error {
		idx := -1
		for i := range c.Providers {
			if c.Providers[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return errors.New("供应商不存在")
		}
		c.Providers = append(c.Providers[:idx], c.Providers[idx+1:]...)
		models := c.Models[:0]
		for _, m := range c.Models {
			if m.ProviderID == id {
				removed++
				continue
			}
			models = append(models, m)
		}
		c.Models = models
		return nil
	})
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed_models": removed})
}

func (s *Server) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	res := s.providers.Test(r.Context(), r.PathValue("id"))
	writeJSON(w, 200, res)
}

func (s *Server) handleProviderSecret(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	p, ok := cfg.FindProvider(r.PathValue("id"))
	if !ok {
		apiErr(w, 404, "供应商不存在")
		return
	}
	writeJSON(w, 200, map[string]string{"api_key": p.Auth.APIKey})
}

func (s *Server) handleFetchModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.providers.FetchModels(r.Context(), r.PathValue("id"))
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, []map[string]any{
		{"preset": "km-aimodelhub", "name": "KmAiModelHub", "protocol": "responses", "protocols": []string{"responses", "chat"}, "base_url": "https://aimodelhub.ai.kmsoft.com.cn/v1", "hint": "企业模型网关 · 同时提供 Responses / Chat"},
		{"preset": "opencode-go", "name": "OpenCode Go", "protocol": "chat", "protocols": []string{"chat"}, "base_url": "https://opencode.ai/zen/go/v1", "hint": "多端点网关 · 模型级可固定协议"},
		{"preset": "deepseek", "name": "DeepSeek", "protocol": "chat", "protocols": []string{"chat"}, "base_url": "https://api.deepseek.com", "hint": "OpenAI 兼容 · 无需 /v1"},
		{"preset": "openai", "name": "OpenAI", "protocol": "responses", "protocols": []string{"responses", "chat"}, "base_url": "https://api.openai.com/v1", "hint": "Responses 优先，兼容 Chat"},
		{"preset": "anthropic", "name": "Anthropic", "protocol": "messages", "protocols": []string{"messages"}, "base_url": "https://api.anthropic.com/v1", "hint": "Messages API"},
		{"preset": "custom", "name": "自定义", "protocol": "chat", "protocols": []string{"chat"}, "base_url": "", "hint": "手动填写地址"},
	})
}

// ---- 模型 ----

type modelDTO struct {
	ID              string            `json:"id"`
	ProviderID      string            `json:"provider_id"`
	ProviderName    string            `json:"provider_name"`
	Slug            string            `json:"slug"`
	Name            string            `json:"name,omitempty"`
	Description     string            `json:"description,omitempty"`
	Capabilities    []string          `json:"capabilities,omitempty"`
	Protocol        string            `json:"protocol,omitempty"`
	ContextWindow   *int              `json:"context_window,omitempty"`
	MaxOutputTokens *int              `json:"max_output_tokens,omitempty"`
	Reasoning       *config.Reasoning `json:"reasoning,omitempty"`
	Enabled         bool              `json:"enabled"`
}

func (s *Server) toModelDTO(cfg *config.Config, m config.Model) modelDTO {
	name := ""
	providerID := m.ProviderID
	if p, ok := cfg.FindProvider(m.ProviderID); ok {
		name = p.Name
		providerID = p.ID
	}
	return modelDTO{
		ID: m.ID, ProviderID: providerID, ProviderName: name, Slug: providerID + "/" + m.ID,
		Name: m.Name, Description: m.Description, Capabilities: m.Capabilities,
		Protocol: m.Protocol, ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens,
		Reasoning: m.Reasoning, Enabled: m.Enabled,
	}
}

func (s *Server) handleModelList(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	pid := r.PathValue("providerID")
	out := []modelDTO{}
	for _, m := range cfg.ModelsOf(pid) {
		out = append(out, s.toModelDTO(&cfg, m))
	}
	writeJSON(w, 200, out)
}

type modelInput struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Capabilities    []string          `json:"capabilities"`
	Protocol        string            `json:"protocol"`
	ContextWindow   *int              `json:"context_window"`
	MaxOutputTokens *int              `json:"max_output_tokens"`
	Reasoning       *config.Reasoning `json:"reasoning"`
	Enabled         *bool             `json:"enabled"`
}

func (in *modelInput) validate() error {
	if strings.TrimSpace(in.ID) == "" {
		return errors.New("模型 ID 不能为空")
	}
	// 上游模型 ID 允许包含 /（例如 Qwen/Qwen3.6-35B-A3B），对外请求名会自动拼接供应商 ID 前缀
	switch in.Protocol {
	case "", "chat", "responses", "messages":
	default:
		return errors.New("模型协议必须是 chat、responses、messages 或留空（跟随供应商）")
	}
	if in.Reasoning != nil && in.Reasoning.DefaultLevel != "" && len(in.Reasoning.Levels) > 0 {
		found := false
		for _, l := range in.Reasoning.Levels {
			if strings.EqualFold(l, in.Reasoning.DefaultLevel) {
				found = true
			}
		}
		if !found {
			return errors.New("默认推理档位不在支持列表中")
		}
	}
	return nil
}

func (s *Server) handleModelCreate(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("providerID")
	var in modelInput
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := in.validate(); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var created config.Model
	err := s.store.Update(func(c *config.Config) error {
		p, ok := c.FindProvider(pid)
		if !ok {
			return errors.New("供应商不存在")
		}
		if _, ok := c.FindModel(pid, in.ID); ok {
			return fmt.Errorf("模型 %q 已存在", in.ID)
		}
		name := in.Name
		if name == "" {
			name = in.ID
		}
		created = config.Model{
			ID: in.ID, ProviderID: pid, Name: name, Description: in.Description,
			Capabilities: in.Capabilities, Protocol: in.Protocol, ContextWindow: in.ContextWindow,
			MaxOutputTokens: in.MaxOutputTokens,
			Reasoning:       in.Reasoning, Enabled: enabled,
		}
		c.Models = append(c.Models, created)
		// 首次添加模型时自动设为默认模型，用户无需任何操作
		if c.Settings.DefaultModel == "" {
			c.Settings.DefaultModel = p.ID + "/" + in.ID
		}
		return nil
	})
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	cfg := s.store.Snapshot()
	writeJSON(w, 200, s.toModelDTO(&cfg, created))
}

func (s *Server) handleModelUpdate(w http.ResponseWriter, r *http.Request) {
	pid, mid := r.PathValue("providerID"), r.PathValue("modelID")
	var in modelInput
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := in.validate(); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	var updated config.Model
	err := s.store.Update(func(c *config.Config) error {
		for i := range c.Models {
			m := &c.Models[i]
			if m.ProviderID != pid || m.ID != mid {
				continue
			}
			if in.ID != mid {
				if _, exists := c.FindModel(pid, in.ID); exists {
					return fmt.Errorf("模型 %q 已存在", in.ID)
				}
				m.ID = in.ID
			}
			m.Name, m.Description = in.Name, in.Description
			m.Capabilities, m.Protocol = in.Capabilities, in.Protocol
			m.ContextWindow = in.ContextWindow
			m.MaxOutputTokens, m.Reasoning = in.MaxOutputTokens, in.Reasoning
			if in.Enabled != nil {
				m.Enabled = *in.Enabled
			}
			if m.Name == "" {
				m.Name = m.ID
			}
			updated = *m
			return nil
		}
		return errors.New("模型不存在")
	})
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	cfg := s.store.Snapshot()
	writeJSON(w, 200, s.toModelDTO(&cfg, updated))
}

func (s *Server) handleModelDelete(w http.ResponseWriter, r *http.Request) {
	pid, mid := r.PathValue("providerID"), r.PathValue("modelID")
	err := s.store.Update(func(c *config.Config) error {
		idx := -1
		for i := range c.Models {
			if c.Models[i].ProviderID == pid && c.Models[i].ID == mid {
				idx = i
				break
			}
		}
		if idx < 0 {
			return errors.New("模型不存在")
		}
		c.Models = append(c.Models[:idx], c.Models[idx+1:]...)
		return nil
	})
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleModelTest(w http.ResponseWriter, r *http.Request) {
	res := s.providers.TestModel(r.Context(), r.PathValue("providerID"), r.PathValue("modelID"))
	writeJSON(w, 200, res)
}

func (s *Server) handleAvailableModels(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	out := []map[string]any{}
	for _, m := range cfg.Models {
		p, ok := cfg.FindProvider(m.ProviderID)
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"slug": p.ID + "/" + m.ID, "provider_id": p.ID, "provider": p.Name, "id": m.ID,
			"name": m.Name, "enabled": m.Enabled && p.Enabled, "context_window": m.ContextWindow,
		})
	}
	writeJSON(w, 200, out)
}

// ---- 代理控制 ----

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	writeJSON(w, 200, map[string]any{"running": s.proxy.Running(), "host": cfg.Settings.Proxy.Host, "port": cfg.Settings.Proxy.Port})
}

func (s *Server) handleProxyStart(w http.ResponseWriter, r *http.Request) {
	if err := s.proxy.Start(); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"running": true})
}

func (s *Server) handleProxyStop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.proxy.Stop(ctx); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"running": false})
}

func (s *Server) handleProxyRestart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.proxy.Restart(ctx); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"running": true})
}

// ---- Codex ----

func (s *Server) handleCodexStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.codex.Status()
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleCodexAuto(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.store.Update(func(c *config.Config) error {
		c.Settings.CodexAutoSync = in.Enabled
		return nil
	}); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"auto_sync": in.Enabled})
}

func (s *Server) handleCodexSync(w http.ResponseWriter, r *http.Request) {
	var ov codex.Overrides
	if err := readJSON(r, &ov); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	plan, err := s.codex.BuildPlan(ov)
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.codex.Apply(plan); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	if ov.DefaultModel != "" {
		s.store.Update(func(c *config.Config) error {
			c.Settings.DefaultModel = ov.DefaultModel
			return nil
		})
	} else if plan.DefaultModel != "" {
		// 自动模式下把选定的默认模型落库，保证界面与 Codex 一致
		s.store.Update(func(c *config.Config) error {
			if c.Settings.DefaultModel == "" {
				c.Settings.DefaultModel = plan.DefaultModel
			}
			return nil
		})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已同步到 Codex 配置", "warnings": plan.Warnings})
}

func (s *Server) handleCodexDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.codex.Disconnect(); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已断开 Codex 接入"})
}

// ---- OpenCode ----

func (s *Server) handleOpenCodeStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.opencode.Status()
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleOpenCodeAuto(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.store.Update(func(c *config.Config) error {
		c.Settings.OpenCodeAutoSync = in.Enabled
		return nil
	}); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"auto_sync": in.Enabled})
}

// handleOpenCodeShape 设置写入形状：auto（按文件判断）/ v1（OpenCode 1.x）/ v2（OpenCode 2.x 原生）。
func (s *Server) handleOpenCodeShape(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Shape string `json:"shape"`
	}
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	shape := strings.ToLower(strings.TrimSpace(in.Shape))
	switch shape {
	case "auto", "v1", "v2":
	default:
		apiErr(w, 400, "形状必须是 auto、v1 或 v2")
		return
	}
	if err := s.store.Update(func(c *config.Config) error {
		c.Settings.OpenCodeShape = shape
		return nil
	}); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"shape": shape})
}

func (s *Server) handleOpenCodeSync(w http.ResponseWriter, r *http.Request) {
	var ov opencode.Overrides
	if err := readJSON(r, &ov); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	plan, err := s.opencode.BuildPlan(ov)
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.opencode.Apply(plan); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "message": "已同步到 OpenCode 配置",
		"models": len(plan.Models), "path": plan.Path, "warnings": plan.Warnings,
	})
}

func (s *Server) handleOpenCodeDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.opencode.Disconnect(); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已断开 OpenCode 接入"})
}

// ---- Trae SOLO ----

func (s *Server) handleTraeStatus(w http.ResponseWriter, r *http.Request) {
	st := s.trae.Status(r.Context())
	writeJSON(w, 200, st)
}

func (s *Server) handleTraeProgress(w http.ResponseWriter, r *http.Request) {
	job := s.trae.Progress()
	writeJSON(w, 200, map[string]any{"job": job})
}

func (s *Server) handleTraeSync(w http.ResponseWriter, r *http.Request) {
	if err := s.trae.StartSync(); err != nil {
		apiErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已开始同步"})
}

func (s *Server) handleTraeReorder(w http.ResponseWriter, r *http.Request) {
	if err := s.trae.StartReorder(); err != nil {
		apiErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已开始重新排序"})
}

func (s *Server) handleTraeRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.trae.Restart(r.Context()); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Trae 已重启，调试端口就绪"})
}

// ---- 设置 ----

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	writeJSON(w, 200, map[string]any{
		"settings": cfg.Settings,
		"version":  buildinfo.Version,
		"paths": map[string]string{
			"config_dir":  config.Dir(),
			"config_file": s.store.Path(),
			"log_dir":     s.logDir,
		},
	})
}

func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Proxy              *config.Endpoint    `json:"proxy"`
		Log                *config.LogSettings `json:"log"`
		OpenBrowserOnStart *bool               `json:"open_browser_on_start"`
		AutoStart          *bool               `json:"auto_start"`
		DefaultModel       *string             `json:"default_model"`
	}
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	autoStartChanged := false
	autoStartValue := false
	proxyChanged := false
	err := s.store.Update(func(c *config.Config) error {
		if in.Proxy != nil {
			if in.Proxy.Port < 1024 || in.Proxy.Port > 65535 {
				return errors.New("代理端口必须在 1024-65535 之间")
			}
			if *in.Proxy != c.Settings.Proxy {
				proxyChanged = true
			}
			c.Settings.Proxy = *in.Proxy
		}
		if in.Log != nil {
			c.Settings.Log = *in.Log
		}
		if in.OpenBrowserOnStart != nil {
			c.Settings.OpenBrowserOnStart = *in.OpenBrowserOnStart
		}
		if in.AutoStart != nil && *in.AutoStart != c.Settings.AutoStart {
			autoStartChanged, autoStartValue = true, *in.AutoStart
			c.Settings.AutoStart = *in.AutoStart
		}
		if in.DefaultModel != nil {
			c.Settings.DefaultModel = *in.DefaultModel
		}
		return nil
	})
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if autoStartChanged && s.onAutoStart != nil {
		if err := s.onAutoStart(autoStartValue); err != nil {
			apiErr(w, 500, "写入开机启动失败: "+err.Error())
			return
		}
	}
	warning := ""
	if proxyChanged && s.proxy.Running() {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.proxy.Restart(ctx); err != nil {
			warning = "设置已保存，但代理重启失败：" + err.Error()
		}
	}
	writeJSON(w, 200, map[string]any{"settings": s.store.Snapshot().Settings, "warning": warning})
}

func (s *Server) handleOpenConfigDir(w http.ResponseWriter, r *http.Request) {
	openDir(w, config.Dir())
}

func (s *Server) handleOpenLogDir(w http.ResponseWriter, r *http.Request) {
	openDir(w, s.logDir)
}

func openDir(w http.ResponseWriter, dir string) {
	os.MkdirAll(dir, 0o755)
	if err := winutil.Open(dir); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- 工具 ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("请求体不是合法 JSON: %w", err)
	}
	return nil
}

func randomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func randomID(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
