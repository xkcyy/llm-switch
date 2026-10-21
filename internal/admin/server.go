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
	"sync"
	"time"

	"llm-switch/internal/buildinfo"
	"llm-switch/internal/codex"
	"llm-switch/internal/config"
	"llm-switch/internal/dsh"
	"llm-switch/internal/opencode"
	"llm-switch/internal/provider"
	"llm-switch/internal/proxy"
	"llm-switch/internal/proxy/protocol"
	"llm-switch/internal/trace"
	"llm-switch/internal/trae"
	"llm-switch/internal/usage"
	"llm-switch/internal/winutil"
	"llm-switch/internal/workbuddy"
)

type Server struct {
	store     *config.Store
	providers *provider.Service
	proxy     *proxy.Server
	codex     *codex.Service
	opencode  *opencode.Service
	trae      *trae.Service
	dsh       *dsh.Service
	workbuddy *workbuddy.Service
	token     string
	webFS     fs.FS

	onAutoStart func(bool) error
	logDir      string

	http    *http.Server
	ln      net.Listener
	running bool

	verifyMu      sync.Mutex
	verifications map[string]AgentVerification
}

func New(store *config.Store, prov *provider.Service, px *proxy.Server, cx *codex.Service, oc *opencode.Service, tr *trae.Service, ds *dsh.Service, wb *workbuddy.Service, webFS fs.FS, logDir string, onAutoStart func(bool) error) *Server {
	return &Server{
		store: store, providers: prov, proxy: px, codex: cx, opencode: oc, trae: tr, dsh: ds, workbuddy: wb,
		token: randomToken(), webFS: webFS, logDir: logDir, onAutoStart: onAutoStart,
		verifications: map[string]AgentVerification{},
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
	mux.HandleFunc("GET /api/usage", s.handleUsage)
	mux.HandleFunc("GET /api/usage/report", s.handleUsageReport)
	mux.HandleFunc("GET /api/metrics/report", s.handleMetricsReport)

	mux.HandleFunc("GET /api/trajectory", s.handleTrajectoryList)
	mux.HandleFunc("GET /api/trajectory/{id}", s.handleTrajectoryDetail)
	mux.HandleFunc("GET /api/trajectory/{id}/raw", s.handleTrajectoryRaw)

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

	mux.HandleFunc("GET /api/dsh", s.handleDshStatus)
	mux.HandleFunc("PUT /api/dsh/auto", s.handleDshAuto)
	mux.HandleFunc("PUT /api/dsh/profile", s.handleDshProfile)
	mux.HandleFunc("POST /api/dsh/sync", s.handleDshSync)
	mux.HandleFunc("POST /api/dsh/disconnect", s.handleDshDisconnect)

	mux.HandleFunc("GET /api/workbuddy", s.handleWorkBuddyStatus)
	mux.HandleFunc("PUT /api/workbuddy/auto", s.handleWorkBuddyAuto)
	mux.HandleFunc("POST /api/workbuddy/sync", s.handleWorkBuddySync)
	mux.HandleFunc("POST /api/workbuddy/disconnect", s.handleWorkBuddyDisconnect)

	mux.HandleFunc("POST /api/agents/{agent}/test", s.handleAgentTest)

	mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	mux.HandleFunc("PUT /api/settings", s.handleSettingsUpdate)
	mux.HandleFunc("POST /api/system/open-config-dir", s.handleOpenConfigDir)
	mux.HandleFunc("POST /api/system/open-log-dir", s.handleOpenLogDir)
}

// ---- 概览 ----

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	writeJSON(w, 200, map[string]any{
		"proxy": map[string]any{
			"running": s.proxy.Running(), "host": cfg.Settings.Proxy.Host,
			"port": cfg.Settings.Proxy.Port, "protocols": []string{"chat", "responses", "messages"},
		},
		"counts": map[string]any{"providers": len(cfg.Providers), "models": len(cfg.Models), "enabled_models": len(cfg.AvailableModels())},
		"codex": map[string]any{
			"connected": s.codex.Connected(), "auto_sync": cfg.Settings.CodexAutoSync,
			"config_path": s.codex.Paths().ConfigTOML,
		},
		"opencode": map[string]any{
			"connected": s.opencode.Connected(), "auto_sync": cfg.Settings.OpenCodeAutoSync,
			"config_path": s.opencode.Paths().Config,
		},
		"dsh": map[string]any{
			"connected": s.dsh.Connected(cfg.Settings.DshProfile), "auto_sync": cfg.Settings.DshAutoSync,
			"profile": cfg.Settings.DshProfile, "config_path": dsh.ResolvePaths(cfg.Settings.DshProfile).Patch,
		},
		"default_model":       cfg.Settings.DefaultModel,
		"recent_requests":     s.proxy.Recent(8),
		"recent_errors":       s.proxy.RecentErrors(5),
		"repairs":             s.store.LastRepairs(),
		"agent_verifications": s.verificationSnapshot(),
	})
}

