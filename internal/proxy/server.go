package proxy

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"llm-switch/internal/buildinfo"
	"llm-switch/internal/config"
	"llm-switch/internal/proxy/protocol"
	"llm-switch/internal/proxy/protocol/compat"
	"llm-switch/internal/trace"
	"llm-switch/internal/upstream"
	"llm-switch/internal/usage"
)

const maxBodyBytes = 32 << 20

// Record 是一次代理请求的日志记录。
type Record struct {
	Time         time.Time `json:"time"`
	Entry        string    `json:"entry"`
	Source       string    `json:"source,omitempty"`
	Session      string    `json:"session,omitempty"`
	RequestModel string    `json:"request_model"`
	Provider     string    `json:"provider,omitempty"`
	Model        string    `json:"model,omitempty"`
	Upstream     string    `json:"upstream,omitempty"`
	Converted    bool      `json:"converted,omitempty"`
	Status       int       `json:"status"`
	DurationMS   int64     `json:"duration_ms"`
	Error        string    `json:"error,omitempty"`
	Warnings     []string  `json:"warnings,omitempty"`
}

// sourceOf 依据 User-Agent 粗略识别调用方（Codex / OpenCode / WorkBuddy / 其它），仅用于界面展示。
func sourceOf(r *http.Request) string {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	switch {
	case strings.Contains(ua, "opencode"):
		return "OpenCode"
	case strings.Contains(ua, "workbuddy") || strings.Contains(ua, "codebuddy"):
		return "WorkBuddy"
	case strings.Contains(ua, "codex"):
		return "Codex"
	case ua == "":
		return "未知"
	default:
		return "其它"
	}
}

// classifyFailure 把一次已到达供应商的失败归入可用性统计分类。
func classifyFailure(status int, err error) string {
	if status == 499 || errors.Is(err, context.Canceled) {
		return usage.OutcomeClientCancel
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usage.OutcomeAuthError
	}
	return usage.OutcomeUpstreamError
}

type Server struct {
	store  *config.Store
	index  *Holder
	client *http.Client

	mu      sync.Mutex
	srv     *http.Server
	ln      net.Listener
	running bool

	recMu   sync.Mutex
	records []Record

	usageStore *usage.Store
	traces     *trace.Store
}

func NewServer(store *config.Store, index *Holder) *Server {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	// 用量落盘到配置文件同级的 stats/ 目录，测试的临时目录也因此互不干扰。
	usageStore, err := usage.Open(filepath.Join(filepath.Dir(store.Path()), "stats", "usage.json"))
	if err != nil {
		slog.Warn("用量统计加载失败，本次运行不落盘", "error", err)
		usageStore = usage.NewStore()
	}
	return &Server{
		store:      store,
		index:      index,
		client:     &http.Client{Transport: transport},
		usageStore: usageStore,
		traces:     trace.NewStore(),
	}
}

// Usage 返回用量统计存储，供管理面查询。
func (s *Server) Usage() *usage.Store { return s.usageStore }

// Traces 返回入站请求轨迹存储，供管理面查询。
func (s *Server) Traces() *trace.Store { return s.traces }

// Close 落盘用量统计，进程退出前调用。
func (s *Server) Close() error { return s.usageStore.Close() }

func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Server) Addr() string {
	cfg := s.store.Snapshot()
	return net.JoinHostPort(cfg.Settings.Proxy.Host, strconv.Itoa(cfg.Settings.Proxy.Port))
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	cfg := s.store.Snapshot()
	addr := net.JoinHostPort(cfg.Settings.Proxy.Host, strconv.Itoa(cfg.Settings.Proxy.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("代理端口 %s 无法监听：%w", addr, err)
	}
	mux := s.mux()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 20 * time.Second}
	s.srv, s.ln, s.running = srv, ln, true
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("代理服务退出", "error", err)
		}
	}()
	slog.Info("代理已启动", "addr", "http://"+addr)
	return nil
}

// mux 构建代理的全部路由（测试也可直接使用）。
func (s *Server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		s.handleProxy(w, r, protocol.Chat)
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		s.handleProxy(w, r, protocol.Responses)
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		s.handleProxy(w, r, protocol.Messages)
	})
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
	})
	return mux
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.srv, s.ln, s.running = nil, nil, false
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	err := srv.Shutdown(ctx)
	slog.Info("代理已停止")
	return err
}

func (s *Server) Restart(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil {
		return err
	}
	return s.Start()
}

