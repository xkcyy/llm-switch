package codex

import (
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"llm-switch/internal/config"
)

func sampleConfig() *config.Config {
	cw := 131072
	return &config.Config{
		Settings: config.Settings{
			Proxy:        config.Endpoint{Host: "127.0.0.1", Port: 8317},
			DefaultModel: "deepseek/deepseek-chat",
		},
		Providers: []config.Provider{
			{ID: "deepseek", Name: "DeepSeek", Protocol: "chat", BaseURL: "https://api.deepseek.com/v1", Enabled: true},
			{ID: "anthropic", Name: "Anthropic", Protocol: "messages", BaseURL: "https://api.anthropic.com/v1", Enabled: true},
		},
		Models: []config.Model{
			{ID: "deepseek-chat", ProviderID: "deepseek", Name: "DeepSeek Chat", Enabled: true,
				Capabilities: []string{"tools", "reasoning"}, ContextWindow: &cw,
				Reasoning: &config.Reasoning{DefaultLevel: "medium", Levels: []string{"low", "medium", "high"}}},
			{ID: "claude-sonnet-4-6", ProviderID: "anthropic", Enabled: true},
		},
	}
}

func TestBuildCatalog(t *testing.T) {
	cfg := sampleConfig()
	cat, err := BuildCatalog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Models) != 2 {
		t.Fatalf("want 2 models, got %d", len(cat.Models))
	}
	first := cat.Models[0]
	if first.Slug != "deepseek/deepseek-chat" {
		t.Fatalf("slug = %s", first.Slug)
	}
	if first.DisplayName != "deepseek/deepseek-chat" {
		t.Fatalf("display_name = %s", first.DisplayName)
	}
	if first.ContextWindow == nil || *first.ContextWindow != 131072 {
		t.Fatalf("context_window 未写入")
	}
	if first.DefaultReasoningLevel != "medium" || len(first.SupportedReasoningLevels) != 3 {
		t.Fatalf("推理档位错误: %+v", first.SupportedReasoningLevels)
	}
	if !first.SupportsParallelToolCalls {
		t.Fatalf("supports_parallel_tool_calls 必须为 true（Codex 必填字段）")
	}
	if err := cat.Validate("deepseek/deepseek-chat"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := cat.Validate("Nope/none"); err == nil {
		t.Fatalf("默认模型不在目录中时应报错")
	}
}

// 图片输入：只有明确声明视觉能力才写 image，能力未知时保持纯文本。
func TestBuildCatalogInputModalities(t *testing.T) {
	cfg := sampleConfig()
	cfg.Models[0].Capabilities = []string{"tools", "vision"}
	cat, err := BuildCatalog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := cat.Models[0].InputModalities; len(got) != 2 || got[0] != "text" || got[1] != "image" {
		t.Fatalf("声明 vision 后 input_modalities = %v，want [text image]", got)
	}

	cfg = sampleConfig()
	cfg.Models = cfg.Models[:1]
	if got := mustCatalog(t, cfg).Models[0].InputModalities; len(got) != 1 || got[0] != "text" {
		t.Fatalf("未声明视觉能力时 input_modalities = %v，want [text]", got)
	}
}

func mustCatalog(t *testing.T, cfg *config.Config) Catalog {
	t.Helper()
	cat, err := BuildCatalog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestMergeConfigTOMLPreservesUserKeys(t *testing.T) {
	existing := []byte(`
model = "old"
model_reasoning_effort = "xhigh"
approval_policy = "never"

[projects.'C:\work']
trust_level = "trusted"

[model_providers.km]
name = "km"
base_url = "https://aimodelhub.example.com/v1"
wire_api = "responses"
`)
	out, err := mergeConfigTOML(existing, "http://127.0.0.1:8317/v1", "DeepSeek/deepseek-chat", `C:\Users\x\.codex\llm-switch-models.json`)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("生成的 TOML 无法解析: %v", err)
	}
	if parsed["model_provider"] != "llm-switch" {
		t.Fatalf("model_provider = %v", parsed["model_provider"])
	}
	if parsed["model"] != "DeepSeek/deepseek-chat" {
		t.Fatalf("model = %v", parsed["model"])
	}
	if parsed["model_reasoning_effort"] != "xhigh" {
		t.Fatalf("用户键 model_reasoning_effort 丢失")
	}
	if _, ok := parsed["projects"]; !ok {
		t.Fatalf("用户表 projects 丢失")
	}
	providers, ok := parsed["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers 丢失")
	}
	if _, ok := providers["km"]; !ok {
		t.Fatalf("用户已有供应商 km 丢失")
	}
	ls, ok := providers["llm-switch"].(map[string]any)
	if !ok {
		t.Fatalf("llm-switch 供应商未写入")
	}
	if ls["wire_api"] != "responses" {
		t.Fatalf("wire_api = %v", ls["wire_api"])
	}
}

