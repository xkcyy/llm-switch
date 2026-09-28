// Package upstream 负责「发往供应商的上游请求」的装配：地址拼接、认证头与
// 协议固有请求头。连接测试（internal/provider）与代理转发（internal/proxy）
// 共用同一份实现，避免两处各写一份后各自漂移。
//
// 边界：这里只做协议本身要求的事（认证、Messages 的 anthropic-version）。
// 供应商特有的怪癖属于兼容扩展，见 internal/proxy/protocol/compat。
package upstream

import (
	"net/http"
	"strings"

	"llm-switch/internal/config"
	"llm-switch/internal/proxy/protocol"
)

// URL 拼接 base_url 与路径片段，容忍两端的斜杠。
func URL(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

// SetAuth 按供应商配置挂认证头，并补协议固有请求头（Messages 的 anthropic-version）。
// 调用方在认证之后、发出之前再跑兼容扩展（扩展可以覆盖这里的结果）。
func SetAuth(req *http.Request, p config.Provider, proto protocol.Protocol) {
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