func (s *Server) Recent(limit int) []Record {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	n := len(s.records)
	if limit > n {
		limit = n
	}
	out := make([]Record, 0, limit)
	for i := n - limit; i < n; i++ {
		out = append(out, s.records[i])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Server) RecentErrors(limit int) []Record {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	out := []Record{}
	for i := len(s.records) - 1; i >= 0 && len(out) < limit; i-- {
		if s.records[i].Error != "" {
			out = append(out, s.records[i])
		}
	}
	return out
}

func (s *Server) log(r Record) {
	s.recMu.Lock()
	s.records = append(s.records, r)
	if len(s.records) > 200 {
		s.records = s.records[len(s.records)-200:]
	}
	s.recMu.Unlock()
	lvl := slog.LevelInfo
	if r.Error != "" {
		lvl = slog.LevelWarn
	}
	slog.Log(context.Background(), lvl, "代理请求",
		"entry", r.Entry, "source", r.Source, "model", r.RequestModel, "provider", r.Provider, "target", r.Model,
		"upstream", r.Upstream, "converted", r.Converted, "status", r.Status, "ms", r.DurationMS,
		"error", r.Error, "warnings", strings.Join(r.Warnings, "; "))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	type item struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	data := []item{}
	for _, a := range cfg.AvailableModels() {
		data = append(data, item{ID: a.Slug, Object: "model", OwnedBy: a.Provider.Name})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request, entry protocol.Protocol) {
	start := time.Now()
	rec := Record{Time: start, Entry: string(entry), Status: 200, Source: sourceOf(r)}

	// 仅接受回环来源
	if !isLoopback(r.RemoteAddr) {
		rec.Status, rec.Error = 403, "非本机来源"
		s.log(rec)
		protocol.WriteError(w, entry, 403, "仅允许本机访问")
		return
	}
	if r.Method != http.MethodPost {
		rec.Status, rec.Error = 405, "方法不支持"
		s.log(rec)
		protocol.WriteError(w, entry, 405, "仅支持 POST")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		rec.Status, rec.Error = 400, "读取请求体失败"
		s.log(rec)
		protocol.WriteError(w, entry, 400, "读取请求体失败: "+err.Error())
		return
	}
	var head struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &head)
	rec.RequestModel = head.Model

	if strings.TrimSpace(head.Model) == "" {
		rec.Status, rec.Error = 400, "缺少模型名称"
		s.log(rec)
		protocol.WriteError(w, entry, 400, "缺少模型名称")
		return
	}
	idx := s.index.Load()
	match, err := idx.Resolve(head.Model)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrModelNotFound) {
			status = http.StatusNotFound
		}
		rec.Status, rec.Error = status, err.Error()
		s.log(rec)
		protocol.WriteError(w, entry, status, err.Error())
		return
	}
	rec.Provider = match.Provider.Name
	rec.Model = match.Model.ID
	rec.Session = sessionID(r, body, match.Model.ID)

	// 统一记录到达供应商之后的请求结果。网络失败、超时、上游错误和成功
	// 都必须走这里，否则失败率会被低估。
	recorded := false
	recordResult := func(out *protocol.Outcome, outcome string) {
		if recorded {
			return
		}
		recorded = true
		rec.DurationMS = time.Since(start).Milliseconds()
		ok := outcome == usage.OutcomeSuccess && rec.Status >= 200 && rec.Status < 300 && rec.Error == ""
		entry := usage.Entry{
			Time: rec.Time, ProviderID: match.Provider.ID, ProviderName: match.Provider.Name,
			ModelID: match.Model.ID, ModelName: match.Model.Name, Caller: rec.Source,
			OK: ok, Outcome: outcome, HasUsage: out != nil && out.Usage != nil,
		}
		if out != nil && out.Usage != nil {
			entry.Input, entry.Output, entry.Total = out.Usage.Input, out.Usage.Output, out.Usage.Total
			entry.Cached, entry.CacheWrite, entry.Reasoning = out.Usage.Cached, out.Usage.CacheWrite, out.Usage.Reasoning
		}
		if out != nil && out.Timing.Streaming && !out.Timing.FirstOutputAt.IsZero() && !out.Timing.LastOutputAt.IsZero() {
			entry.HasTiming = true
			entry.TTFTMS = out.Timing.FirstOutputAt.Sub(start).Milliseconds()
			entry.DecodeMS = out.Timing.LastOutputAt.Sub(out.Timing.FirstOutputAt).Milliseconds()
		}
		s.usageStore.Add(entry)
	}

	// 轨迹采集：旁路复制原始入站请求体，点开即可回看完整执行链路。不改变转发行为。
	traceID := 0
	if idx.TraceEnabled {
		traceID = s.traces.Add(trace.Entry{
			Time: start, Source: rec.Source, Session: rec.Session, Protocol: string(entry),
			RequestModel: head.Model, Provider: match.Provider.Name, Model: match.Model.ID,
			Bytes: len(body), Body: append([]byte(nil), body...),
			Method: r.Method, URL: "http://" + r.Host + r.URL.RequestURI(), Proto: r.Proto,
			Headers: captureHeaders(r.Header),
		})
		defer func() { s.traces.Complete(traceID, rec.Status, time.Since(start).Milliseconds()) }()
	}

	up, err := match.UpstreamProtocol(entry)
	if err != nil {
		rec.Status, rec.Error = 400, err.Error()
		s.log(rec)
		protocol.WriteError(w, entry, 400, err.Error())
		return
	}
	rec.Upstream = string(up)
	rec.Converted = up != entry

	// 兼容扩展：内置自动匹配 + 供应商显式声明。供应商怪癖集中在这里，不散在核心路径。
	extUpstream := compat.Upstream{
		ProviderID: match.Provider.ID, Preset: match.Provider.Preset,
		BaseURL: match.Provider.BaseURL, ModelID: match.Model.ID,
	}
	plan := compat.New(extUpstream, match.Provider.Extensions)
	var planWarnings []string
	if unknown := plan.Unknown(); len(unknown) > 0 {
		planWarnings = append(planWarnings, fmt.Sprintf("未知的兼容扩展 %s（已忽略）", strings.Join(unknown, "、")))
	}

	// 请求侧转换：解码客户端请求 → IR → 编码上游请求。
	// 转换只做协议本身的事：供应商怪癖交给兼容扩展（下方 plan.Apply）。
	relay, upstreamBody, err := protocol.Prepare(entry, up, body, protocol.Identity{
		ProviderID: match.Provider.ID,
		ModelID:    match.Model.ID,
	})
	if err != nil {
		rec.Status, rec.Error = 400, err.Error()
		s.log(rec)
		protocol.WriteError(w, entry, 400, err.Error())
		return
	}

	url := upstream.URL(match.Provider.BaseURL, up.UpstreamPath())
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(upstreamBody))
	if err != nil {
		rec.Status, rec.Error = 500, err.Error()
		s.log(rec)
		protocol.WriteError(w, entry, 500, "构建上游请求失败")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if head.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	// 自我标识（OpenCode Go 要求自定义 UA，而非通用 HTTP 库名）
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	upstream.SetAuth(req, match.Provider, up)
	// 兼容扩展：在认证之后、发出之前做各自的事（补请求头、补请求体字段等）。
	extReq := &compat.Request{
		Upstream:         extUpstream,
		UpstreamProtocol: up,
		HTTP:             req,
		Body:             upstreamBody,
		Session:          rec.Session,
		LookupReasoning:  relay.LookupReasoning,
		Warn:             func(format string, args ...any) { planWarnings = append(planWarnings, fmt.Sprintf(format, args...)) },
	}
	if err := plan.Apply(extReq); err != nil {
		rec.Status, rec.Error = 500, "兼容扩展处理失败: "+err.Error()
		s.log(rec)
		protocol.WriteError(w, entry, 500, "兼容扩展处理失败")
		return
	}
	if !bytes.Equal(extReq.Body, upstreamBody) {
		patched := append([]byte(nil), extReq.Body...)
		req.Body = io.NopCloser(bytes.NewReader(patched))
		req.ContentLength = int64(len(patched))
		// GetBody 也必须指向改写后的正文；重定向或连接重试会用它重放请求，
		// 否则兼容扩展补的 reasoning_content 等字段会静默丢失。
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(patched)), nil
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		status := http.StatusBadGateway
		msg := "上游请求失败: " + err.Error()
		if errors.Is(err, context.Canceled) {
			status, msg = 499, "客户端已取消"
		} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
			status, msg = http.StatusGatewayTimeout, "上游超时"
		}
		rec.Status, rec.Error = status, msg
		recordResult(nil, classifyFailure(status, err))
		s.log(rec)
		if status != 499 {
			protocol.WriteError(w, entry, status, msg)
		}
		return
	}
	defer resp.Body.Close()
	rec.Status = resp.StatusCode

	// 原始响应旁路采集：TeeReader 只观察代理实际读到的字节，不改变客户端收到的响应。
	var responseBody io.Reader = resp.Body
	var captured *responseCapture
	if traceID != 0 {
		captured = &responseCapture{limit: maxBodyBytes}
		responseBody = io.TeeReader(resp.Body, captured)
	}

	// 响应侧转换：上游错误体、流式事件、非流式响应与直通都在转换模块里收口。
	out, derr := relay.Deliver(resp.StatusCode, resp.Header, responseBody, w)
	if traceID != 0 && captured != nil {
		s.traces.SetResponse(traceID, trace.Response{
			Status: resp.StatusCode, Proto: resp.Proto, Bytes: captured.seen,
			Truncated: captured.truncated, Headers: captureHeaders(resp.Header),
			Body: captured.body.Bytes(),
		})
	}
	rec.Warnings = append(out.Warnings, planWarnings...)
	if out.DownstreamStatus != 0 {
		rec.Status = out.DownstreamStatus
	}
	if out.UpstreamBody != nil {
		rec.Error = fmt.Sprintf("上游 HTTP %d: %s", out.UpstreamStatus, upstreamErrorSummary(out.UpstreamBody, resp))
	}
	if derr != nil && !errors.Is(derr, context.Canceled) {
		rec.Error = "流式转换中断: " + derr.Error()
		slog.Warn("流式转换中断", "error", derr)
	}
	outcome := usage.OutcomeSuccess
	if derr != nil {
		outcome = classifyFailure(rec.Status, derr)
	} else if out.UpstreamStatus >= 400 {
		outcome = classifyFailure(out.UpstreamStatus, nil)
	}
	recordResult(&out, outcome)
	s.log(rec)
}

