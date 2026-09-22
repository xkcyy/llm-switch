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
	"strconv"
	"strings"
	"sync"
	"time"

	"llm-switch/internal/buildinfo"
	"llm-switch/internal/config"
	"llm-switch/internal/proxy/protocol"
)

const maxBodyBytes = 32 << 20

// Record 是一次代理请求的日志记录。
type Record struct {
	Time         time.Time `json:"time"`
	Entry        string    `json:"entry"`
	Source       string    `json:"source,omitempty"`
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

// sourceOf 依据 User-Agent 粗略识别调用方（Codex / OpenCode / 其它），仅用于界面展示。
func sourceOf(r *http.Request) string {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	switch {
	case strings.Contains(ua, "opencode"):
		return "OpenCode"
	case strings.Contains(ua, "codex"):
		return "Codex"
	case ua == "":
		return "未知"
	default:
		return "其它"
	}
}

type Server struct {
	store  *config.Store
	index  *Holder
	client *http.Client

	reasoning *reasoningCache

	mu      sync.Mutex
	srv     *http.Server
	ln      net.Listener
	running bool

	recMu   sync.Mutex
	records []Record
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
	return &Server{
		store:     store,
		index:     index,
		client:    &http.Client{Transport: transport},
		reasoning: newReasoningCache(),
	}
}

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
	for _, m := range cfg.Models {
		if !m.Enabled {
			continue
		}
		p, ok := cfg.FindProvider(m.ProviderID)
		if !ok || !p.Enabled {
			continue
		}
		data = append(data, item{ID: p.ID + "/" + m.ID, Object: "model", OwnedBy: p.Name})
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
		writeProtocolError(w, entry, 403, "仅允许本机访问")
		return
	}
	if r.Method != http.MethodPost {
		rec.Status, rec.Error = 405, "方法不支持"
		s.log(rec)
		writeProtocolError(w, entry, 405, "仅支持 POST")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		rec.Status, rec.Error = 400, "读取请求体失败"
		s.log(rec)
		writeProtocolError(w, entry, 400, "读取请求体失败: "+err.Error())
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
		writeProtocolError(w, entry, 400, "缺少模型名称")
		return
	}
	match, err := s.index.Load().Resolve(head.Model)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrModelNotFound) {
			status = http.StatusNotFound
		}
		rec.Status, rec.Error = status, err.Error()
		s.log(rec)
		writeProtocolError(w, entry, status, err.Error())
		return
	}
	rec.Provider = match.Provider.Name
	rec.Model = match.Model.ID

	up, err := match.UpstreamProtocol(entry)
	if err != nil {
		rec.Status, rec.Error = 400, err.Error()
		s.log(rec)
		writeProtocolError(w, entry, 400, err.Error())
		return
	}
	rec.Upstream = string(up)
	rec.Converted = up != entry

	var customTools map[string]bool
	if entry == protocol.Responses && up != protocol.Responses {
		customTools = protocol.CustomToolNames(body)
	}
	chatOpts := protocol.ChatConvertOptions{
		OnWarning: func(msg string) { rec.Warnings = append(rec.Warnings, msg) },
	}
	if up == protocol.Chat {
		// 思考型上游（DeepSeek 等）要求把 reasoning_content 原样回传；
		// 冷缓存时按供应商类型补空值，保证请求合法。
		chatOpts.Reasoning = func(callID string) (string, bool) {
			if text, ok := s.reasoning.get(match.Provider.ID, match.Model.ID, callID); ok {
				return text, true
			}
			if needsReasoningEcho(match) {
				return "", true
			}
			return "", false
		}
	}

	upstreamBody, err := convertRequest(entry, up, body, match.Model.ID, chatOpts)
	if err != nil {
		rec.Status, rec.Error = 400, err.Error()
		s.log(rec)
		writeProtocolError(w, entry, 400, err.Error())
		return
	}

	url := joinURL(match.Provider.BaseURL, up.UpstreamPath())
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(upstreamBody))
	if err != nil {
		rec.Status, rec.Error = 500, err.Error()
		s.log(rec)
		writeProtocolError(w, entry, 500, "构建上游请求失败")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if head.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	// 自我标识（OpenCode Go 要求自定义 UA，而非通用 HTTP 库名）
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	// 会话标识：优先透传客户端已有的会话头，其次用「模型 + 首条用户消息」生成稳定值。
	// OpenCode Go 依赖 x-opencode-session 做路由与提示缓存亲和。
	if sid := sessionID(r, body, match.Model.ID); sid != "" {
		req.Header.Set("x-opencode-session", sid)
	}
	applyAuth(req, match.Provider, up)

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
		s.log(rec)
		if status != 499 {
			writeProtocolError(w, entry, status, msg)
		}
		return
	}
	defer resp.Body.Close()
	rec.Status = resp.StatusCode

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		rec.Error = fmt.Sprintf("上游 HTTP %d", resp.StatusCode)
		s.log(rec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(protocol.ConvertErrorBody(entry, resp.StatusCode, b))
		return
	}

	respOpts := protocol.ResponseConvertOptions{
		CustomTools: customTools,
		OnWarning:   func(msg string) { rec.Warnings = append(rec.Warnings, msg) },
	}
	if entry == protocol.Responses && up == protocol.Chat {
		respOpts.OnReasoning = func(reasoning string, callIDs []string) {
			s.reasoning.store(match.Provider.ID, match.Model.ID, callIDs, reasoning)
		}
	}

	streaming := head.Stream && strings.Contains(resp.Header.Get("Content-Type"), "event-stream")
	if streaming {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		flush := func() {
			if flusher != nil {
				flusher.Flush()
			}
		}
		var terr error
		if entry == up {
			terr = passthroughStream(resp.Body, w, flush)
		} else {
			terr = streamTransform(entry, up, resp.Body, w, flush, respOpts)
		}
		if terr != nil && !errors.Is(terr, context.Canceled) {
			rec.Error = "流式转换中断: " + terr.Error()
			slog.Warn("流式转换中断", "error", terr)
		}
		rec.DurationMS = time.Since(start).Milliseconds()
		s.log(rec)
		return
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		rec.Error = "读取上游响应失败"
		rec.DurationMS = time.Since(start).Milliseconds()
		s.log(rec)
		writeProtocolError(w, entry, http.StatusBadGateway, "读取上游响应失败")
		return
	}
	if entry != up {
		converted, err := convertResponse(entry, up, b, respOpts)
		if err != nil {
			rec.Error = err.Error()
			rec.DurationMS = time.Since(start).Milliseconds()
			s.log(rec)
			writeProtocolError(w, entry, 502, err.Error())
			return
		}
		b = converted
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(b)
	rec.DurationMS = time.Since(start).Milliseconds()
	s.log(rec)
}