// ---- 供应商 ----

// ---- 用量 ----

// ---- 轨迹 ----

// handleTrajectoryList 返回最近入站推理请求的摘要（不含请求体）。
func (s *Server) handleTrajectoryList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"capacity": trace.Capacity,
		"requests": s.proxy.Traces().List(),
	})
}

// handleTrajectoryDetail 返回单条请求解析后的轨迹文档（系统提示词、工具、历史行）。
func (s *Server) handleTrajectoryDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求 ID 无效"})
		return
	}
	entry, ok := s.proxy.Traces().Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "轨迹记录不存在或已被覆盖"})
		return
	}
	resp := map[string]any{"entry": entry}
	p, err := protocol.Parse(entry.Protocol)
	if err != nil {
		resp["error"] = "未知入口协议：" + entry.Protocol
		writeJSON(w, http.StatusOK, resp)
		return
	}
	doc, err := protocol.TraceRequest(p, entry.Body)
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["document"] = doc
	writeJSON(w, http.StatusOK, resp)
}

// handleTrajectoryRaw 返回单条请求的原始入站请求体与上游响应体。
func (s *Server) handleTrajectoryRaw(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求 ID 无效"})
		return
	}
	entry, ok := s.proxy.Traces().Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "轨迹记录不存在或已被覆盖"})
		return
	}
	resp := map[string]any{
		"entry": entry, "headers": entry.Headers, "body": string(entry.Body),
		"response": nil,
	}
	if entry.Response != nil {
		resp["response"] = map[string]any{
			"status":    entry.Response.Status,
			"proto":     entry.Response.Proto,
			"bytes":     entry.Response.Bytes,
			"truncated": entry.Response.Truncated,
			"headers":   entry.Response.Headers,
			"body":      string(entry.Response.Body),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUsage 返回累计与今日的用量合计。时间粒度与分组查询在后续迭代补齐。
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	store := s.proxy.Usage()
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	writeJSON(w, 200, map[string]any{
		"total": store.Totals(time.Time{}, now),
		"today": store.Totals(dayStart, dayStart),
	})
}

// handleUsageReport 返回用量报表：按分组的时间序列 + 分组明细。
// range 取值 today / 7d / month / 30d / custom；group 取值 all / provider / model。
func (s *Server) handleUsageReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	span := q.Get("range")
	if span == "" {
		span = "30d"
	}
	from, to, gran := today.AddDate(0, 0, -29), today, usage.GranDay
	switch span {
	case "today":
		from, to, gran = today, today.Add(23*time.Hour), usage.GranHour
	case "7d":
		from, to = today.AddDate(0, 0, -6), today
	case "month":
		from, to = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc), today
	case "custom":
		f, errF := time.ParseInLocation("2006-01-02", q.Get("from"), loc)
		t, errT := time.ParseInLocation("2006-01-02", q.Get("to"), loc)
		if errF == nil && errT == nil && !t.Before(f) {
			from, to = f, t
			if to.Sub(from) > 30*24*time.Hour {
				gran = usage.GranMonth
			}
		}
	}
	if gran == usage.GranMonth {
		from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, loc)
	}

	group := q.Get("group")
	if group == "" {
		group = usage.GroupAll
	}
	res := s.proxy.Usage().Query(usage.Query{
		From: from, To: to, Granularity: gran, Group: group,
		ProviderID: q.Get("provider"), ModelID: q.Get("model"), Caller: q.Get("caller"),
	})
	writeJSON(w, 200, map[string]any{
		"range": span, "granularity": gran, "group": group,
		"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"),
		"totals": res.Totals,
		"series": fillSeries(res.Series, res.Groups, from, to, gran),
		"groups": res.Groups,
	})
}