// ---- 上游错误摘要 ----

// requestIDHeaders 是上游常见的请求 ID 响应头，用于把错误关联到供应商侧。
var requestIDHeaders = []string{"x-request-id", "request-id", "x-ds-request-id", "x-opencode-request-id", "x-trace-id"}

func upstreamRequestID(resp *http.Response) string {
	for _, h := range requestIDHeaders {
		if v := strings.TrimSpace(resp.Header.Get(h)); v != "" {
			return v
		}
	}
	return ""
}

// upstreamErrorSummary 提取上游错误体的可读摘要（含 request id），便于排查。
// 以前这里只记「上游 HTTP 400」，定位问题必须回捞 Codex 会话，代价很高。
func upstreamErrorSummary(body []byte, resp *http.Response) string {
	msg := strings.TrimSpace(string(body))
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		switch {
		case parsed.Error != nil && parsed.Error.Message != "":
			msg = parsed.Error.Message
		case parsed.Message != "":
			msg = parsed.Message
		}
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if id := upstreamRequestID(resp); id != "" {
		msg += "（request id: " + id + "）"
	}
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return msg
}

// ---- 辅助 ----

// sessionHeaderCandidates 是各客户端常见的会话头；命中的第一个会映射为 x-opencode-session。
// 例如 Codex 会发送 session_id（见 codex-api 的 build_session_headers）。
var sessionHeaderCandidates = []string{
	"x-opencode-session",
	"session_id",
	"session-id",
	"x-session-id",
	"x-codex-session-id",
	"conversation_id",
	"conversation-id",
	"x-conversation-id",
	"thread_id",
	"x-thread-id",
}

