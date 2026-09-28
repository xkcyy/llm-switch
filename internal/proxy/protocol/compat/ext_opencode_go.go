package compat

import "strings"

func init() { Register(opencodeGo{}) }

// opencodeGo：OpenCode Go（opencode.ai/zen）要求的供应商特有行为。
// 当前只有一条：会话标识头，网关靠它做路由与提示缓存亲和，
// 缺失时直接把请求判为 MissingSessionID。后续该供应商的怪癖也放这里。
type opencodeGo struct{}

func (opencodeGo) Name() string { return "opencode-go" }

func (opencodeGo) Match(u Upstream) bool {
	return u.Preset == "opencode-go" || strings.Contains(strings.ToLower(u.BaseURL), "opencode.ai")
}

// BeforeRequest 把本次会话标识写进上游请求头。
func (opencodeGo) BeforeRequest(r *Request) error {
	if r.Session == "" || r.HTTP == nil {
		return nil
	}
	r.HTTP.Header.Set(SessionHeaderDefault, r.Session)
	return nil
}
