package dsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llm-switch/internal/config"
)

func newStore(t *testing.T) *config.Store {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ctx := 128000
	out := 8192
	err = store.Update(func(c *config.Config) error {
		if _, err := c.AddProvider(config.ProviderDraft{
			ID: "ds", Name: "DeepSeek", BaseURL: "https://api.deepseek.com",
			Protocols: []string{"chat"}, Auth: config.AuthDraft{Type: "bearer", APIKey: "sk-x"},
		}); err != nil {
			return err
		}
		if _, err := c.AddModel("ds", config.ModelDraft{
			ID: "deepseek-chat", Name: "deepseek-chat", ContextWindow: &ctx, MaxOutputTokens: &out,
			Capabilities: []string{"tools", "vision"},
			Reasoning:    &config.Reasoning{DefaultLevel: "high", Levels: []string{"none", "low", "high"}},
		}); err != nil {
			return err
		}
		if _, err := c.AddModel("ds", config.ModelDraft{ID: "plain"}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return store
}

func TestBuildPlanEmptyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	store := newStore(t)
	svc := NewService(store)

	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Profile != ProfileDesktop {
		t.Fatalf("profile = %q, want desktop", plan.Profile)
	}
	wantPath := filepath.Join(home, "profiles", "desktop", PatchFilename)
	if plan.Path != wantPath {
		t.Fatalf("path = %q, want %q", plan.Path, wantPath)
	}
	for _, want := range []string{
		"id: llm-pi-ai",
		"name: '@deepseek-ai/dsh-llm-pi-ai'",
		"llm-switch:",
		"api: openai-completions",
		"baseURL: http://127.0.0.1:8317/v1",
		"Authorization: Bearer llm-switch-local",
		"id: ds/deepseek-chat",
		"contextWindow: 128000",
		"maxTokens: 8192",
		"input: [text, image]",
		"reasoningEfforts:",
		"id: ds/plain",
		"input: [text]",
	} {
		if !strings.Contains(plan.Content, want) {
			t.Fatalf("generated content missing %q\n%s", want, plan.Content)
		}
	}
	if len(plan.Models) != 2 {
		t.Fatalf("models = %v", plan.Models)
	}
	t.Logf("generated:\n%s", plan.Content)
	if err := svc.Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !svc.Connected(ProfileDesktop) {
		t.Fatal("want connected after apply")
	}
}

func TestBuildPlanPreservesExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	store := newStore(t)
	svc := NewService(store)

	patchDir := filepath.Join(home, "profiles", "desktop")
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `# 我的配置
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      anthropic:
        apiKeyEnv: ANTHROPIC_API_KEY
- id: agent-loop
  config:
    model: !!js process.env.DSH_SPEC_MODEL
`
	if err := os.WriteFile(filepath.Join(patchDir, PatchFilename), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	for _, want := range []string{"# 我的配置", "anthropic:", "apiKeyEnv: ANTHROPIC_API_KEY", "!!js process.env.DSH_SPEC_MODEL", "llm-switch:"} {
		if !strings.Contains(plan.Content, want) {
			t.Fatalf("merged content missing %q\n%s", want, plan.Content)
		}
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	st, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Connected || len(st.Models) != 2 {
		t.Fatalf("status = %+v", st)
	}
	if len(st.OtherProviders) != 1 || st.OtherProviders[0] != "anthropic" {
		t.Fatalf("other providers = %v", st.OtherProviders)
	}
}

func TestDisconnectKeepsOtherProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	store := newStore(t)
	svc := NewService(store)

	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	// 模拟 dsh 模型页往同一份文档里写了另一个 provider。
	patch := ResolvePaths(ProfileDesktop).Patch
	b, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	merged := strings.Replace(string(b), "llm-switch:", "anthropic:\n        apiKeyEnv: ANTHROPIC_API_KEY\n      llm-switch:", 1)
	if err := os.WriteFile(patch, []byte(merged), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.Disconnect(ProfileDesktop); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	out, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "llm-switch") {
		t.Fatalf("llm-switch provider still present\n%s", out)
	}
	if !strings.Contains(string(out), "anthropic") {
		t.Fatalf("other provider removed\n%s", out)
	}
}

func TestDisconnectRemovesOwnEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	store := newStore(t)
	svc := NewService(store)

	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect(ProfileDesktop); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(ResolvePaths(ProfileDesktop).Patch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "llm-pi-ai") || strings.Contains(string(out), "llm-switch") {
		t.Fatalf("entry not removed\n%s", out)
	}
}

func TestMergeIntoInsertedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	store := newStore(t)
	svc := NewService(store)

	patchDir := filepath.Join(home, "profiles", "desktop")
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `- insert:
    - id: llm-pi-ai
      name: '@deepseek-ai/dsh-llm-pi-ai'
      config:
        providers:
          anthropic:
            apiKeyEnv: ANTHROPIC_API_KEY
`
	if err := os.WriteFile(filepath.Join(patchDir, PatchFilename), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.BuildPlan(Overrides{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if strings.Count(plan.Content, "id: llm-pi-ai") != 1 {
		t.Fatalf("should merge into inserted entry, got\n%s", plan.Content)
	}
	if !strings.Contains(plan.Content, "anthropic:") || !strings.Contains(plan.Content, "llm-switch:") {
		t.Fatalf("merged content wrong\n%s", plan.Content)
	}
}
