package trae

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llm-switch/internal/config"
)

// newStore 造一个带样本数据的配置存储。
func intPtr(v int) *int { return &v }

func newStore(t *testing.T) *config.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(c *config.Config) error {
		*c = config.Config{
			Settings: config.Settings{
				Proxy: config.Endpoint{Host: "127.0.0.1", Port: 8317},
			},
			Providers: []config.Provider{
				{ID: "ocg", Name: "OpenCode Go", Protocol: "chat", Enabled: true},
				{ID: "km", Name: "KmAiModelHub", Protocol: "responses", Enabled: true},
				{ID: "off", Name: "Disabled", Protocol: "chat", Enabled: false},
			},
			Models: []config.Model{
				{ID: "glm-5.3", ProviderID: "ocg", Enabled: true,
					ContextWindow: intPtr(1000000),
					Reasoning:     &config.Reasoning{DefaultLevel: "high", Levels: []string{"low", "high", "max"}}},
				{ID: "gpt-5.6-sol", ProviderID: "km", Enabled: true,
					ContextWindow: intPtr(1050000), MaxOutputTokens: intPtr(64000),
					Capabilities: []string{"tools", "vision"}},
				{ID: "hidden", ProviderID: "km", Enabled: false}, // 停用模型不进清单
				{ID: "orphan", ProviderID: "off", Enabled: true}, // 停用供应商不进清单
			},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestModelsAndBaseURL(t *testing.T) {
	svc := NewService(newStore(t))
	got := svc.Models()
	want := []string{"ocg/glm-5.3", "km/gpt-5.6-sol"}
	if len(got) != len(want) {
		t.Fatalf("模型清单应为 %v，实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("模型清单应为 %v，实际 %v", want, got)
		}
	}
	if svc.BaseURL() != "http://127.0.0.1:8317/v1" {
		t.Fatalf("BaseURL 应为 http://127.0.0.1:8317/v1，实际 %s", svc.BaseURL())
	}
}

func TestEnsureDebugPortPreservesContent(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, ".trae-cn", "argv.json")
	if err := os.MkdirAll(filepath.Dir(argv), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "{\n  \"locale\": \"zh-cn\",\n\n\t// 注释要保留\n\t\"enable-crash-reporter\": true\n}\n"
	if err := os.WriteFile(argv, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Paths{ArgvJSON: argv}

	if p.DebugPortConfigured() {
		t.Fatal("初始状态不应配置调试端口")
	}
	if p.DebugPort() != DefaultPort {
		t.Fatalf("未配置时应返回默认端口 %d", DefaultPort)
	}

	changed, err := p.EnsureDebugPort()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("首次写入应返回 changed=true")
	}
	body, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "\"remote-debugging-port\": \"9222\"") {
		t.Fatalf("写入后应包含调试端口：%s", text)
	}
	if !strings.Contains(text, "locale") || !strings.Contains(text, "注释要保留") {
		t.Fatalf("原有内容被破坏：%s", text)
	}
	if !p.DebugPortConfigured() || p.DebugPort() != DefaultPort {
		t.Fatal("写入后应能读出端口")
	}
	if _, err := os.Stat(argv + ".bak-llmswitch"); err != nil {
		t.Fatal("应保留一份备份")
	}

	changed, err = p.EnsureDebugPort()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("重复写入应返回 changed=false")
	}

	removed, err := p.RemoveDebugPort()
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("移除应返回 true")
	}
	body, _ = os.ReadFile(argv)
	if strings.Contains(string(body), "remote-debugging-port") {
		t.Fatalf("移除后不应再有调试端口：%s", string(body))
	}
	if !strings.Contains(string(body), "locale") {
		t.Fatalf("移除时不应破坏其它内容：%s", string(body))
	}
}

func TestEnsureDebugPortCreatesFile(t *testing.T) {
	dir := t.TempDir()
	p := Paths{ArgvJSON: filepath.Join(dir, ".trae-cn", "argv.json")}
	if _, err := p.EnsureDebugPort(); err != nil {
		t.Fatal(err)
	}
	port := p.DebugPort()
	if port != DefaultPort {
		t.Fatalf("应写入默认端口，实际读到 %d", port)
	}
}

func TestIsCustomVendor(t *testing.T) {
	cases := map[string]bool{
		"自定义(OpenAI Compatible)":     true,
		"Custom (OpenAI Compatible)": true,
		"火山引擎 Plan":                  false,
		"DeepSeek":                   false,
		"":                           false,
	}
	for in, want := range cases {
		if got := isCustomVendor(in); got != want {
			t.Fatalf("isCustomVendor(%q) = %v，期望 %v", in, got, want)
		}
	}
}

// 页面上的差异计算依赖「显示名 == 模型 ID」这一约定，这里记录该约定。
func TestDisplayNameEqualsModelID(t *testing.T) {
	svc := NewService(newStore(t))
	for _, m := range svc.Models() {
		if !strings.Contains(m, "/") {
			t.Fatalf("模型键应形如 供应商ID/模型ID：%s", m)
		}
		if strings.Contains(m, "//") {
			t.Fatalf("模型键不应包含双斜杠：%s", m)
		}
	}
}

func TestSplitDiff(t *testing.T) {
	targets := []string{"a/1", "b/2", "c/3"}
	existing := []string{"b/2", "user-own[km]", "c/3"}
	missing, extra := SplitDiff(targets, existing)

	if len(missing) != 1 || missing[0] != "a/1" {
		t.Fatalf("missing 应为 [a/1]，实际 %v", missing)
	}
	if len(extra) != 1 || extra[0] != "user-own[km]" {
		t.Fatalf("extra 应为 [user-own[km]]，实际 %v", extra)
	}

	// 完全一致时不应产生任何操作
	missing2, extra2 := SplitDiff(targets, targets)
	if len(missing2) != 0 || len(extra2) != 0 {
		t.Fatalf("一致时不应有差异：missing=%v extra=%v", missing2, extra2)
	}
}

// TestSpecFor 验证模型元数据到 Trae 高级配置的映射。
func TestSpecFor(t *testing.T) {
	svc := NewService(newStore(t))

	glm := svc.SpecFor("ocg/glm-5.3")
	if glm.Context != 1000000 {
		t.Fatalf("上下文窗口应为 1000000，实际 %d", glm.Context)
	}
	if glm.Thinking == nil || !*glm.Thinking {
		t.Fatalf("有推理档位时应开启思考模式，实际 %v", glm.Thinking)
	}
	if glm.Vision != nil {
		t.Fatalf("没有视觉能力标注时不应设置图片输入，实际 %v", *glm.Vision)
	}

	sol := svc.SpecFor("km/gpt-5.6-sol")
	if sol.Context != 1050000 || sol.MaxOutput != 64000 {
		t.Fatalf("上下文/输出应分别为 1050000/64000，实际 %d/%d", sol.Context, sol.MaxOutput)
	}
	if sol.Vision == nil || !*sol.Vision {
		t.Fatalf("标注了 vision 能力时应设置支持图片输入，实际 %v", sol.Vision)
	}
	if sol.Thinking != nil {
		t.Fatalf("没有推理档位时不应设置思考模式，实际 %v", *sol.Thinking)
	}

	unknown := svc.SpecFor("km/hidden")
	if unknown.Context != 0 || unknown.Vision != nil || unknown.Thinking != nil {
		t.Fatalf("未知/停用模型应返回空设置，实际 %+v", unknown)
	}
}
