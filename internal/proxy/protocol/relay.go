package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func nowUnix() int64     { return time.Now().Unix() }
func nowUnixNano() int64 { return time.Now().UnixNano() }

// maxResponseBytes 与代理侧的请求体上限一致。
const maxResponseBytes = 32 << 20

// Identity 是转换所需的上游身份：思考内容缓存的键。
// 转换模块不依赖配置层，因此只接受这两个字段。
type Identity struct {
	ProviderID string
	ModelID    string
}

// Select 选择本次请求使用的上游协议（纯函数）：
// 优先与入口协议一致（直通，保真度最高），否则按候选顺序取第一个可转换的协议。
// pinned 非空时只使用该协议（多端点网关的个别模型需要固定）。
func Select(entry Protocol, candidates []string, pinned string) (Protocol, error) {
	list := candidates
	if strings.TrimSpace(pinned) != "" {
		list = []string{pinned}
	}
	for _, c := range list {
		if p, err := Parse(c); err == nil && p == entry {
			return p, nil
		}
	}
	for _, c := range list {
		p, err := Parse(c)
		if err != nil {
			continue
		}
		if CanConvert(entry, p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("协议不兼容：入口 %s，上游可用协议 %s", entry, strings.Join(list, "、"))
}

// Relay 是一次代理请求的转换句柄：请求侧由 Prepare 完成，响应侧由 Deliver 完成。
// 上游 HTTP 调用、认证与记录留在调用方（见设计文档 §9.1）。
type Relay struct {
	entry, up   Protocol
	stream      bool
	legacy      bool // Messages 上游暂用成对函数，见设计文档 §9.2 的迁移计划
	id          Identity
	customTools map[string]bool
	warnings    []string

	// echoed 是客户端这次回传的思考内容：调用 ID → 推理文本。
	// 由 Responses 的 reasoning 条目按「条目在前、调用在后」关联到工具调用。
	echoed map[string]string

	// 流式思考内容累积（仅 Responses 入口 + Chat 上游）
	captureReasoning bool
	reasoning        strings.Builder
	callIDs          []string
}

// LookupReasoning 返回某个工具调用对应的真实思考内容：先查代理侧缓存
// （同一轮刚拿到的），再查客户端本次回传的 reasoning 条目。
// 拿不到时 ok=false，由兼容扩展决定兜底策略。
func (r *Relay) LookupReasoning(callID string) (string, bool) {
	if callID == "" {
		return "", false
	}
	if text, ok := reasoningGet(r.id.ProviderID, r.id.ModelID, callID); ok && text != "" {
		return text, true
	}
	if text, ok := r.echoed[callID]; ok && text != "" {
		return text, true
	}
	return "", false
}

// Outcome 是响应侧转换的可观测结果。
type Outcome struct {
	Warnings []string
	// UpstreamBody 仅在上游返回 >= 400 时填充（已限长），供调用方记录错误摘要。
	UpstreamStatus int
	UpstreamBody   []byte
}

// encodeContext 是编码器可用的请求侧上下文：上游身份、自定义工具名、告警出口。
type encodeContext struct {
	id          Identity
	customTools map[string]bool
	relay       *Relay
}

func (c *encodeContext) warn(format string, args ...any) {
	c.relay.warnings = append(c.relay.warnings, fmt.Sprintf(format, args...))
}

// Prepare 完成请求侧转换：解码客户端请求 → IR → 编码上游请求。
// 入口与上游协议相同时只替换 model，原样转发（不经过 IR）。
func Prepare(entry, up Protocol, body []byte, id Identity) (*Relay, []byte, error) {
	r := &Relay{entry: entry, up: up, id: id, stream: streamFlagOf(body)}
	if entry == up {
		out, err := PatchModel(body, id.ModelID)
		if err != nil {
			return nil, nil, err
		}
		return r, out, nil
	}
	if up == Messages {
		// Messages 上游暂用成对函数，见设计文档 §9.2 的迁移计划。
		r.legacy = true
		var (
			out []byte
			err error
		)
		if entry == Responses {
			out, err = ResponsesToMessagesRequest(body, id.ModelID)
		} else {
			out, err = ChatToMessagesRequest(body, id.ModelID)
		}
		if err != nil {
			return nil, nil, err
		}
		return r, out, nil
	}

	req, err := decodeRequest(entry, body)
	if err != nil {
		return nil, nil, err
	}
	r.customTools = req.customToolNames()
	r.captureReasoning = entry == Responses && up == Chat
	r.echoed = associateReasoning(req)
	out, err := encodeRequest(up, req, id.ModelID, r.context())
	if err != nil {
		return nil, nil, err
	}
	return r, out, nil
}

// Deliver 完成响应侧转换并把结果写给客户端：
// 上游错误体、流式事件、非流式响应与直通都在这里收口。
func (r *Relay) Deliver(status int, header http.Header, body io.Reader, w http.ResponseWriter) (Outcome, error) {
	if status >= 400 {
		b, _ := io.ReadAll(io.LimitReader(body, 64<<10))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(ConvertErrorBody(r.entry, status, b))
		out := r.outcome()
		out.UpstreamStatus, out.UpstreamBody = status, b
		return out, nil
	}
	streaming := r.stream && strings.Contains(header.Get("Content-Type"), "event-stream")
	if streaming {
		setSSEHeaders(w)
		w.WriteHeader(status)
		flush := flushFunc(w)
		if r.legacy {
			err := legacyStream(r.entry, body, w, flush)
			return r.outcome(), err
		}
		if r.entry == r.up {
			return r.outcome(), passthroughStream(body, w, flush)
		}
		enc := newStreamEncoder(r, w, flush)
		err := decodeStream(r.up, body, r.capture(enc.Emit))
		r.finishReasoning()
		if err != nil {
			return r.outcome(), err
		}
		return r.outcome(), enc.Close()
	}

	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes))
	if err != nil {
		WriteError(w, r.entry, http.StatusBadGateway, "读取上游响应失败")
		return r.outcome(), err
	}
	if r.entry == r.up {
		writeContentType(w, header)
		w.WriteHeader(status)
		w.Write(raw)
		return r.outcome(), nil
	}
	if r.legacy {
		out, err := legacyResponse(r.entry, raw)
		if err != nil {
			WriteError(w, r.entry, http.StatusBadGateway, err.Error())
			return r.outcome(), err
		}
		writeContentType(w, header)
		w.WriteHeader(status)
		w.Write(out)
		return r.outcome(), nil
	}
	resp, err := decodeResponse(r.up, raw)
	if err != nil {
		WriteError(w, r.entry, http.StatusBadGateway, err.Error())
		return r.outcome(), err
	}
	r.storeReasoning(resp)
	out, err := encodeResponse(r.entry, resp, r.context())
	if err != nil {
		WriteError(w, r.entry, http.StatusBadGateway, err.Error())
		return r.outcome(), err
	}
	writeContentType(w, header)
	w.WriteHeader(status)
	w.Write(out)
	return r.outcome(), nil
}

