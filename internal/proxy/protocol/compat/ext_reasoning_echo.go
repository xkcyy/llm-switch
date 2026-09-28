package compat

import (
	"encoding/json"
	"strings"

	"llm-switch/internal/proxy/protocol"
)

func init() { Register(reasoningEcho{}) }

// reasoningPlaceholder 是思考内容拿不到时的回填文本。
// 上游只校验字段存在与（部分网关要求的）非空，不校验内容本身；
// 用固定占位而不是回放其他轮次的推理，避免把过期推理注入上下文。
const reasoningPlaceholder = "(reasoning omitted)"

// reasoningEcho：思考型上游（DeepSeek 等）在多轮工具调用中要求
// 带 tool_calls 的 assistant 消息回传非空 reasoning_content，缺失即 400。
// 该要求不属于 Responses 协议本身（Responses 用 reasoning 条目承载思考内容），
// 因此由本扩展在发往 Chat 上游的请求体上补齐字段。
type reasoningEcho struct{}

func (reasoningEcho) Name() string { return "reasoning-echo" }

func (reasoningEcho) Match(u Upstream) bool {
	switch u.Preset {
	case "deepseek", "opencode-go":
		return true
	}
	base := strings.ToLower(u.BaseURL)
	return strings.Contains(base, "deepseek") || strings.Contains(base, "opencode.ai")
}

// BeforeRequest 给每条带 tool_calls 的 assistant 消息补上非空 reasoning_content。
// 优先用真实思考内容（代理侧缓存或客户端回传），拿不到才回填占位并告警。
func (reasoningEcho) BeforeRequest(r *Request) error {
	if r.UpstreamProtocol != protocol.Chat {
		return nil // 只有 Chat 上游体有这个字段
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(r.Body, &doc); err != nil {
		return nil // 不是 JSON 对象就不动它
	}
	rawMsgs, ok := doc["messages"]
	if !ok {
		return nil
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil {
		return nil
	}

	changed, placeholderUsed := false, false
	for _, m := range msgs {
		if !hasToolCalls(m) {
			continue
		}
		if s, ok := stringField(m, "reasoning_content"); ok && strings.TrimSpace(s) != "" {
			continue
		}
		text := ""
		if r.LookupReasoning != nil {
			for _, id := range toolCallIDs(m) {
				if t, ok := r.LookupReasoning(id); ok && t != "" {
					text = t
					break
				}
			}
		}
		if text == "" {
			text = reasoningPlaceholder
			placeholderUsed = true
		}
		b, err := json.Marshal(text)
		if err != nil {
			return err
		}
		m["reasoning_content"] = b
		changed = true
	}
	if placeholderUsed {
		r.Warnf("思考内容缓存与客户端回传均未命中，已回填占位文本（不影响请求合法性）")
	}
	if !changed {
		return nil
	}
	patched, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	doc["messages"] = patched
	out, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	r.Body = out
	return nil
}

// hasToolCalls 判断该消息是「带工具调用的 assistant 消息」。
func hasToolCalls(m map[string]json.RawMessage) bool {
	if s, ok := stringField(m, "role"); !ok || s != "assistant" {
		return false
	}
	return len(toolCallIDs(m)) > 0
}

func toolCallIDs(m map[string]json.RawMessage) []string {
	raw, ok := m["tool_calls"]
	if !ok {
		return nil
	}
	var calls []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil
	}
	ids := make([]string, 0, len(calls))
	for _, c := range calls {
		ids = append(ids, c.ID)
	}
	return ids
}

func stringField(m map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := m[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}
