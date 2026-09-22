package proxy

import (
	"errors"
	"testing"

	"llm-switch/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Providers: []config.Provider{
			{ID: "p1", Name: "DeepSeek", Protocol: "chat", BaseURL: "https://api.deepseek.com/v1", Enabled: true},
			{ID: "p2", Name: "OpenAI", Protocol: "responses", BaseURL: "https://api.openai.com/v1", Enabled: true},
			{ID: "p3", Name: "Disabled", Protocol: "chat", BaseURL: "https://x", Enabled: false},
			{ID: "p4", Name: "Mirror", Protocol: "chat", BaseURL: "https://y", Enabled: true},
		},
		Models: []config.Model{
			{ID: "deepseek-chat", ProviderID: "p1", Enabled: true},
			{ID: "gpt-4.1", ProviderID: "p2", Enabled: true},
			{ID: "gpt-4.1", ProviderID: "p3", Enabled: true},
			{ID: "gpt-4.1", ProviderID: "p4", Enabled: true},
			{ID: "deepseek-chat", ProviderID: "p3", Enabled: true},
			{ID: "old-model", ProviderID: "p1", Enabled: false},
		},
	}
}

func TestResolve(t *testing.T) {
	ix := BuildIndex(testConfig())

	t.Run("provider/model 唯一匹配", func(t *testing.T) {
		m, err := ix.Resolve("DeepSeek/deepseek-chat")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Provider.Name != "DeepSeek" || m.Model.ID != "deepseek-chat" {
			t.Fatalf("resolved %s/%s", m.Provider.Name, m.Model.ID)
		}
	})

	t.Run("裁剪空白", func(t *testing.T) {
		if _, err := ix.Resolve("  OpenAI/gpt-4.1  "); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("空名称", func(t *testing.T) {
		if _, err := ix.Resolve("   "); !errors.Is(err, ErrMissingModel) {
			t.Fatalf("want ErrMissingModel, got %v", err)
		}
	})

	t.Run("供应商不存在", func(t *testing.T) {
		if _, err := ix.Resolve("Nope/x"); !errors.Is(err, ErrProviderNotFound) {
			t.Fatalf("want ErrProviderNotFound, got %v", err)
		}
	})

	t.Run("停用供应商不可用", func(t *testing.T) {
		if _, err := ix.Resolve("Disabled/deepseek-chat"); !errors.Is(err, ErrProviderNotFound) {
			t.Fatalf("want ErrProviderNotFound, got %v", err)
		}
	})

	t.Run("停用模型不可用", func(t *testing.T) {
		if _, err := ix.Resolve("DeepSeek/old-model"); !errors.Is(err, ErrModelNotFound) {
			t.Fatalf("want ErrModelNotFound, got %v", err)
		}
	})

	t.Run("无前缀唯一匹配", func(t *testing.T) {
		m, err := ix.Resolve("deepseek-chat")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Provider.Name != "DeepSeek" {
			t.Fatalf("got provider %s", m.Provider.Name)
		}
	})

	t.Run("名称歧义", func(t *testing.T) {
		if _, err := ix.Resolve("gpt-4.1"); !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("want ErrAmbiguous, got %v", err)
		}
	})

	t.Run("模型不存在", func(t *testing.T) {
		if _, err := ix.Resolve("not-exist"); !errors.Is(err, ErrModelNotFound) {
			t.Fatalf("want ErrModelNotFound, got %v", err)
		}
	})

	t.Run("模型名内部斜杠", func(t *testing.T) {
		cfg := &config.Config{
			Providers: []config.Provider{{ID: "qwen", Name: "Qwen", Enabled: true}},
			Models:    []config.Model{{ID: "Qwen/Qwen3.6-35B", ProviderID: "qwen", Enabled: true}},
		}
		ix2 := BuildIndex(cfg)
		// 第一个 / 之前是供应商 ID，其余全部属于模型名
		m, err := ix2.Resolve("qwen/Qwen/Qwen3.6-35B")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Model.ID != "Qwen/Qwen3.6-35B" {
			t.Fatalf("got %s", m.Model.ID)
		}
	})

	t.Run("供应商 ID 定位", func(t *testing.T) {
		m, err := ix.Resolve("p1/deepseek-chat")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Provider.ID != "p1" || m.Model.ID != "deepseek-chat" {
			t.Fatalf("resolved %s/%s", m.Provider.ID, m.Model.ID)
		}
	})

	t.Run("供应商名称兼容且大小写不敏感", func(t *testing.T) {
		for _, key := range []string{"DeepSeek/deepseek-chat", "deepseek/deepseek-chat"} {
			m, err := ix.Resolve(key)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", key, err)
			}
			if m.Provider.ID != "p1" {
				t.Fatalf("%s resolved provider %s", key, m.Provider.ID)
			}
		}
	})
}