func TestMergeConfigTOMLRootKeysBeforeTables(t *testing.T) {
	// TOML 要求根级标量出现在任何表之前；序列化结果必须仍然合法。
	out, err := mergeConfigTOML(nil, "http://127.0.0.1:8317/v1", "A/b", "C:\\c.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("生成的 TOML 无法解析: %v\n%s", err, out)
	}
	if parsed["model"] != "A/b" {
		t.Fatalf("model = %v", parsed["model"])
	}
}

func TestBuildPlanAndApply(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		*c = *sampleConfig()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store)
	svc.paths = Paths{
		ConfigTOML: filepath.Join(dir, "config.toml"),
		AuthJSON:   filepath.Join(dir, "auth.json"),
		Catalog:    filepath.Join(dir, "models.json"),
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BaseURL != "http://127.0.0.1:8317/v1" {
		t.Fatalf("base_url = %s", plan.BaseURL)
	}
	if plan.DefaultModel != "deepseek/deepseek-chat" {
		t.Fatalf("default model = %s", plan.DefaultModel)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.paths.Catalog); err != nil {
		t.Fatalf("模型目录未写入: %v", err)
	}
	auth, err := os.ReadFile(svc.paths.AuthJSON)
	if err != nil || !containsStr(string(auth), "OPENAI_API_KEY") {
		t.Fatalf("auth.json 内容错误: %v %s", err, auth)
	}
	// 手动覆盖也必须走同一写入路径
	manual, err := svc.BuildPlan(Overrides{DefaultModel: "anthropic/claude-sonnet-4-6"})
	if err != nil {
		t.Fatal(err)
	}
	if manual.DefaultModel != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("覆盖默认模型失败: %s", manual.DefaultModel)
	}
}

func TestBuildPlanFallsBackWhenDefaultModelMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		*c = *sampleConfig()
		c.Settings.DefaultModel = "DeepSeek/deepseek-flash" // 旧命名，已失效
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store)
	svc.paths = Paths{
		ConfigTOML: filepath.Join(dir, "config.toml"),
		AuthJSON:   filepath.Join(dir, "auth.json"),
		Catalog:    filepath.Join(dir, "models.json"),
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatalf("默认模型失效不应阻断同步: %v", err)
	}
	if plan.DefaultModel != "deepseek/deepseek-chat" {
		t.Fatalf("未回退到可用模型: %s", plan.DefaultModel)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("应给出告警说明默认模型已回退")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestDumpForCodexValidation 在设置 LLM_SWITCH_DUMP_DIR 时导出配置，
// 供真实 Codex（CODEX_HOME 指向同一目录）验证解析。
func TestDumpForCodexValidation(t *testing.T) {
	dir := os.Getenv("LLM_SWITCH_DUMP_DIR")
	if dir == "" {
		t.Skip("未设置 LLM_SWITCH_DUMP_DIR，跳过导出")
	}
	store, err := config.Open(filepath.Join(dir, "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		*c = *sampleConfig()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store)
	svc.paths = Paths{
		ConfigTOML: filepath.Join(dir, "config.toml"),
		AuthJSON:   filepath.Join(dir, "auth.json"),
		Catalog:    filepath.Join(dir, "llm-switch-models.json"),
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	t.Logf("已导出到 %s", dir)
}
