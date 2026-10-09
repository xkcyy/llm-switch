package workbuddy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llm-switch/internal/config"
)

func newStore(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("WORKBUDDY_CONFIG_DIR", dir)
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(c *config.Config) error {
		if _, err := c.AddProvider(config.ProviderDraft{
			ID: "DeepSeek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com",
			Protocols: []string{"chat"}, Auth: config.AuthDraft{Type: "bearer", APIKey: "sk-x"},
		}); err != nil {
			return err
		}
		cw := 128000
		_, err := c.AddModel("deepseek", config.ModelDraft{
			ID: "deepseek-chat", ContextWindow: &cw,
			Capabilities: []string{"tools", "vision"},
			Reasoning:    &config.Reasoning{DefaultLevel: "high", Levels: []string{"low", "high"}},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, filepath.Join(dir, Filename)
}

func TestBuildPlanPreservesUserModels(t *testing.T) {
	store, path := newStore(t)
	svc := NewService(store)

	userEntry := `{"id":"my-ollama","name":"My Ollama","vendor":"Custom","url":"http://127.0.0.1:11434/v1/chat/completions","apiKey":"x","supportsToolCall":true}`
	stale := `{"id":"Old/deepseek","name":"Old/deepseek","vendor":"LLM Switch","url":"http://127.0.0.1:8317/v1/chat/completions","apiKey":"y","llmSwitchManaged":true}`
	write(t, path, "[\n  "+userEntry+",\n  "+stale+"\n]\n")

	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Models) != 1 || plan.Models[0] != "deepseek/deepseek-chat" {
		t.Fatalf("models = %v", plan.Models)
	}
	if !strings.Contains(plan.Content, "my-ollama") {
		t.Fatalf("用户条目被误删: %s", plan.Content)
	}
	if strings.Contains(plan.Content, "Old/deepseek") {
		t.Fatalf("旧条目未被替换: %s", plan.Content)
	}
	if !strings.Contains(plan.Content, "http://127.0.0.1:8317/v1/chat/completions") {
		t.Fatalf("未写入代理地址: %s", plan.Content)
	}

	// 用户自建条目字段应原样保留（顺序、额外字段都不动）。
	if !strings.Contains(plan.Content, `"id": "my-ollama"`) {
		t.Fatalf("字段未保留: %s", plan.Content)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(got, "Old/deepseek") || !strings.Contains(got, "my-ollama") {
		t.Fatalf("落盘内容不对: %s", got)
	}

	// 幂等：内容一致时不重复写入。
	before := got
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if read(t, path) != before {
		t.Fatalf("重复写入改动了文件")
	}
}

func TestBuildPlanObjectFormatKeepsWrapper(t *testing.T) {
	store, path := newStore(t)
	svc := NewService(store)
	write(t, path, `{
  "version": 2,
  "models": [
    {"id":"mine","url":"https://example.com/v1","apiKey":"z"}
  ]
}
`)
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct {
		Version int              `json:"version"`
		Models  []map[string]any `json:"models"`
	}
	if err := json.Unmarshal([]byte(plan.Content), &wrapper); err != nil {
		t.Fatalf("输出不是对象格式: %v (%s)", err, plan.Content)
	}
	if wrapper.Version != 2 {
		t.Fatalf("wrapper 字段丢失: %s", plan.Content)
	}
	if len(wrapper.Models) != 2 {
		t.Fatalf("models = %d (%s)", len(wrapper.Models), plan.Content)
	}
}

func TestDisconnectOnlyRemovesOurs(t *testing.T) {
	store, path := newStore(t)
	svc := NewService(store)
	write(t, path, `[
  {"id":"mine","url":"http://127.0.0.1:19134/v1/chat/completions","apiKey":"z"},
  {"id":"DeepSeek/deepseek-chat","url":"http://127.0.0.1:8317/v1/chat/completions","apiKey":"y","llmSwitchManaged":true}
]
`)
	if err := svc.Disconnect(); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "mine") {
		t.Fatalf("用户条目被误删: %s", got)
	}
	if strings.Contains(got, "deepseek/deepseek-chat") {
		t.Fatalf("本工具条目未删除: %s", got)
	}
}

func TestBuildPlanRejectsInvalidJSON(t *testing.T) {
	store, path := newStore(t)
	svc := NewService(store)
	write(t, path, "{not json")
	if _, err := svc.BuildPlan(Overrides{}); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	// 不破坏原文件。
	if read(t, path) != "{not json" {
		t.Fatalf("原文件被改动: %s", read(t, path))
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