func (r *Relay) context() *encodeContext {
	return &encodeContext{id: r.id, customTools: r.customTools, relay: r}
}

func (r *Relay) outcome() Outcome {
	return Outcome{Warnings: r.warnings}
}

// capture 包装事件回调，累积思考内容与工具调用 ID 供后续请求回填。
func (r *Relay) capture(next func(irEvent) error) func(irEvent) error {
	return func(ev irEvent) error {
		if r.captureReasoning {
			switch ev.Kind {
			case irReasoning:
				r.reasoning.WriteString(ev.Text)
			case irCallStart:
				if ev.CallID != "" {
					r.callIDs = append(r.callIDs, ev.CallID)
				}
			}
		}
		return next(ev)
	}
}

// finishReasoning 落缓存。流被中断也要调用：缓存能救下一轮的请求。
func (r *Relay) finishReasoning() {
	if !r.captureReasoning || r.reasoning.Len() == 0 || len(r.callIDs) == 0 {
		return
	}
	reasoningStore(r.id.ProviderID, r.id.ModelID, r.callIDs, r.reasoning.String())
}

func (r *Relay) storeReasoning(resp *irResponse) {
	if !r.captureReasoning || resp.Reasoning == "" {
		return
	}
	ids := make([]string, 0, len(resp.Calls))
	for _, c := range resp.Calls {
		ids = append(ids, c.CallID)
	}
	reasoningStore(r.id.ProviderID, r.id.ModelID, ids, resp.Reasoning)
}