// sessionID 返回该请求的稳定会话标识：优先使用客户端会话头，
// 否则用「模型 + 首条用户消息」生成（同一会话保持稳定，便于上游路由与缓存）。
func sessionID(r *http.Request, body []byte, model string) string {
	for _, h := range sessionHeaderCandidates {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	if text := firstUserText(body); text != "" {
		sum := sha1.Sum([]byte(model + "\x00" + text))
		return "ls-" + hex.EncodeToString(sum[:8])
	}
	return ""
}

func firstUserText(body []byte) string {
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if raw, ok := payload["messages"]; ok {
		var msgs []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &msgs) == nil {
			for _, m := range msgs {
				if m.Role == "user" {
					return extractText(m.Content)
				}
			}
		}
	}
	if raw, ok := payload["input"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var items []struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &items) == nil {
			for _, it := range items {
				if it.Type == "message" && it.Role == "user" {
					return extractText(it.Content)
				}
			}
		}
	}
	return ""
}

func extractText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		for _, p := range parts {
			if p.Text != "" {
				return p.Text
			}
		}
	}
	return ""
}

// captureHeaders 复制请求头并按名称排序，供「原始请求」视图展示；认证类头的值会被脱敏。
func captureHeaders(h http.Header) []trace.Header {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]trace.Header, 0, len(names))
	for _, k := range names {
		for _, v := range h.Values(k) {
			if sensitiveHeader(k) {
				v = "***"
			}
			out = append(out, trace.Header{Name: k, Value: v})
		}
	}
	return out
}

// responseCapture 旁路保存最多 limit 字节的响应体，同时统计完整字节数。
type responseCapture struct {
	body      bytes.Buffer
	limit     int
	seen      int
	truncated bool
}

func (c *responseCapture) Write(p []byte) (int, error) {
	c.seen += len(p)
	if remain := c.limit - c.body.Len(); remain > 0 {
		if len(p) > remain {
			c.body.Write(p[:remain])
			c.truncated = true
		} else {
			c.body.Write(p)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

func sensitiveHeader(name string) bool {
	l := strings.ToLower(name)
	switch {
	case l == "authorization", l == "proxy-authorization", l == "cookie", l == "set-cookie":
		return true
	case strings.Contains(l, "api-key"), strings.Contains(l, "apikey"), strings.Contains(l, "token"):
		return true
	}
	return false
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
