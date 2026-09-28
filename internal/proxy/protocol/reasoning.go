package protocol

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// reasoningCache 缓存上游思考内容，供兼容扩展在构造下一轮请求时取用真实值。
// DeepSeek 等思考型上游要求把上一轮的 reasoning_content 在下一轮带 tool_calls
// 的 assistant 消息里回传，而 Responses 协议没有等价字段（见 ADR-0001），
// 因此在转换模块内部按「供应商 + 模型 + 工具调用 ID」暂存。
//
// 实测（对 opencode.ai/zen）：该字段只要缺失就报
// 「The reasoning_content in the thinking mode must be passed back to the API」，
// 回填非空占位可通过；但这只是兜底，真实值优先。
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

// 进程内单例：转换模块自己持有这份状态，调用方不再传缓存句柄。
var reasoningState = newReasoningCache()

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

func reasoningStore(providerID, modelID string, callIDs []string, text string) {
	reasoningState.store(providerID, modelID, callIDs, text)
}

func reasoningGet(providerID, modelID, callID string) (string, bool) {
	return reasoningState.get(providerID, modelID, callID)
}
