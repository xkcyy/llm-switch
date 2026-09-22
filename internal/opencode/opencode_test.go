package opencode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llm-switch/internal/config"
)

func sampleConfig() *config.Config {
	cw, out := 1000000, 32000
	return &config.Config{
		Settings: config.Settings{
			Proxy:        config.Endpoint{Host: "127.0.0.1", Port: 8317},
			DefaultModel: "ocg/glm-5.3",
		},
		Providers: []config.Provider{
			{ID: "ocg", Name: "OpenCode Go", Protocol: "chat", BaseURL: "https://opencode.ai/zen/go/v1", Enabled: true},
			{ID: "km", Name: "KmAiModelHub", Protocol: "responses", BaseURL: "https://aimodelhub.example.com/v1", Enabled: true},
			{ID: "off", Name: "Disabled", Protocol: "chat", Enabled: false},
		},
		Models: []config.Model{
			{ID: "glm-5.3", ProviderID: "ocg", Name: "GLM-5.3", Enabled: true, ContextWindow: &cw, MaxOutputTokens: &out},
			{ID: "kimi-k3", ProviderID: "km", Name: "Kimi K3", Enabled: true},
			{ID: "glm-5.3", ProviderID: "km", Name: "GLM-5.3", Enabled: true}, // 同名不同供应商：键必须限定
			{ID: "ctx-only", ProviderID: "km", Enabled: true, ContextWindow: &cw},
			{ID: "hidden", ProviderID: "km", Enabled: false},
			{ID: "orphan", ProviderID: "off", Enabled: true},
		},
	}
}

func newService(t *testing.T) (*Service, *config.Store, string) {
	t.Helper()
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
		Dir:    dir,
		Config: filepath.Join(dir, "opencode.json"),
		JSONC:  filepath.Join(dir, "opencode.jsonc"),
	}
	return svc, store, dir
}

const userConfig = `{
  "$schema": "https://opencode.ai/config.json",
  "disabled_providers": ["glm"],
  "model": "km/gpt-5.6-sol",
  "provider": {
    "km": {
      "name": "KM_ModelHub",
      "npm": "@ai-sdk/openai",
      "models": { "gpt-5.6-sol": { "name": "gpt-5.6-sol" } },
      "options": { "apiKey": "sk-user-secret", "baseURL": "https://qegilibrvucj.example/v1" }
    }
  },
  "plugin": ["oh-my-opencode"]
}`

func TestBuildPlanAndApplyPreservesUserConfig(t *testing.T) {
	svc, _, _ := newService(t)
	if err := os.WriteFile(svc.paths.Config, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BaseURL != "http://127.0.0.1:8317/v1" {
		t.Fatalf("base_url = %s", plan.BaseURL)
	}
	// 只包含启用中的模型，且键为 供应商ID/模型ID
	want := []string{"km/ctx-only", "km/glm-5.3", "km/kimi-k3", "ocg/glm-5.3"}
	if strings.Join(plan.Models, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", plan.Models, want)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(svc.paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("写入结果不是合法 JSON: %v", err)
	}
	// 用户内容必须保留
	if doc["model"] != "km/gpt-5.6-sol" {
		t.Fatalf("用户默认模型被改动: %v", doc["model"])
	}
	if doc["$schema"] != "https://opencode.ai/config.json" {
		t.Fatalf("$schema 丢失")
	}
	if _, ok := doc["disabled_providers"]; !ok {
		t.Fatalf("disabled_providers 丢失")
	}
	if _, ok := doc["plugin"]; !ok {
		t.Fatalf("plugin 丢失")
	}
	providers := doc["provider"].(map[string]any)
	km := providers["km"].(map[string]any)
	opts := km["options"].(map[string]any)
	if opts["apiKey"] != "sk-user-secret" {
		t.Fatalf("用户密钥被改动: %v", opts["apiKey"])
	}
	ls, ok := providers["llm-switch"].(map[string]any)
	if !ok {
		t.Fatalf("llm-switch 段未写入")
	}
	if ls["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %v", ls["npm"])
	}
	lsOpts := ls["options"].(map[string]any)
	if lsOpts["baseURL"] != "http://127.0.0.1:8317/v1" || lsOpts["apiKey"] != placeholderKey {
		t.Fatalf("llm-switch options = %v", lsOpts)
	}
	models := ls["models"].(map[string]any)
	if len(models) != 4 {
		t.Fatalf("模型数量 = %d", len(models))
	}
	if name, _ := models["ocg/glm-5.3"].(map[string]any)["name"].(string); name != "ocg/glm-5.3" {
		t.Fatalf("模型展示名 = %q", name)
	}
	// V1 形状：limit 必须同时给出 context 与 output，只知其一则不写（否则配置校验失败）
	if _, ok := models["ocg/glm-5.3"].(map[string]any)["limit"]; !ok {
		t.Fatalf("context+output 已知时应写入 limit")
	}
	if _, ok := models["km/ctx-only"].(map[string]any)["limit"]; ok {
		t.Fatalf("只有 context 时不应写 limit（1.x 要求 output 必填）")
	}
	if _, ok := models["km/kimi-k3"].(map[string]any)["limit"]; ok {
		t.Fatalf("未知上限不应写 limit")
	}
	if !svc.Connected() {
		t.Fatalf("Connected 应为 true")
	}
}