// ---- 按协议分发 ----

// associateReasoning 把客户端回传的 reasoning 条目关联到其后的工具调用：
// Responses 的条目顺序是「reasoning（可多个）→ function_call/custom_tool_call」，
// 同一轮的推理内容对该轮所有工具调用都适用。
func associateReasoning(req *irRequest) map[string]string {
	out := map[string]string{}
	pending := ""
	for _, it := range req.Items {
		switch it.Kind {
		case "reasoning":
			if it.Text != "" {
				pending = it.Text
			}
		case irFunctionCall, irCustomCall:
			if pending != "" && it.CallID != "" {
				out[it.CallID] = pending
			}
		default:
			// 工具结果出现即认为该轮推理已消费完（正文消息夹在推理与调用之间时仍适用）
			if it.Kind == irCallOutput || it.Kind == irCustomOutput {
				pending = ""
			}
		}
	}
	return out
}

func decodeRequest(p Protocol, body []byte) (*irRequest, error) {
	switch p {
	case Chat:
		return chatDecodeRequest(body)
	case Responses:
		return responsesDecodeRequest(body)
	}
	return nil, fmt.Errorf("协议不兼容：入口 %s 暂不支持转换", p)
}

func encodeRequest(p Protocol, req *irRequest, model string, ctx *encodeContext) ([]byte, error) {
	switch p {
	case Chat:
		return chatEncodeRequest(req, model, ctx)
	case Responses:
		return responsesEncodeRequest(req, model, ctx)
	}
	return nil, fmt.Errorf("协议不兼容：暂不支持转换为 %s 上游", p)
}

func decodeResponse(up Protocol, body []byte) (*irResponse, error) {
	switch up {
	case Chat:
		return chatDecodeResponse(body)
	case Responses:
		return responsesDecodeResponse(body)
	}
	return nil, fmt.Errorf("协议不兼容：暂不支持 %s 上游响应转换", up)
}

func encodeResponse(entry Protocol, resp *irResponse, ctx *encodeContext) ([]byte, error) {
	switch entry {
	case Chat:
		return chatEncodeResponse(resp, ctx)
	case Responses:
		return responsesEncodeResponse(resp, ctx)
	}
	return nil, fmt.Errorf("协议不兼容：暂不支持 %s 入口响应转换", entry)
}

type streamEncoder interface {
	Emit(irEvent) error
	Close() error
}

func newStreamEncoder(r *Relay, w io.Writer, flush func()) streamEncoder {
	if r.entry == Responses {
		return newResponsesStreamEncoder(w, flush, r.context())
	}
	return newChatStreamEncoder(w, flush)
}

func decodeStream(up Protocol, body io.Reader, emit func(irEvent) error) error {
	switch up {
	case Chat:
		return chatDecodeStream(body, emit)
	case Responses:
		return responsesDecodeStream(body, emit)
	}
	return fmt.Errorf("协议不兼容：暂不支持 %s 上游流式转换", up)
}

// ---- Messages 上游的成对函数桥（迁移中） ----

func legacyResponse(entry Protocol, body []byte) ([]byte, error) {
	if entry == Responses {
		return MessagesToResponsesResponse(body)
	}
	return MessagesToChatResponse(body)
}

func legacyStream(entry Protocol, body io.Reader, w io.Writer, flush func()) error {
	if entry == Responses {
		return StreamMessagesToResponses(body, w, flush)
	}
	return StreamMessagesToChat(body, w, flush)
}

// ---- 辅助 ----

func streamFlagOf(body []byte) bool {
	var head struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &head)
	return head.Stream
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeContentType(w http.ResponseWriter, header http.Header) {
	if ct := header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
}

func flushFunc(w http.ResponseWriter) func() {
	flusher, _ := w.(http.Flusher)
	return func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
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
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// WriteError 按入口协议写出错误体（Messages 与 OpenAI 形状不同）。
func WriteError(w http.ResponseWriter, entry Protocol, status int, message string) {
	var body []byte
	if entry == Messages {
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
