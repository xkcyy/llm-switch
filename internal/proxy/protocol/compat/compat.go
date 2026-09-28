package compat

import (
	"net/http"
	"strings"

	"llm-switch/internal/proxy/protocol"
)

// SessionHeaderDefault 是会话标识头的默认名。
const SessionHeaderDefault = "x-opencode-session"

// Upstream 是扩展匹配与作用所需的中立上下文，不依赖配置层。
type Upstream struct {
	ProviderID string
	Preset     string
	BaseURL    string
	ModelID    string
}

// Request 是上游请求的可改写视图。扩展在时机点里读它、改它。
type Request struct {
	Upstream Upstream
	// UpstreamProtocol 是本次发往上游的协议，扩展据此判断请求体形状。
	UpstreamProtocol protocol.Protocol
	// HTTP 是即将发出的上游请求，扩展可改它的头。
	HTTP *http.Request
	// Body 是上游请求体，扩展可整体替换。
	Body []byte
	// Session 是本次请求的会话标识（已从客户端头或请求内容解析）。
	Session string
	// LookupReasoning 返回某个工具调用对应的真实思考内容（代理侧缓存或客户端回传）。
	// 拿不到时返回 ok=false，由扩展自己决定兜底策略。
	LookupReasoning func(callID string) (string, bool)
	// Warn 上报降级行为，进代理记录。
	Warn func(format string, args ...any)
}

// Warnf 便于扩展内部上报。
func (r *Request) Warnf(format string, args ...any) {
	if r.Warn != nil {
		r.Warn(format, args...)
	}
}

// Extension 是一条兼容扩展：一个名字 + 是否命中。
type Extension interface {
	Name() string
	Match(Upstream) bool
}

// 时机点由机制定义，干什么由扩展自己决定；扩展按需实现，
// 机制侧只按接口类型查找，不认识任何具体扩展。
// 新增一个时机点 = 新增一个接口 + 核心侧一个调用点，既有扩展不受影响。

// BeforeRequest 在上游请求发出前调用（认证之后，可覆盖认证结果）。
type BeforeRequest interface {
	BeforeRequest(*Request) error
}

// Plan 是本次请求命中的扩展集合。
type Plan struct {
	upstream Upstream
	exts     []Extension
	unknown  []string
}

// New 计算本次请求的扩展：内置目录里自动命中的 + 供应商显式声明的名字。
// 显式名字里不认识的会记入 Unknown，由调用方提示（不静默丢弃）。
func New(u Upstream, explicit []string) Plan {
	seen := map[string]bool{}
	var exts []Extension
	for _, e := range All() {
		if e.Match(u) {
			seen[e.Name()] = true
			exts = append(exts, e)
		}
	}
	var unknown []string
	for _, raw := range explicit {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		e, ok := ByName(name)
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		exts = append(exts, e)
	}
	return Plan{upstream: u, exts: exts, unknown: unknown}
}

// Active 返回本次命中的扩展名（含自动匹配与显式挂载）。
func (p Plan) Active() []string {
	out := make([]string, 0, len(p.exts))
	for _, e := range p.exts {
		out = append(out, e.Name())
	}
	return out
}

// Unknown 返回显式声明但不存在的扩展名。
func (p Plan) Unknown() []string { return append([]string(nil), p.unknown...) }

// Apply 按顺序执行各扩展在 BeforeRequest 时机的动作。
func (p Plan) Apply(r *Request) error {
	for _, e := range p.exts {
		h, ok := e.(BeforeRequest)
		if !ok {
			continue
		}
		if err := h.BeforeRequest(r); err != nil {
			return err
		}
	}
	return nil
}
