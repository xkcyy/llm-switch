package protocol

import (
	"bytes"
	"encoding/json"
	"io"
)

// CallUsage 是一次调用里上游上报的用量。字段已归一化，找不到的记 0。
type CallUsage struct {
	Input      int
	Output     int
	Total      int
	Cached     int
	CacheWrite int
	Reasoning  int
}

// ParseUsage 从上游响应体或流式事件中提取用量。
// 上游字段名不统一：输入是 input_tokens 或 prompt_tokens，输出是 output_tokens 或
// completion_tokens，缓存与推理字段的位置也各异；这里统一归一化。
// 找不到 usage 时返回 ok=false——上游不上报就记无用量，绝不估算。
func ParseUsage(body []byte) (CallUsage, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return CallUsage{}, false
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return CallUsage{}, false
	}
	u := findUsageObject(root)
	if u == nil {
		return CallUsage{}, false
	}

	out := CallUsage{
		Input:  firstInt(u, "input_tokens", "prompt_tokens"),
		Output: firstInt(u, "output_tokens", "completion_tokens"),
		Total:  firstInt(u, "total_tokens"),
	}
	if out.Total == 0 {
		out.Total = out.Input + out.Output
	}
	// 缓存与推理先看 *_details，再退回顶层同名字段；只取一处，避免重复累加。
	out.Cached = firstInt(asMap(u["input_tokens_details"]), "cached_tokens")
	if out.Cached == 0 {
		out.Cached = firstInt(asMap(u["prompt_tokens_details"]), "cached_tokens")
	}
	if out.Cached == 0 {
		out.Cached = firstInt(u, "cached_tokens")
	}
	out.CacheWrite = firstInt(asMap(u["input_tokens_details"]), "cache_write_tokens")
	if out.CacheWrite == 0 {
		out.CacheWrite = firstInt(u, "cache_write_tokens")
	}
	out.Reasoning = firstInt(asMap(u["output_tokens_details"]), "reasoning_tokens")
	if out.Reasoning == 0 {
		out.Reasoning = firstInt(asMap(u["completion_tokens_details"]), "reasoning_tokens")
	}
	return out, true
}

// usageScanner 旁路解析 SSE 流里的 usage，读到的字节原样返回（不改写转发内容）。
// 与转发在同一个 goroutine 上顺序执行，无需加锁。
type usageScanner struct {
	r     io.Reader
	buf   []byte
	usage CallUsage
	found bool
}

func (s *usageScanner) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.buf = append(s.buf, p[:n]...)
	}
	s.drain(err != nil)
	return n, err
}

func (s *usageScanner) drain(flush bool) {
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		s.handleLine(s.buf[:i])
		s.buf = s.buf[i+1:]
	}
	if flush && len(s.buf) > 0 {
		s.handleLine(s.buf)
		s.buf = nil
	}
}

func (s *usageScanner) handleLine(line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	if u, ok := ParseUsage(payload); ok {
		mergeUsage(&s.usage, u)
		s.found = true
	}
}

// mergeUsage 合并同一流里多个事件的用量，各字段取较大值：
// Anthropic 的 input_tokens 在 message_start、output_tokens 在 message_delta，必须合并。
func mergeUsage(dst *CallUsage, u CallUsage) {
	dst.Input = maxInt(dst.Input, u.Input)
	dst.Output = maxInt(dst.Output, u.Output)
	dst.Total = maxInt(dst.Total, u.Total)
	dst.Cached = maxInt(dst.Cached, u.Cached)
	dst.CacheWrite = maxInt(dst.CacheWrite, u.CacheWrite)
	dst.Reasoning = maxInt(dst.Reasoning, u.Reasoning)
}

func maxInt(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// findUsageObject 定位 usage 对象：非流式响应在顶层，
// Responses 流式事件在 response.usage，Anthropic message_start 在 message.usage。
func findUsageObject(root map[string]any) map[string]any {
	if u := asMap(root["usage"]); u != nil {
		return u
	}
	for _, k := range []string{"response", "message"} {
		if nested := asMap(root[k]); nested != nil {
			if u := asMap(nested["usage"]); u != nil {
				return u
			}
		}
	}
	return nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// firstInt 返回第一个存在且为数值的字段。
func firstInt(m map[string]any, keys ...string) int {
	if m == nil {
		return 0
	}
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int(v)
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return int(n)
			}
		case int:
			return v
		}
	}
	return 0
}