// handleMetricsReport 返回性能与可用性报表：供应商/模型的时间序列与派生指标。
func (s *Server) handleMetricsReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	span := q.Get("range")
	if span == "" {
		span = "90m"
	}

	from, to := now.Truncate(time.Minute).Add(-89*time.Minute), now.Truncate(time.Minute)
	gran := usage.GranMinute
	switch span {
	case "24h":
		to = now.Truncate(time.Hour)
		from, gran = to.Add(-23*time.Hour), usage.GranHour
	case "today":
		from, to, gran = today, today.Add(23*time.Hour), usage.GranHour
	case "7d":
		from, to, gran = today.AddDate(0, 0, -6), today, usage.GranDay
	case "30d":
		from, to, gran = today.AddDate(0, 0, -29), today, usage.GranDay
	case "all":
		from, to, gran = today.AddDate(0, 0, -89), today, usage.GranDay
		if earliest, ok := s.proxy.Usage().EarliestDay(); ok {
			if parsed, err := time.ParseInLocation("2006-01-02", earliest, loc); err == nil {
				from = parsed
			}
		}
	case "custom":
		f, errF := time.ParseInLocation("2006-01-02", q.Get("from"), loc)
		t, errT := time.ParseInLocation("2006-01-02", q.Get("to"), loc)
		if errF != nil || errT != nil || t.Before(f) {
			f, t = today.AddDate(0, 0, -6), today
		}
		days := int(t.Sub(f)/(24*time.Hour)) + 1
		from, to = f, t.Add(24*time.Hour-time.Minute)
		switch {
		case days > 30:
			gran = usage.GranMonth
			from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, loc)
		case days > 1:
			gran = usage.GranDay
		default:
			gran = usage.GranHour
		}
	}

	group := q.Get("group")
	if group == "" {
		group = usage.GroupProvider
	}
	res := s.proxy.Usage().Query(usage.Query{
		From: from, To: to, Granularity: gran, Group: group,
		ProviderID: q.Get("provider"), ModelID: q.Get("model"), Caller: q.Get("caller"),
	})

	type point struct {
		Period string `json:"period"`
		Group  string `json:"group,omitempty"`
		usage.Totals
		Metrics usage.Metrics `json:"metrics"`
	}
	type row struct {
		usage.GroupRow
		Metrics usage.Metrics `json:"metrics"`
	}
	series := make([]point, 0)
	for _, p := range fillSeries(res.Series, res.Groups, from, to, gran) {
		series = append(series, point{Period: p.Period, Group: p.Group, Totals: p.Totals, Metrics: p.Totals.Derive()})
	}
	groups := make([]row, 0, len(res.Groups))
	for _, g := range res.Groups {
		groups = append(groups, row{GroupRow: g, Metrics: g.Totals.Derive()})
	}

	layout := "2006-01-02"
	if gran == usage.GranMinute {
		layout = "2006-01-02T15:04"
	} else if gran == usage.GranHour {
		layout = "2006-01-02T15"
	}
	writeJSON(w, 200, map[string]any{
		"range": span, "granularity": gran, "group": group,
		"from": from.Format(layout), "to": to.Format(layout),
		"totals":  res.Totals,
		"summary": res.Totals.Derive(),
		"series":  series, "groups": groups,
	})
}

