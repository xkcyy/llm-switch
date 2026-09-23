package proxy

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// reasoningPlaceholder 是思考内容缓存未命中时的回填文本。
// 上游只校验字段存在与（部分网关要求的）非空，不校验内容本身；
// 用固定占位而不是回放其他轮次的推理，避免把过期推理注入上下文。
const reasoningPlaceholder = "(reasoning omitted)"

// reasoningCache 缓存上游思考内容。
// DeepSeek 等思考型上游要求把上一轮的 reasoning_content 在下一轮带 tool_calls
// 的 assistant 消息里回传，而 Responses 协议没有等价字段，
// 因此在代理侧按「供应商 + 模型 + 工具调用 ID」暂存。
//
// 实测规则：每个带 tool_calls 的 assistant 消息都必须带该字段（缺失即 400），
// 且部分网关把空串视为未回传，所以缓存未命中时由调用方回填非空占位文本。
type reasoningCache struct {
	mu      sync.Mutex
	entries map[string]reasoningEntry
	seq     uint64
	bytes   int
	ttl     time.Duration
	maxN    int
	maxByte int
}

type reasoningEntry struct {
	text string
	at   time.Time
	seq  uint64
}

const (
	// reasoningTTL 覆盖长会话（Codex 单次会话常超过 1 小时）。
	reasoningTTL = 6 * time.Hour
	// 单条上限与总量上限，避免超长思考内容把内存撑爆。
	reasoningMaxEntryBytes = 64 << 10
	reasoningMaxTotalBytes = 32 << 20
	reasoningMaxEntries    = 4096
)

func newReasoningCache() *reasoningCache {
	return &reasoningCache{
		entries: map[string]reasoningEntry{},
		ttl:     reasoningTTL,
		maxN:    reasoningMaxEntries,
		maxByte: reasoningMaxTotalBytes,
	}
}

func reasoningKey(providerID, modelID, callID string) string {
	return providerID + "\x00" + modelID + "\x00" + callID
}

// store 记录一次响应中所有工具调用共享的思考内容（同一轮回复的推理内容相同）。
func (c *reasoningCache) store(providerID, modelID string, callIDs []string, text string) {
	text = strings.TrimSpace(text)
	if text == "" || len(callIDs) == 0 {
		return
	}
	if len(text) > reasoningMaxEntryBytes {
		text = text[:reasoningMaxEntryBytes]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for _, id := range callIDs {
		if id == "" {
			continue
		}
		key := reasoningKey(providerID, modelID, id)
		if old, ok := c.entries[key]; ok {
			c.bytes -= len(old.text)
		}
		c.seq++
		c.entries[key] = reasoningEntry{text: text, at: now, seq: c.seq}
		c.bytes += len(text)
	}
	if len(c.entries) > c.maxN || c.bytes > c.maxByte {
		c.pruneLocked(now)
	}
}

func (c *reasoningCache) get(providerID, modelID, callID string) (string, bool) {
	if callID == "" {
		return "", false
	}
	key := reasoningKey(providerID, modelID, callID)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if time.Since(e.at) > c.ttl {
		delete(c.entries, key)
		c.bytes -= len(e.text)
		return "", false
	}
	return e.text, true
}

// pruneLocked 先清理过期条目，仍超限时按写入顺序淘汰最旧的一半。
func (c *reasoningCache) pruneLocked(now time.Time) {
	for k, e := range c.entries {
		if now.Sub(e.at) > c.ttl {
			delete(c.entries, k)
			c.bytes -= len(e.text)
		}
	}
	if len(c.entries) <= c.maxN && c.bytes <= c.maxByte {
		return
	}
	keys := make([]string, 0, len(c.entries))
	for k := range c.entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return c.entries[keys[i]].seq < c.entries[keys[j]].seq })
	target := len(c.entries) / 2
	for _, k := range keys {
		if len(c.entries) <= target && c.bytes <= c.maxByte/2 {
			break
		}
		c.bytes -= len(c.entries[k].text)
		delete(c.entries, k)
	}
}

// needsReasoningEcho 判断该上游是否属于「思考模式必须回传 reasoning_content」的类型。
// 命中缓存时无需判断；这里用于冷缓存（代理刚重启、上一轮未产出推理等）时仍能
// 构造合法请求：字段存在且非空，上游不会校验内容本身。
func needsReasoningEcho(m Match) bool {
	switch m.Provider.Preset {
	case "deepseek", "opencode-go":
		return true
	}
	base := strings.ToLower(m.Provider.BaseURL)
	return strings.Contains(base, "deepseek") || strings.Contains(base, "opencode.ai")
}
