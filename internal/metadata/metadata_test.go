package metadata

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCache(t *testing.T, dir string, cf cacheFile) {
	t.Helper()
	b, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modelsdev.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasImage(mods []string) bool {
	for _, m := range mods {
		if m == "image" {
			return true
		}
	}
	return false
}

// 缓存里的输入模态必须能被读出，供上层生成「图片输入」能力。
func TestLookupReturnsInputModalities(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, cacheFile{
		Version: cacheVersion, FetchedAt: time.Now(),
		Providers: map[string]map[string]ModelMeta{
			"deepseek":    {"deepseek-flash": {InputModalities: []string{"text", "image"}}},
			"opencode-go": {"glm-5.3": {InputModalities: []string{"text"}}},
		},
	})
	c := New(dir)
	meta := c.Lookup("deepseek", "deepseek-flash")
	if meta == nil || !hasImage(meta.InputModalities) {
		t.Fatalf("未读出图片模态: %+v", meta)
	}
	if !c.HasNamespace("deepseek") {
		t.Fatalf("deepseek 应被视为可信命名空间")
	}
	if c.HasNamespace("km-aimodelhub") {
		t.Fatalf("未知预置类型不应被视为可信命名空间")
	}
}

// 旧结构版本的缓存必须触发一次刷新，否则新增字段永远读不到。
func TestStaleCacheVersionTriggersRefresh(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"deepseek":{"models":{"deepseek-flash":{"modalities":{"input":["text","image"]}}}}}`))
	}))
	defer ts.Close()
	old := sourceURL
	sourceURL = ts.URL
	defer func() { sourceURL = old }()

	dir := t.TempDir()
	writeCache(t, dir, cacheFile{
		Version: cacheVersion - 1, FetchedAt: time.Now(),
		Providers: map[string]map[string]ModelMeta{"deepseek": {"deepseek-flash": {}}},
	})
	c := New(dir)
	meta := c.Lookup("deepseek", "deepseek-flash")
	if hits == 0 {
		t.Fatalf("缓存版本过期时未刷新")
	}
	if meta == nil || !hasImage(meta.InputModalities) {
		t.Fatalf("刷新后仍未拿到模态: %+v", meta)
	}
	// 刷新结果要落盘，供下次启动直接使用
	b, err := os.ReadFile(filepath.Join(dir, "modelsdev.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cf cacheFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	if cf.Version != cacheVersion || !hasImage(cf.Providers["deepseek"]["deepseek-flash"].InputModalities) {
		t.Fatalf("刷新的缓存未写入新字段: %+v", cf)
	}
}