// fillSeries 把没有数据的时段补成 0，并为每个分组各生成一条序列。
func fillSeries(points []usage.SeriesPoint, groups []usage.GroupRow, from, to time.Time, gran string) []usage.SeriesPoint {
	byKey := make(map[[2]string]usage.Totals, len(points))
	for _, p := range points {
		byKey[[2]string{p.Period, p.Group}] = p.Totals
	}
	keys := make([]string, 0, len(groups))
	for _, g := range groups {
		keys = append(keys, g.Key)
	}
	if len(keys) == 0 {
		keys = []string{""}
	}
	out := []usage.SeriesPoint{}
	add := func(period string) {
		for _, k := range keys {
			out = append(out, usage.SeriesPoint{Period: period, Group: k, Totals: byKey[[2]string{period, k}]})
		}
	}
	switch gran {
	case usage.GranMinute:
		for t := from; !t.After(to); t = t.Add(time.Minute) {
			add(t.Format("2006-01-02T15:04"))
		}
	case usage.GranHour:
		for t := from; !t.After(to); t = t.Add(time.Hour) {
			add(t.Format("2006-01-02T15"))
		}
	case usage.GranMonth:
		for m := from; !m.After(to); m = m.AddDate(0, 1, 0) {
			add(m.Format("2006-01"))
		}
	default:
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			add(d.Format("2006-01-02"))
		}
	}
	return out
}

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
	Extensions []string `json:"extensions"`
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
		Extensions: p.Extensions,
		Auth:       authDTO{Type: p.Auth.Type, APIKeyMasked: maskKey(p.Auth.APIKey), Header: p.Auth.Header},
		Enabled:    p.Enabled, ModelCount: modelCount,
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
	// Extensions 是显式挂载的兼容扩展名；缺省（nil）表示保留原值，不覆盖。
	Extensions []string `json:"extensions"`
	Auth       authDTO  `json:"auth"`
	APIKey     string   `json:"api_key"`
	Header     string   `json:"header"`
	Enabled    *bool    `json:"enabled"`
}

// draft 把 HTTP 输入转成 config 的写入输入。
// JSON 字段名与兼容字段（单值 protocol、顶层 header）留在这一层；
// 写入规则（归一化、唯一性、级联）在 config 里。
func (in providerInput) draft() config.ProviderDraft {
	d := config.ProviderDraft{
		ID: in.ID, Name: in.Name, Preset: in.Preset, BaseURL: in.BaseURL,
		Protocol: in.Protocol, Protocols: in.Protocols,
		Auth:    config.AuthDraft{Type: in.Auth.Type, APIKey: in.APIKey, Header: in.Header},
		Enabled: in.Enabled,
	}
	if in.Extensions != nil {
		exts := in.Extensions
		d.Extensions = &exts
	}
	return d
}