// ---- 协议分发 ----

func convertRequest(entry, up protocol.Protocol, body []byte, model string, opts protocol.ChatConvertOptions) ([]byte, error) {
	if entry == up {
		return protocol.PatchModel(body, model)
	}
	switch {
	case entry == protocol.Responses && up == protocol.Chat:
		return protocol.ResponsesToChatRequestWith(body, model, opts)
	case entry == protocol.Responses && up == protocol.Messages:
		return protocol.ResponsesToMessagesRequest(body, model)
	case entry == protocol.Chat && up == protocol.Messages:
		return protocol.ChatToMessagesRequest(body, model)
	case entry == protocol.Chat && up == protocol.Responses:
		return protocol.ChatToResponsesRequest(body, model)
	}
	return nil, fmt.Errorf("协议不兼容：%s 入口暂不支持 %s 上游", entry, up)
}

func convertResponse(entry, up protocol.Protocol, body []byte, opts protocol.ResponseConvertOptions) ([]byte, error) {
	switch {
	case entry == protocol.Responses && up == protocol.Chat:
		return protocol.ChatToResponsesResponseWith(body, opts)
	case entry == protocol.Responses && up == protocol.Messages:
		return protocol.MessagesToResponsesResponse(body)
	case entry == protocol.Chat && up == protocol.Messages:
		return protocol.MessagesToChatResponse(body)
	case entry == protocol.Chat && up == protocol.Responses:
		return protocol.ResponsesToChatResponse(body)
	}
	return nil, fmt.Errorf("协议不兼容：暂不支持 %s 上游响应转换为 %s", up, entry)
}

func streamTransform(entry, up protocol.Protocol, r io.Reader, w io.Writer, flush func(), opts protocol.ResponseConvertOptions) error {
	switch {
	case entry == protocol.Responses && up == protocol.Chat:
		return protocol.StreamChatToResponsesWith(r, w, flush, opts)
	case entry == protocol.Responses && up == protocol.Messages:
		return protocol.StreamMessagesToResponses(r, w, flush)
	case entry == protocol.Chat && up == protocol.Responses:
		return protocol.StreamResponsesToChat(r, w, flush)
	case entry == protocol.Chat && up == protocol.Messages:
		return protocol.StreamMessagesToChat(r, w, flush)
	}
	return fmt.Errorf("协议不兼容：暂不支持 %s → %s 流式转换", up, entry)
}

func passthroughStream(r io.Reader, w io.Writer, flush func()) error {
	buf := make([]byte, 16<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
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

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func joinURL(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

func applyAuth(req *http.Request, p config.Provider, proto protocol.Protocol) {
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
	if proto == protocol.Messages && req.Header.Get("anthropic-version") == "" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
}

func writeProtocolError(w http.ResponseWriter, entry protocol.Protocol, status int, message string) {
	var body []byte
	if entry == protocol.Messages {
		t := "invalid_request_error"
		if status == http.StatusNotFound {
			t = "not_found_error"
		}
		body, _ = json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": t, "message": message}})
	} else {
		t := "invalid_request_error"
		if status >= 500 {
			t = "api_error"
		}
		body, _ = json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": t, "code": nil}})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}
