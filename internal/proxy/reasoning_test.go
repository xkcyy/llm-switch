package proxy

import (
	"strings"
	"testing"
	"time"

	"llm-switch/internal/config"
)

func provider(preset, baseURL string) config.Provider {
	return config.Provider{Preset: preset, BaseURL: baseURL}
}

func TestReasoningCacheStoreGet(t *testing.T) {
	c := newReasoningCache()

	// 空白思考内容不入库
	c.store("p", "m", []string{"call_1"}, "   ")
	if _, ok := c.get("p", "m", "call_1"); ok {
		t.Fatal("空白思考内容不应入库")
	}
	// 空 call_id 跳过
	c.store("p", "m", []string{""}, "推理")
	if _, ok := c.get("p", "m", ""); ok {
		t.Fatal("空 call_id 不应入库")
	}
	// 正常命中，且按供应商/模型隔离
	c.store("p", "m", []string{"call_1", "call_2"}, "推理")
	for _, id := range []string{"call_1", "call_2"} {
		if v, ok := c.get("p", "m", id); !ok || v != "推理" {
			t.Fatalf("未命中 %s: %q %v", id, v, ok)
		}
	}
	if _, ok := c.get("p2", "m", "call_1"); ok {
		t.Fatal("不同供应商不应命中同一缓存")
	}
	if _, ok := c.get("p", "m2", "call_1"); ok {
		t.Fatal("不同模型不应命中同一缓存")
	}
}

func TestReasoningCacheTTLAndTruncate(t *testing.T) {
	c := newReasoningCache()
	c.store("p", "m", []string{"call_1"}, "推理")
	c.ttl = time.Nanosecond
	time.Sleep(2 * time.Millisecond)
	if _, ok := c.get("p", "m", "call_1"); ok {
		t.Fatal("过期条目应被清理")
	}

	long := strings.Repeat("x", reasoningMaxEntryBytes+128)
	c.store("p", "m", []string{"call_2"}, long)
	v, ok := c.get("p", "m", "call_2")
	if !ok || len(v) != reasoningMaxEntryBytes {
		t.Fatalf("超长思考内容应被截断: len=%d", len(v))
	}
}

func TestReasoningCacheEvictsWhenFull(t *testing.T) {
	c := newReasoningCache()
	c.maxN = 2
	c.store("p", "m", []string{"a"}, "A")
	c.store("p", "m", []string{"b"}, "B")
	c.store("p", "m", []string{"c"}, "C")
	if len(c.entries) > c.maxN {
		t.Fatalf("容量未收敛: %d", len(c.entries))
	}
	if _, ok := c.get("p", "m", "c"); !ok {
		t.Fatal("最新条目应保留")
	}
}

func TestNeedsReasoningEcho(t *testing.T) {
	cases := []struct {
		name string
		m    Match
		want bool
	}{
		{"deepseek 预置", Match{Provider: provider("deepseek", "https://api.deepseek.com")}, true},
		{"opencode-go 预置", Match{Provider: provider("opencode-go", "https://opencode.ai/zen/go/v1")}, true},
		{"地址含 opencode", Match{Provider: provider("custom", "https://opencode.ai/zen/v1")}, true},
		{"地址含 deepseek", Match{Provider: provider("custom", "https://proxy.example.com/deepseek/v1")}, true},
		{"普通网关", Match{Provider: provider("custom", "https://api.example.com/v1")}, false},
	}
	for _, tc := range cases {
		if got := needsReasoningEcho(tc.m); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
