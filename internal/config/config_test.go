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