func (s *Server) handleProviderCreate(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	var created config.Provider
	err := s.store.Update(func(c *config.Config) error {
		p, err := c.AddProvider(in.draft())
		created = p
		return err
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
	var updated config.Provider
	err := s.store.Update(func(c *config.Config) error {
		p, err := c.UpdateProvider(id, in.draft())
		updated = p
		return err
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
		n, err := c.RemoveProvider(id)
		removed = n
		return err
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
		ID: m.ID, ProviderID: providerID, ProviderName: name, Slug: config.ModelSlug(providerID, m.ID),
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

// draft 把 HTTP 输入转成 config 的写入输入。
func (in modelInput) draft() config.ModelDraft {
	return config.ModelDraft{
		ID: in.ID, Name: in.Name, Description: in.Description,
		Capabilities: in.Capabilities, Protocol: in.Protocol,
		ContextWindow: in.ContextWindow, MaxOutputTokens: in.MaxOutputTokens,
		Reasoning: in.Reasoning, Enabled: in.Enabled,
	}
}

func (s *Server) handleModelCreate(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("providerID")
	var in modelInput
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	var created config.Model
	err := s.store.Update(func(c *config.Config) error {
		m, err := c.AddModel(pid, in.draft())
		created = m
		return err
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
	var updated config.Model
	err := s.store.Update(func(c *config.Config) error {
		m, err := c.UpdateModel(pid, mid, in.draft())
		updated = m
		return err
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
		return c.RemoveModel(pid, mid)
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
	defer s.clearAgentVerification("codex")
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
	defer s.clearAgentVerification("codex")
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
	defer s.clearAgentVerification("opencode")
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
	defer s.clearAgentVerification("opencode")
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
	defer s.clearAgentVerification("trae")
	if err := s.trae.StartSync(); err != nil {
		apiErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已开始同步"})
}

func (s *Server) handleTraeReorder(w http.ResponseWriter, r *http.Request) {
	defer s.clearAgentVerification("trae")
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

// ---- DeepSeek Harness (dsh) ----

func (s *Server) handleDshStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.dsh.Status()
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleDshAuto(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.store.Update(func(c *config.Config) error {
		c.Settings.DshAutoSync = in.Enabled
		return nil
	}); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"auto_sync": in.Enabled})
}

// handleDshProfile 设置写入哪个 dsh profile：desktop（桌面端）或 web。
func (s *Server) handleDshProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Profile string `json:"profile"`
	}
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	profile := strings.TrimSpace(in.Profile)
	if profile == "" {
		apiErr(w, 400, "profile 不能为空")
		return
	}
	if err := s.store.Update(func(c *config.Config) error {
		c.Settings.DshProfile = profile
		return nil
	}); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"profile": profile})
}

func (s *Server) handleDshSync(w http.ResponseWriter, r *http.Request) {
	defer s.clearAgentVerification("dsh")
	var ov dsh.Overrides
	if err := readJSON(r, &ov); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	plan, err := s.dsh.BuildPlan(ov)
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.dsh.Apply(plan); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	if strings.TrimSpace(ov.Profile) != "" {
		s.store.Update(func(c *config.Config) error {
			c.Settings.DshProfile = plan.Profile
			return nil
		})
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "message": "已同步到 DeepSeek Harness 配置",
		"models": len(plan.Models), "path": plan.Path, "profile": plan.Profile, "warnings": plan.Warnings,
	})
}

func (s *Server) handleDshDisconnect(w http.ResponseWriter, r *http.Request) {
	defer s.clearAgentVerification("dsh")
	cfg := s.store.Snapshot()
	if err := s.dsh.Disconnect(cfg.Settings.DshProfile); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已断开 DeepSeek Harness 接入"})
}

func (s *Server) handleWorkBuddyStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.workbuddy.Status()
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleWorkBuddyAuto(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.store.Update(func(c *config.Config) error {
		c.Settings.WorkBuddyAutoSync = in.Enabled
		return nil
	}); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"auto_sync": in.Enabled})
}

func (s *Server) handleWorkBuddySync(w http.ResponseWriter, r *http.Request) {
	defer s.clearAgentVerification("workbuddy")
	var ov workbuddy.Overrides
	if err := readJSON(r, &ov); err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	plan, err := s.workbuddy.BuildPlan(ov)
	if err != nil {
		apiErr(w, 400, err.Error())
		return
	}
	if err := s.workbuddy.Apply(plan); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "message": "已同步到 WorkBuddy 自定义模型",
		"models": len(plan.Models), "path": plan.Path, "warnings": plan.Warnings,
	})
}

func (s *Server) handleWorkBuddyDisconnect(w http.ResponseWriter, r *http.Request) {
	defer s.clearAgentVerification("workbuddy")
	if err := s.workbuddy.Disconnect(); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已断开 WorkBuddy 接入"})
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
		TraceEnabled       *bool               `json:"trace_enabled"`
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
		if in.TraceEnabled != nil {
			c.Settings.TraceEnabled = *in.TraceEnabled
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