func TestFallbackWhenManagedDefaultModelMissing(t *testing.T) {
	svc, _, _ := newService(t)
	existing := `{"model":"llm-switch/gone/model","provider":{}}`
	if err := os.WriteFile(svc.paths.Config, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Model != "llm-switch/ocg/glm-5.3" {
		t.Fatalf("默认模型未回退: %q", plan.Model)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("应给出回退告警")
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.RootModel != "llm-switch/ocg/glm-5.3" || !st.UseDefaultModel {
		t.Fatalf("status = %+v", st)
	}
}

func TestUseDefaultModelToggle(t *testing.T) {
	svc, _, _ := newService(t)
	yes := true
	plan, err := svc.BuildPlan(Overrides{UseDefaultModel: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Model != "llm-switch/ocg/glm-5.3" {
		t.Fatalf("model = %q", plan.Model)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}

	no := false
	plan2, err := svc.BuildPlan(Overrides{UseDefaultModel: &no})
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.RemoveRootModel {
		t.Fatalf("应标记移除根级 model")
	}
	if err := svc.Apply(plan2); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(svc.paths.Config)
	if strings.Contains(string(b), `"model"`) {
		t.Fatalf("根级 model 未被移除: %s", b)
	}
}

func TestDisconnectKeepsUserConfig(t *testing.T) {
	svc, _, _ := newService(t)
	if err := os.WriteFile(svc.paths.Config, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	yes := true
	plan, err := svc.BuildPlan(Overrides{UseDefaultModel: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(svc.paths.Config)
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("断开后不是合法 JSON: %v", err)
	}
	providers := doc["provider"].(map[string]any)
	if _, ok := providers["llm-switch"]; ok {
		t.Fatalf("llm-switch 段未移除")
	}
	if _, ok := providers["km"]; !ok {
		t.Fatalf("用户 provider 被误删")
	}
	if _, ok := doc["model"]; ok {
		t.Fatalf("本插件写入的默认模型未移除: %v", doc["model"])
	}
	if svc.Connected() {
		t.Fatalf("断开后 Connected 应为 false")
	}
}

func TestJSONCOnlyRefuses(t *testing.T) {
	svc, _, _ := newService(t)
	if err := os.WriteFile(svc.paths.JSONC, []byte("{\n  // 注释\n  \"model\": \"a/b\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BuildPlan(Overrides{}); !errors.Is(err, ErrJSONCOnly) {
		t.Fatalf("err = %v, want ErrJSONCOnly", err)
	}
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Connected || st.Generated != "" {
		t.Fatalf("jsonc 场景不应生成配置")
	}
	if len(st.Warnings) == 0 {
		t.Fatal("应提示 jsonc 限制")
	}
	// 补一个 opencode.json 后应可正常写入，并提示两者共存
	if err := os.WriteFile(svc.paths.Config, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) == 0 || !strings.Contains(plan.Warnings[0], "同时存在") {
		t.Fatalf("应提示共存: %v", plan.Warnings)
	}
}

func TestManualConfigJSONOverride(t *testing.T) {
	svc, _, _ := newService(t)
	manual := `{"provider":{"llm-switch":{"npm":"@ai-sdk/openai-compatible","models":{"x/y":{"name":"x/y"}}}}}`
	plan, err := svc.BuildPlan(Overrides{ConfigJSON: manual})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(svc.paths.Config)
	if !strings.Contains(string(b), `"x/y"`) {
		t.Fatalf("手动内容未写入: %s", b)
	}
	if _, err := svc.BuildPlan(Overrides{ConfigJSON: `{"provider":{}}`}); err == nil {
		t.Fatal("缺少 llm-switch 段时应报错")
	}
	if _, err := svc.BuildPlan(Overrides{ConfigJSON: `{`}); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestNoModels(t *testing.T) {
	svc, store, _ := newService(t)
	if err := store.Update(func(c *config.Config) error {
		c.Models = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BuildPlan(Overrides{}); !errors.Is(err, ErrNoModels) {
		t.Fatalf("err = %v, want ErrNoModels", err)
	}
}

func TestBuildPlanDoesNotCreateFileOnDryRun(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.BuildPlan(Overrides{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.paths.Config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("生成计划不应落盘")
	}
}

// OpenCode 2.x 原生形状的文件：自动跟随 V2 形状写入，且不动用户已有内容。
func TestShapeAutoFollowsV2File(t *testing.T) {
	svc, _, _ := newService(t)
	v2 := `{
  "$schema": "https://opencode.ai/config.json",
  "providers": {
    "km": {
      "name": "KM_ModelHub",
      "package": "aisdk:@ai-sdk/openai",
      "settings": { "baseURL": "https://km.example/v1", "apiKey": "sk-user" },
      "models": { "gpt-5.4": { "name": "gpt-5.4" } }
    }
  }
}`
	if err := os.WriteFile(svc.paths.Config, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Shape != ShapeV2 {
		t.Fatalf("shape = %s, want v2", plan.Shape)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(svc.paths.Config)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["provider"]; ok {
		t.Fatalf("不应写入 V1 形状的 provider 键")
	}
	providers := doc["providers"].(map[string]any)
	if _, ok := providers["km"]; !ok {
		t.Fatalf("用户 V2 provider 被误删")
	}
	ls := providers["llm-switch"].(map[string]any)
	if ls["package"] != packageV2 {
		t.Fatalf("package = %v", ls["package"])
	}
	if _, ok := ls["npm"]; ok {
		t.Fatalf("V2 形状不应包含 npm 字段")
	}
	settings := ls["settings"].(map[string]any)
	if settings["baseURL"] != "http://127.0.0.1:8317/v1" || settings["apiKey"] != placeholderKey {
		t.Fatalf("settings = %v", settings)
	}
	models := ls["models"].(map[string]any)
	entry := models["ocg/glm-5.3"].(map[string]any)
	if entry["modelID"] != "ocg/glm-5.3" {
		t.Fatalf("modelID = %v", entry["modelID"])
	}
	limit, ok := entry["limit"].(map[string]any)
	if !ok || limit["context"] != float64(1000000) || limit["output"] != float64(32000) {
		t.Fatalf("limit 未写入: %v", entry["limit"])
	}
	// V2 形状：只知 context 也允许写（1.x 则要求 output 必填）
	ctxOnly, ok := models["km/ctx-only"].(map[string]any)["limit"].(map[string]any)
	if !ok || ctxOnly["context"] != float64(1000000) {
		t.Fatalf("V2 形状下应写入 context-only limit: %v", models["km/ctx-only"])
	}
	if _, hasOutput := ctxOnly["output"]; hasOutput {
		t.Fatalf("未知 output 不应写: %v", ctxOnly)
	}
	if _, ok := models["km/kimi-k3"].(map[string]any)["limit"]; ok {
		t.Fatalf("未知上限不应写 limit")
	}
}

// 强制 V2：清理另一种形状下的同名残留，避免重复条目。
func TestShapeForcedV2RemovesStaleV1Entry(t *testing.T) {
	svc, store, _ := newService(t)
	if err := store.Update(func(c *config.Config) error {
		c.Settings.OpenCodeShape = ShapeV2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.paths.Config, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	// 先按 V1 形状接入一次，制造残留
	v1Svc, _ := svc, store
	if err := v1Svc.store.Update(func(c *config.Config) error {
		c.Settings.OpenCodeShape = ShapeAuto
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	planV1, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(planV1); err != nil {
		t.Fatal(err)
	}
	// 再强制 V2
	if err := store.Update(func(c *config.Config) error {
		c.Settings.OpenCodeShape = ShapeV2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Shape != ShapeV2 {
		t.Fatalf("shape = %s", plan.Shape)
	}
	if len(plan.Warnings) == 0 || !strings.Contains(strings.Join(plan.Warnings, " "), "另一形状") {
		t.Fatalf("应提示已清理另一形状: %v", plan.Warnings)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(svc.paths.Config)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if root, ok := doc["provider"].(map[string]any); ok {
		if _, stale := root[ProviderID]; stale {
			t.Fatalf("V1 形状下的 llm-switch 残留未清理")
		}
		if _, ok := root["km"]; !ok {
			t.Fatalf("用户 V1 provider 丢失")
		}
	} else {
		t.Fatalf("用户 V1 provider 整段丢失")
	}
	providers := doc["providers"].(map[string]any)
	if _, ok := providers[ProviderID]; !ok {
		t.Fatalf("V2 形状未写入")
	}
	if doc["model"] != "km/gpt-5.6-sol" {
		t.Fatalf("用户默认模型被改动: %v", doc["model"])
	}
}

func TestShapeStatusReportsSetting(t *testing.T) {
	svc, _, _ := newService(t)
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.ShapeSetting != ShapeAuto || st.Shape != ShapeV1 {
		t.Fatalf("status shape=%s setting=%s", st.Shape, st.ShapeSetting)
	}
}

// 用户的 provider 都在 V1 形状里、而 V2 形状下只剩我们自己的残留时，auto 应判定回 V1 并迁移。
func TestShapeAutoPrefersUserShapeAndMigratesBack(t *testing.T) {
	svc, _, _ := newService(t)
	mixed := `{
  "provider": {
    "km": { "name": "KM", "npm": "@ai-sdk/openai", "models": {}, "options": { "apiKey": "sk-user" } }
  },
  "providers": {
    "llm-switch": { "name": "LLM Switch", "package": "aisdk:@ai-sdk/openai-compatible", "models": { "stale/x": { "name": "stale/x" } } }
  }
}`
	if err := os.WriteFile(svc.paths.Config, []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Shape != ShapeV1 {
		t.Fatalf("shape = %s, want v1（用户的 provider 在 V1 形状里）", plan.Shape)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(svc.paths.Config)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["providers"]; ok {
		t.Fatalf("残留的 V2 段应被整段清理: %v", doc["providers"])
	}
	if _, ok := doc["provider"].(map[string]any)[ProviderID]; !ok {
		t.Fatalf("未写回 V1 形状")
	}
	if _, ok := doc["provider"].(map[string]any)["km"]; !ok {
		t.Fatalf("用户 provider 丢失")
	}
}
