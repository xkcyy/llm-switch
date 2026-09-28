package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateNotifiesSubscribersWithoutDeadlock(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	store.Subscribe(func() {
		_ = store.Snapshot() // 订阅者读取配置不应造成死锁
		close(done)
	})
	if err := store.Update(func(c *Config) error {
		c.Settings.DefaultModel = "A/b"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("订阅者未被调用或发生死锁")
	}
	if got := store.Snapshot().Settings.DefaultModel; got != "A/b" {
		t.Fatalf("DefaultModel = %q", got)
	}
}

func TestUpdateValidationDoesNotPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := true
	if err := store.Update(func(c *Config) error {
		c.Settings.DefaultModel = "A/b"
		if wantErr {
			return errTest
		}
		return nil
	}); err == nil {
		t.Fatal("期望返回错误")
	}
	if got := store.Snapshot().Settings.DefaultModel; got != "" {
		t.Fatalf("失败更新不应写入内存: %q", got)
	}
}

var errTest = &testError{"boom"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestSanitizeRepairsInconsistentData(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *Config) error {
		c.Providers = []Provider{
			{ID: "deepseek", Name: "DeepSeek", Protocol: "bogus", Enabled: true},
			{ID: "", Name: "我的 供应商", Protocol: "chat", Enabled: true},
		}
		c.Models = []Model{
			{ID: "deepseek-flash", ProviderID: "deepseek", Enabled: true},
			{ID: "orphan", ProviderID: "missing", Enabled: true},
			{ID: "deepseek-flash", ProviderID: "deepseek", Enabled: true},
		}
		c.Settings.DefaultModel = "DeepSeek/deepseek-flash" // 旧命名，已失效
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	repairs, err := store.Sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if len(repairs) == 0 {
		t.Fatal("应产生修复项")
	}
	cfg := store.Snapshot()
	if cfg.Providers[0].Protocol != "chat" {
		t.Fatalf("无效协议未回退: %s", cfg.Providers[0].Protocol)
	}
	if cfg.Providers[1].ID == "" {
		t.Fatal("缺失的供应商 ID 未补齐")
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("孤儿/重复模型未清理，剩余 %d 个", len(cfg.Models))
	}
	if cfg.Settings.DefaultModel != "deepseek/deepseek-flash" {
		t.Fatalf("默认模型未修复: %q", cfg.Settings.DefaultModel)
	}
	if got := len(store.LastRepairs()); got != len(repairs) {
		t.Fatalf("LastRepairs = %d, want %d", got, len(repairs))
	}
}

func TestSanitizeKeepsHealthyConfig(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *Config) error {
		c.Providers = []Provider{{ID: "ds", Name: "DeepSeek", Protocol: "chat", Enabled: true}}
		c.Models = []Model{{ID: "deepseek-chat", ProviderID: "ds", Enabled: true}}
		c.Settings.DefaultModel = "ds/deepseek-chat"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err := store.Sanitize()
	if err != nil {
		t.Fatal(err)
	}
	if len(repairs) != 0 {
		t.Fatalf("健康配置不应产生修复项: %+v", repairs)
	}
	if store.Snapshot().Settings.DefaultModel != "ds/deepseek-chat" {
		t.Fatal("健康配置被误改")
	}
}

func TestOpenDefaultsOpenCodeAutoSyncWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{"schema_version":1,"settings":{"proxy":{"host":"127.0.0.1","port":8317},"codex_auto_sync":false}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Snapshot().Settings.OpenCodeAutoSync {
		t.Fatal("老配置缺少 opencode_auto_sync 字段时应按推荐值开启")
	}
	if store.Snapshot().Settings.CodexAutoSync {
		t.Fatal("已有字段不应被改动")
	}
	if err := store.Update(func(c *Config) error {
		c.Settings.OpenCodeAutoSync = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Snapshot().Settings.OpenCodeAutoSync {
		t.Fatal("显式关闭的开关不应被重置为开启")
	}
}

func TestProviderProtocolsMigrationAndSanitize(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *Config) error {
		c.Providers = []Provider{
			// 旧配置只有单值 protocol：迁移为列表
			{ID: "ds", Name: "DeepSeek", Protocol: "responses", Enabled: true},
			// 多协议 + 非法项 + 重复项
			{ID: "km", Name: "KmAiModelHub", Protocol: "responses", Protocols: []string{"responses", "Chat", "chat", "bogus"}, Enabled: true},
		}
		c.Models = []Model{{ID: "m", ProviderID: "ds", Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err := store.Sanitize()
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot()

	if got := cfg.Providers[0].UpstreamProtocols(); len(got) != 1 || got[0] != "responses" {
		t.Fatalf("单值迁移失败: %v", got)
	}
	if cfg.Providers[0].Protocol != "responses" {
		t.Fatalf("兼容字段未同步: %q", cfg.Providers[0].Protocol)
	}
	got := cfg.Providers[1].UpstreamProtocols()
	if len(got) != 2 || got[0] != "responses" || got[1] != "chat" {
		t.Fatalf("协议列表去重/校验失败: %v", got)
	}
	if cfg.Providers[1].Protocol != "responses" {
		t.Fatalf("protocol 应等于列表首项: %q", cfg.Providers[1].Protocol)
	}
	if len(repairs) == 0 {
		t.Fatal("非法协议项应产生修复提示")
	}
	// 兜底：空配置不会返回空列表
	if p := (Provider{}); len(p.UpstreamProtocols()) != 1 || p.UpstreamProtocols()[0] != "chat" {
		t.Fatalf("空配置兜底错误: %v", p.UpstreamProtocols())
	}
}

// 兼容扩展名：去空、去重、保留未知名（可能是更新版本才支持的扩展）。
func TestProviderExtensionsNormalized(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *Config) error {
		c.Providers = []Provider{{
			ID: "gw", Name: "自建网关", Protocol: "chat", Enabled: true,
			Extensions: []string{" reasoning-echo ", "", "reasoning-echo", "future-extension"},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Sanitize(); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Providers[0].Extensions
	want := []string{"reasoning-echo", "future-extension"}
	if len(got) != len(want) {
		t.Fatalf("扩展名去空/去重失败: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("扩展名内容或顺序错误: %v", got)
		}
	}
}

// 可用模型 = 已启用供应商下的已启用模型，按配置顺序，slug 为 供应商ID/模型ID。
func TestAvailableModels(t *testing.T) {
	cfg := Config{
		Providers: []Provider{
			{ID: "a", Name: "A", Protocol: "chat", Enabled: true},
			{ID: "b", Name: "B", Protocol: "chat", Enabled: false},
		},
		Models: []Model{
			{ID: "m1", ProviderID: "a", Enabled: true},
			{ID: "m2", ProviderID: "a", Enabled: false},
			{ID: "m3", ProviderID: "b", Enabled: true},
			{ID: "m4", ProviderID: "missing", Enabled: true},
			{ID: "q/w", ProviderID: "a", Enabled: true},
		},
	}
	got := cfg.AvailableModels()
	want := []string{"a/m1", "a/q/w"}
	if len(got) != len(want) {
		t.Fatalf("可用模型 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Slug != want[i] {
			t.Fatalf("第 %d 个可用模型 = %q, want %q", i, got[i].Slug, want[i])
		}
	}
	if !got[0].Provider.Enabled || got[0].Model.ID != "m1" || got[0].Provider.Name != "A" {
		t.Fatalf("可用模型未带完整定义: %+v", got[0])
	}
	empty := Config{}
	if n := len(empty.AvailableModels()); n != 0 {
		t.Fatalf("空配置的可用模型应为 0，实际 %d", n)
	}
}

func TestLookupAvailable(t *testing.T) {
	cfg := Config{
		Providers: []Provider{
			{ID: "a", Name: "A", Protocol: "chat", Enabled: true},
			{ID: "b", Name: "B", Protocol: "chat", Enabled: false},
		},
		Models: []Model{
			{ID: "m1", ProviderID: "a", Enabled: true},
			{ID: "m2", ProviderID: "a", Enabled: false},
			{ID: "m3", ProviderID: "b", Enabled: true},
		},
	}
	if a, ok := cfg.LookupAvailable("a/m1"); !ok || a.Model.ID != "m1" || a.Provider.ID != "a" {
		t.Fatalf("应命中 a/m1: %+v ok=%v", a, ok)
	}
	for _, slug := range []string{"a/m2", "b/m3", "a/nope", "", "m1"} {
		if a, ok := cfg.LookupAvailable(slug); ok {
			t.Fatalf("%q 不应命中，实际 %+v", slug, a)
		}
	}
}

func TestModelSlug(t *testing.T) {
	// 模型 ID 允许含 /（如 Qwen/Qwen3-35B），供应商 ID 不含 /，因此 slug 仍可反查。
	if got := ModelSlug("ds", "q/w"); got != "ds/q/w" {
		t.Fatalf("ModelSlug = %q", got)
	}
}

// ---- 写入接口（供应商 / 模型） ----

func TestAddProviderRules(t *testing.T) {
	cfg := Config{}
	p, err := cfg.AddProvider(ProviderDraft{
		Name: "My Provider", ID: " My Provider/ ", BaseURL: "https://x/v1/",
		Protocols: []string{"responses", "chat", "responses"},
		Auth:      AuthDraft{Type: "bearer", APIKey: "k"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "my-provider" {
		t.Fatalf("ID 应归一化为 my-provider，实际 %q", p.ID)
	}
	if p.Protocol != "responses" || len(p.Protocols) != 2 {
		t.Fatalf("协议应去重且 protocol 等于首项，实际 %q %v", p.Protocol, p.Protocols)
	}
	if p.BaseURL != "https://x/v1" {
		t.Fatalf("地址应去掉尾部斜杠，实际 %q", p.BaseURL)
	}
	if !p.Enabled || p.Auth.Type != "bearer" {
		t.Fatalf("默认启用且认证类型为 bearer，实际 %+v", p)
	}

	// 未填 ID 时由名称推导
	p2, err := cfg.AddProvider(ProviderDraft{Name: "另一个 供应商", BaseURL: "https://y", Protocols: []string{"chat"}})
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID != "另一个-供应商" {
		t.Fatalf("应由名称推导 ID，实际 %q", p2.ID)
	}

	bad := []struct {
		name  string
		draft ProviderDraft
		want  string
	}{
		{"重名", ProviderDraft{Name: "My Provider", ID: "other", BaseURL: "https://x", Protocols: []string{"chat"}}, "供应商名称 \"My Provider\" 已存在"},
		{"重 ID", ProviderDraft{Name: "Other", ID: "MY-PROVIDER", BaseURL: "https://x", Protocols: []string{"chat"}}, "供应商 ID \"my-provider\" 已存在，请换一个"},
		{"空名称", ProviderDraft{Name: "  ", BaseURL: "https://x", Protocols: []string{"chat"}}, "供应商名称不能为空"},
		{"地址非 http", ProviderDraft{Name: "N", BaseURL: "ftp://x", Protocols: []string{"chat"}}, "接口地址必须以 http(s):// 开头"},
		{"非法协议", ProviderDraft{Name: "N", BaseURL: "https://x", Protocols: []string{"bogus"}}, "协议 \"bogus\" 无效，必须是 chat、responses 或 messages"},
		{"无协议", ProviderDraft{Name: "N", BaseURL: "https://x"}, "请至少选择一个协议"},
		{"非法认证类型", ProviderDraft{Name: "N", BaseURL: "https://x", Protocols: []string{"chat"}, Auth: AuthDraft{Type: "weird"}}, "认证类型不支持"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			before := len(cfg.Providers)
			if _, err := cfg.AddProvider(c.draft); err == nil || err.Error() != c.want {
				t.Fatalf("错误应为 %q，实际 %v", c.want, err)
			}
			if len(cfg.Providers) != before {
				t.Fatal("失败不应改动配置")
			}
		})
	}
}

// 兼容字段与缺省语义：单值 protocol、顶层认证头名回退、扩展名原样保留。
func TestProviderDraftCompatAndDefaults(t *testing.T) {
	cfg := Config{}
	exts := []string{"  pad  ", "", "dup", "dup"}
	p, err := cfg.AddProvider(ProviderDraft{
		Name: "A", BaseURL: "https://x", Protocol: "chat", Extensions: &exts,
		Auth: AuthDraft{Type: "api_key_header"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Protocols) != 1 || p.Protocols[0] != "chat" {
		t.Fatalf("单值 protocol 应迁移为列表，实际 %v", p.Protocols)
	}
	if p.Auth.Header != "x-api-key" {
		t.Fatalf("头名缺省应为 x-api-key，实际 %q", p.Auth.Header)
	}
	if len(p.Extensions) != 4 || p.Extensions[0] != "  pad  " {
		t.Fatalf("扩展名应原样保留，实际 %v", p.Extensions)
	}
}

func TestUpdateProviderCascadesAndKeepsKey(t *testing.T) {
	cfg := Config{}
	if _, err := cfg.AddProvider(ProviderDraft{
		Name: "DeepSeek", ID: "ds", BaseURL: "https://x", Protocols: []string{"chat"},
		Auth: AuthDraft{APIKey: "sk-abcdefgh"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.AddModel("ds", ModelDraft{ID: "chat"}); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.DefaultModel != "ds/chat" {
		t.Fatalf("首个模型应成为默认模型，实际 %q", cfg.Settings.DefaultModel)
	}

	p, err := cfg.UpdateProvider("ds", ProviderDraft{
		Name: "DeepSeek", ID: "ds2", BaseURL: "https://x", Protocols: []string{"chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "ds2" || p.Auth.APIKey != "sk-abcdefgh" {
		t.Fatalf("改 ID 应成功且留空 Key 保留原值，实际 %+v", p)
	}
	if cfg.Models[0].ProviderID != "ds2" || cfg.Settings.DefaultModel != "ds2/chat" {
		t.Fatalf("应级联模型归属与默认模型，实际 %+v / %q", cfg.Models, cfg.Settings.DefaultModel)
	}

	// 显式提交新 Key 时替换
	if p, err = cfg.UpdateProvider("ds2", ProviderDraft{
		Name: "DeepSeek", ID: "ds2", BaseURL: "https://x", Protocols: []string{"chat"},
		Auth: AuthDraft{APIKey: "sk-new"},
	}); err != nil || p.Auth.APIKey != "sk-new" {
		t.Fatalf("提交新 Key 应替换，实际 %+v %v", p, err)
	}

	if _, err := cfg.UpdateProvider("nope", ProviderDraft{Name: "N", BaseURL: "https://x", Protocols: []string{"chat"}}); err == nil {
		t.Fatal("不存在的供应商应报错")
	}
}

func TestRemoveProviderCascadesModels(t *testing.T) {
	cfg := Config{}
	cfg.AddProvider(ProviderDraft{Name: "A", ID: "a", BaseURL: "https://x", Protocols: []string{"chat"}})
	cfg.AddProvider(ProviderDraft{Name: "B", ID: "b", BaseURL: "https://x", Protocols: []string{"chat"}})
	cfg.AddModel("a", ModelDraft{ID: "m1"})
	cfg.AddModel("a", ModelDraft{ID: "m2"})
	cfg.AddModel("b", ModelDraft{ID: "m3"})

	removed, err := cfg.RemoveProvider("a")
	if err != nil || removed != 2 {
		t.Fatalf("应连带删除 2 个模型，实际 %d %v", removed, err)
	}
	if len(cfg.Providers) != 1 || len(cfg.Models) != 1 || cfg.Models[0].ProviderID != "b" {
		t.Fatalf("剩余应为 B 及其 1 个模型，实际 %+v", cfg)
	}
	if _, err := cfg.RemoveProvider("a"); err == nil {
		t.Fatal("重复删除应报错")
	}
}

func TestAddModelRules(t *testing.T) {
	cfg := Config{}
	cfg.AddProvider(ProviderDraft{Name: "A", ID: "a", BaseURL: "https://x", Protocols: []string{"chat"}})

	m, err := cfg.AddModel("a", ModelDraft{ID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "m1" || !m.Enabled || m.ProviderID != "a" {
		t.Fatalf("名称应默认等于 ID 且默认启用，实际 %+v", m)
	}
	if cfg.Settings.DefaultModel != "a/m1" {
		t.Fatalf("默认模型为空时应自动设为 a/m1，实际 %q", cfg.Settings.DefaultModel)
	}
	// 已有默认模型时不覆盖
	if _, err := cfg.AddModel("a", ModelDraft{ID: "m2"}); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.DefaultModel != "a/m1" {
		t.Fatalf("已有默认模型不应被覆盖，实际 %q", cfg.Settings.DefaultModel)
	}

	bad := []struct {
		name string
		d    ModelDraft
		want string
	}{
		{"重复", ModelDraft{ID: "m1"}, "模型 \"m1\" 已存在"},
		{"空 ID", ModelDraft{ID: ""}, "模型 ID 不能为空"},
		{"非法协议", ModelDraft{ID: "x", Protocol: "bogus"}, "模型协议必须是 chat、responses、messages 或留空（跟随供应商）"},
		{"档位不在列表", ModelDraft{ID: "x", Reasoning: &Reasoning{DefaultLevel: "high", Levels: []string{"low"}}}, "默认推理档位不在支持列表中"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := cfg.AddModel("a", c.d); err == nil || err.Error() != c.want {
				t.Fatalf("错误应为 %q，实际 %v", c.want, err)
			}
		})
	}
	if _, err := cfg.AddModel("nope", ModelDraft{ID: "x"}); err == nil || err.Error() != "供应商不存在" {
		t.Fatalf("供应商不存在应报错，实际 %v", err)
	}
}

func TestUpdateAndRemoveModel(t *testing.T) {
	cfg := Config{}
	cfg.AddProvider(ProviderDraft{Name: "A", ID: "a", BaseURL: "https://x", Protocols: []string{"chat"}})
	cfg.AddModel("a", ModelDraft{ID: "m1"})
	cfg.AddModel("a", ModelDraft{ID: "m2"})

	m, err := cfg.UpdateModel("a", "m1", ModelDraft{ID: "m1x", Name: ""})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "m1x" || m.Name != "m1x" {
		t.Fatalf("改名后名称应回落为 ID，实际 %+v", m)
	}
	if _, err := cfg.UpdateModel("a", "m1x", ModelDraft{ID: "m2"}); err == nil || err.Error() != "模型 \"m2\" 已存在" {
		t.Fatalf("重名应报错，实际 %v", err)
	}
	if _, err := cfg.UpdateModel("a", "nope", ModelDraft{ID: "nope"}); err == nil || err.Error() != "模型不存在" {
		t.Fatalf("不存在应报错，实际 %v", err)
	}

	if err := cfg.RemoveModel("a", "m1x"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].ID != "m2" {
		t.Fatalf("删除后应只剩 m2，实际 %+v", cfg.Models)
	}
	if err := cfg.RemoveModel("a", "m1x"); err == nil || err.Error() != "模型不存在" {
		t.Fatalf("重复删除应报错，实际 %v", err)
	}
}

// 启动自愈把已有的供应商 ID 归一化，并级联到模型归属与默认模型。
// 此前只补「缺失的 ID」，已有的怪 ID（大写、空格）会一直留着，模型也会被当孤儿删掉。
func TestSanitizeNormalizesProviderIDAndCascades(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *Config) error {
		c.Providers = []Provider{{ID: "My Provider", Name: "DeepSeek", Protocol: "chat", Enabled: true}}
		c.Models = []Model{{ID: "chat", ProviderID: "My Provider", Enabled: true}}
		c.Settings.DefaultModel = "My Provider/chat"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repairs, err := store.Sanitize()
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Snapshot()
	if cfg.Providers[0].ID != "my-provider" {
		t.Fatalf("ID 应归一化为 my-provider，实际 %q", cfg.Providers[0].ID)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].ProviderID != "my-provider" {
		t.Fatalf("模型归属应级联且不被当孤儿删除，实际 %+v", cfg.Models)
	}
	if cfg.Settings.DefaultModel != "my-provider/chat" {
		t.Fatalf("默认模型应级联，实际 %q", cfg.Settings.DefaultModel)
	}
	if len(repairs) == 0 {
		t.Fatal("应记录修复项")
	}
}

// 写入接口维持「默认模型必须可用」：删除、停用导致失效时立即回退或清空。
func TestWriteKeepsDefaultModelAvailable(t *testing.T) {
	cfg := Config{}
	cfg.AddProvider(ProviderDraft{Name: "A", ID: "a", BaseURL: "https://x", Protocols: []string{"chat"}})
	cfg.AddProvider(ProviderDraft{Name: "B", ID: "b", BaseURL: "https://x", Protocols: []string{"chat"}})
	cfg.AddModel("a", ModelDraft{ID: "m1"})
	cfg.AddModel("b", ModelDraft{ID: "m2"})
	if cfg.Settings.DefaultModel != "a/m1" {
		t.Fatalf("准备数据失败，实际 %q", cfg.Settings.DefaultModel)
	}

	// 删掉非默认模型：默认模型不变
	cfg.AddModel("a", ModelDraft{ID: "m3"})
	if err := cfg.RemoveModel("a", "m3"); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.DefaultModel != "a/m1" {
		t.Fatalf("删除非默认模型不应改动默认模型，实际 %q", cfg.Settings.DefaultModel)
	}

	// 删掉默认模型：回退到第一个可用模型
	if err := cfg.RemoveModel("a", "m1"); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.DefaultModel != "b/m2" {
		t.Fatalf("应回退到 b/m2，实际 %q", cfg.Settings.DefaultModel)
	}

	// 停用默认模型：同样回退（这里只剩它自己，回退为空）
	if _, err := cfg.UpdateModel("b", "m2", ModelDraft{ID: "m2", Enabled: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.DefaultModel != "" {
		t.Fatalf("没有可用模型时应清空，实际 %q", cfg.Settings.DefaultModel)
	}

	// 删掉默认模型所属的供应商：回退到其它可用模型
	cfg2 := Config{}
	cfg2.AddProvider(ProviderDraft{Name: "A", ID: "a", BaseURL: "https://x", Protocols: []string{"chat"}})
	cfg2.AddProvider(ProviderDraft{Name: "B", ID: "b", BaseURL: "https://x", Protocols: []string{"chat"}})
	cfg2.AddModel("a", ModelDraft{ID: "m1"})
	cfg2.AddModel("b", ModelDraft{ID: "m2"})
	if _, err := cfg2.RemoveProvider("a"); err != nil {
		t.Fatal(err)
	}
	if cfg2.Settings.DefaultModel != "b/m2" {
		t.Fatalf("删供应商后应回退到 b/m2，实际 %q", cfg2.Settings.DefaultModel)
	}
}

func boolPtr(v bool) *bool { return &v }
