package admin

// 行为锁定测试：这些用例固定「供应商 / 模型写入」当前的对外行为（含已知怪癖），
// 供把规则搬进 config 时做无行为变化的回归验证。它们描述现状，不描述理想状态。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"llm-switch/internal/codex"
	"llm-switch/internal/config"
	"llm-switch/internal/opencode"
	"llm-switch/internal/provider"
	"llm-switch/internal/proxy"
	"llm-switch/internal/trae"
)

func newTestServer(t *testing.T) (*http.ServeMux, *config.Store) {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	index := &proxy.Holder{}
	index.Store(proxy.BuildIndex(&config.Config{}))
	svc := New(store, provider.NewService(store, nil), proxy.NewServer(store, index),
		codex.NewService(store), opencode.NewService(store), trae.NewService(store),
		fstest.MapFS{}, t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.registerAPI(mux)
	return mux, store
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func decodeInto[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, body)
	}
	return v
}

func apiError(t *testing.T, body []byte) string {
	t.Helper()
	var e map[string]string
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("解析错误响应失败: %v (%s)", err, body)
	}
	return e["error"]
}

func defaultModel(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	code, body := do(t, mux, http.MethodGet, "/api/settings", "")
	if code != 200 {
		t.Fatalf("读取设置失败: %d %s", code, body)
	}
	st := decodeInto[struct {
		Settings config.Settings `json:"settings"`
	}](t, body)
	return st.Settings.DefaultModel
}

// 新建供应商：ID 归一化、协议去重、地址去尾斜杠、密钥脱敏、启用默认 true。
func TestCreateProviderNormalizesInput(t *testing.T) {
	mux, _ := newTestServer(t)
	code, body := do(t, mux, http.MethodPost, "/api/providers", `{
		"name":"My Provider","id":" My Provider/ ","base_url":"https://api.example.com/v1/",
		"protocols":["responses","chat","responses"],"api_key":"sk-abcdefgh"
	}`)
	if code != 200 {
		t.Fatalf("创建应成功: %d %s", code, body)
	}
	p := decodeInto[providerDTO](t, body)
	if p.ID != "my-provider" {
		t.Fatalf("ID 归一化应为 my-provider，实际 %q", p.ID)
	}
	if p.Protocol != "responses" || len(p.Protocols) != 2 || p.Protocols[0] != "responses" || p.Protocols[1] != "chat" {
		t.Fatalf("协议应去重且 protocol 等于首项，实际 %q %v", p.Protocol, p.Protocols)
	}
	if p.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("地址应去掉尾部斜杠，实际 %q", p.BaseURL)
	}
	if p.Auth.Type != "bearer" || p.Auth.APIKeyMasked != "sk-a…efgh" {
		t.Fatalf("认证应为 bearer 且脱敏，实际 %+v", p.Auth)
	}
	if !p.Enabled {
		t.Fatal("未显式指定时默认启用")
	}
}

// 未填 ID 时由名称推导（全中文名称保留中文）。
func TestCreateProviderDerivesIDFromName(t *testing.T) {
	mux, _ := newTestServer(t)
	code, body := do(t, mux, http.MethodPost, "/api/providers",
		`{"name":"我的 供应商","base_url":"https://api.example.com","protocols":["chat"]}`)
	if code != 200 {
		t.Fatalf("创建应成功: %d %s", code, body)
	}
	if got := decodeInto[providerDTO](t, body).ID; got != "我的-供应商" {
		t.Fatalf("应由名称推导 ID，实际 %q", got)
	}
}

func TestCreateProviderRejectsBadInput(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"重名", `{"name":"A","base_url":"https://x","protocols":["chat"]}`, "供应商名称 \"A\" 已存在"},
		{"重 ID", `{"name":"B","id":"a","base_url":"https://x","protocols":["chat"]}`, "供应商 ID \"a\" 已存在，请换一个"},
		{"空名称", `{"name":"","base_url":"https://x","protocols":["chat"]}`, "供应商名称不能为空"},
		{"地址非 http", `{"name":"C","base_url":"ftp://x","protocols":["chat"]}`, "接口地址必须以 http(s):// 开头"},
		{"未知协议", `{"name":"D","base_url":"https://x","protocols":["bogus"]}`, "协议 \"bogus\" 无效，必须是 chat、responses 或 messages"},
		{"无协议", `{"name":"E","base_url":"https://x"}`, "请至少选择一个协议"},
		{"未知认证类型", `{"name":"F","base_url":"https://x","protocols":["chat"],"auth":{"type":"weird"}}`, "认证类型不支持"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mux, _ := newTestServer(t)
			if code, body := do(t, mux, http.MethodPost, "/api/providers", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"]}`); code != 200 {
				t.Fatalf("准备数据失败: %d %s", code, body)
			}
			code, body := do(t, mux, http.MethodPost, "/api/providers", c.body)
			if code != 400 {
				t.Fatalf("应返回 400，实际 %d %s", code, body)
			}
			if got := apiError(t, body); got != c.want {
				t.Fatalf("错误信息应为 %q，实际 %q", c.want, got)
			}
		})
	}
}

// 已知怪癖：扩展名在写入路径不去空、不去重、不去空白（只有启动自愈才清理）。
func TestCreateProviderKeepsExtensionsVerbatim(t *testing.T) {
	mux, _ := newTestServer(t)
	code, body := do(t, mux, http.MethodPost, "/api/providers",
		`{"name":"A","base_url":"https://x","protocols":["chat"],"extensions":["  pad  ","","dup","dup"]}`)
	if code != 200 {
		t.Fatalf("创建应成功: %d %s", code, body)
	}
	got := decodeInto[providerDTO](t, body).Extensions
	want := []string{"  pad  ", "", "dup", "dup"}
	if len(got) != len(want) {
		t.Fatalf("扩展名应原样保留 %v，实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("扩展名应原样保留 %v，实际 %v", want, got)
		}
	}
}

// 认证头名取自顶层 header 字段（不是 auth.header）；缺省时回退 x-api-key。
func TestCreateProviderAuthHeaderSource(t *testing.T) {
	mux, _ := newTestServer(t)
	code, body := do(t, mux, http.MethodPost, "/api/providers",
		`{"name":"A","base_url":"https://x","protocols":["chat"],"api_key":"k","auth":{"type":"api_key_header"},"header":"X-Custom"}`)
	if code != 200 {
		t.Fatalf("创建应成功: %d %s", code, body)
	}
	if got := decodeInto[providerDTO](t, body).Auth.Header; got != "X-Custom" {
		t.Fatalf("头名应取自顶层 header 字段，实际 %q", got)
	}

	code, body = do(t, mux, http.MethodPost, "/api/providers",
		`{"name":"B","base_url":"https://x","protocols":["chat"],"api_key":"k","auth":{"type":"custom"}}`)
	if code != 200 {
		t.Fatalf("创建应成功: %d %s", code, body)
	}
	if got := decodeInto[providerDTO](t, body).Auth.Header; got != "x-api-key" {
		t.Fatalf("缺省头名应为 x-api-key，实际 %q", got)
	}
}

// 供应商 ID 只有一条归一化规则：写入路径与启动自愈走同一个函数，同一输入必得同一结果。
func TestProviderIDNormalizationIsSingleRule(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a_b", "a_b"},
		{"a.b", "a.b"},
		{" My Provider/ ", "my-provider"},
		{"我的 供应商", "我的-供应商"},
		{"KmAiModelHub（企业）", "kmaimodelhub企业"},
		{"!@#", ""},
	}
	for _, c := range cases {
		if got := config.NormalizeProviderID(c.in); got != c.want {
			t.Fatalf("NormalizeProviderID(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestUpdateProviderRenamesIDAndCascades(t *testing.T) {
	mux, _ := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"DeepSeek","id":"ds","base_url":"https://x","protocols":["chat"]}`)
	if code, body := do(t, mux, http.MethodPost, "/api/providers/ds/models", `{"id":"chat"}`); code != 200 {
		t.Fatalf("准备模型失败: %d %s", code, body)
	}
	if got := defaultModel(t, mux); got != "ds/chat" {
		t.Fatalf("首个模型应自动成为默认模型，实际 %q", got)
	}

	code, body := do(t, mux, http.MethodPut, "/api/providers/ds",
		`{"name":"DeepSeek","id":"ds2","base_url":"https://x","protocols":["chat"]}`)
	if code != 200 {
		t.Fatalf("改名应成功: %d %s", code, body)
	}
	if got := decodeInto[providerDTO](t, body).ID; got != "ds2" {
		t.Fatalf("供应商 ID 应为 ds2，实际 %q", got)
	}
	// 模型归属与默认模型前缀级联更新
	code, body = do(t, mux, http.MethodGet, "/api/providers/ds2/models", "")
	if code != 200 {
		t.Fatalf("读取模型失败: %d %s", code, body)
	}
	models := decodeInto[[]modelDTO](t, body)
	if len(models) != 1 || models[0].ProviderID != "ds2" || models[0].Slug != "ds2/chat" {
		t.Fatalf("模型归属应级联到 ds2，实际 %+v", models)
	}
	if got := defaultModel(t, mux); got != "ds2/chat" {
		t.Fatalf("默认模型应级联为 ds2/chat，实际 %q", got)
	}
}

// 更新时密钥留空表示保留原 Key。
func TestUpdateProviderKeepsKeyWhenBlank(t *testing.T) {
	mux, _ := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers",
		`{"name":"A","id":"a","base_url":"https://x","protocols":["chat"],"api_key":"sk-abcdefgh"}`)
	code, body := do(t, mux, http.MethodPut, "/api/providers/a",
		`{"name":"A","id":"a","base_url":"https://x","protocols":["chat"],"api_key":""}`)
	if code != 200 {
		t.Fatalf("更新应成功: %d %s", code, body)
	}
	if got := decodeInto[providerDTO](t, body).Auth.APIKeyMasked; got != "sk-a…efgh" {
		t.Fatalf("留空应保留原 Key，实际 %q", got)
	}
	// 显式提交新 Key 时替换
	code, body = do(t, mux, http.MethodPut, "/api/providers/a",
		`{"name":"A","id":"a","base_url":"https://x","protocols":["chat"],"api_key":"sk-zzzzzzzz"}`)
	if code != 200 {
		t.Fatalf("更新应成功: %d %s", code, body)
	}
	if got := decodeInto[providerDTO](t, body).Auth.APIKeyMasked; got != "sk-z…zzzz" {
		t.Fatalf("提交新 Key 应替换，实际 %q", got)
	}
}

// 删除供应商连带删除其模型。
func TestDeleteProviderRemovesItsModels(t *testing.T) {
	mux, store := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"]}`)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"B","id":"b","base_url":"https://x","protocols":["chat"]}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m1"}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m2"}`)
	do(t, mux, http.MethodPost, "/api/providers/b/models", `{"id":"m3"}`)

	code, body := do(t, mux, http.MethodDelete, "/api/providers/a", "")
	if code != 200 {
		t.Fatalf("删除应成功: %d %s", code, body)
	}
	res := decodeInto[struct {
		OK            bool `json:"ok"`
		RemovedModels int  `json:"removed_models"`
	}](t, body)
	if !res.OK || res.RemovedModels != 2 {
		t.Fatalf("应连带删除 2 个模型，实际 %+v", res)
	}
	cfg := store.Snapshot()
	if len(cfg.Providers) != 1 || len(cfg.Models) != 1 || cfg.Models[0].ProviderID != "b" {
		t.Fatalf("剩余配置应为 B 及其 1 个模型，实际 %+v", cfg)
	}
}

// 删除默认模型所属的供应商后，默认模型立即失效并回退（此前要等下次启动自愈才修正）。
func TestDeleteProviderFixesDefaultModel(t *testing.T) {
	mux, _ := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"]}`)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"B","id":"b","base_url":"https://x","protocols":["chat"]}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m1"}`)
	do(t, mux, http.MethodPost, "/api/providers/b/models", `{"id":"m2"}`)
	if got := defaultModel(t, mux); got != "a/m1" {
		t.Fatalf("准备数据失败，默认模型应为 a/m1，实际 %q", got)
	}
	do(t, mux, http.MethodDelete, "/api/providers/a", "")
	if got := defaultModel(t, mux); got != "b/m2" {
		t.Fatalf("默认模型应回退到剩余可用模型 b/m2，实际 %q", got)
	}

	// 删掉最后一个可用模型后，默认模型清空
	do(t, mux, http.MethodDelete, "/api/providers/b", "")
	if got := defaultModel(t, mux); got != "" {
		t.Fatalf("没有可用模型时默认模型应清空，实际 %q", got)
	}
}

// 停用默认模型（或它所属的供应商）同样立即回退。
func TestDisableDefaultModelFixesDefaultModel(t *testing.T) {
	mux, _ := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"]}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m1"}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m2"}`)
	if got := defaultModel(t, mux); got != "a/m1" {
		t.Fatalf("准备数据失败，实际 %q", got)
	}
	if code, body := do(t, mux, http.MethodPut, "/api/providers/a/models/m1", `{"id":"m1","enabled":false}`); code != 200 {
		t.Fatalf("停用模型失败: %d %s", code, body)
	}
	if got := defaultModel(t, mux); got != "a/m2" {
		t.Fatalf("停用默认模型后应回退到 a/m2，实际 %q", got)
	}
	if code, body := do(t, mux, http.MethodPut, "/api/providers/a", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"],"enabled":false}`); code != 200 {
		t.Fatalf("停用供应商失败: %d %s", code, body)
	}
	if got := defaultModel(t, mux); got != "" {
		t.Fatalf("停用供应商后其模型不可用，默认模型应清空，实际 %q", got)
	}
}

func TestModelCreateDefaultsAndValidation(t *testing.T) {
	mux, _ := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"]}`)

	code, body := do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m1"}`)
	if code != 200 {
		t.Fatalf("创建模型应成功: %d %s", code, body)
	}
	m := decodeInto[modelDTO](t, body)
	if m.Name != "m1" || !m.Enabled || m.Slug != "a/m1" {
		t.Fatalf("名称应默认等于 ID、默认启用、slug 为 a/m1，实际 %+v", m)
	}
	if got := defaultModel(t, mux); got != "a/m1" {
		t.Fatalf("默认模型为空时应自动设为 a/m1，实际 %q", got)
	}

	cases := []struct{ name, body, want string }{
		{"重复", `{"id":"m1"}`, "模型 \"m1\" 已存在"},
		{"空 ID", `{"id":""}`, "模型 ID 不能为空"},
		{"未知协议", `{"id":"m9","protocol":"bogus"}`, "模型协议必须是 chat、responses、messages 或留空（跟随供应商）"},
		{"默认档位不在列表", `{"id":"m9","reasoning":{"default_level":"high","levels":["low"]}}`, "默认推理档位不在支持列表中"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := do(t, mux, http.MethodPost, "/api/providers/a/models", c.body)
			if code != 400 {
				t.Fatalf("应返回 400，实际 %d %s", code, body)
			}
			if got := apiError(t, body); got != c.want {
				t.Fatalf("错误信息应为 %q，实际 %q", c.want, got)
			}
		})
	}

	// 供应商不存在
	if code, body := do(t, mux, http.MethodPost, "/api/providers/nope/models", `{"id":"m9"}`); code != 400 || apiError(t, body) != "供应商不存在" {
		t.Fatalf("供应商不存在时应报错，实际 %d %s", code, body)
	}
}

func TestModelUpdateAndDelete(t *testing.T) {
	mux, _ := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"]}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m1"}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m2"}`)

	// 改名 + 清空名称回落到 ID
	code, body := do(t, mux, http.MethodPut, "/api/providers/a/models/m1", `{"id":"m1x","name":""}`)
	if code != 200 {
		t.Fatalf("更新模型应成功: %d %s", code, body)
	}
	if m := decodeInto[modelDTO](t, body); m.ID != "m1x" || m.Name != "m1x" || m.Slug != "a/m1x" {
		t.Fatalf("改名后名称应回落为 ID，实际 %+v", m)
	}
	// 改成已存在的 ID
	if code, body := do(t, mux, http.MethodPut, "/api/providers/a/models/m1x", `{"id":"m2"}`); code != 400 || apiError(t, body) != "模型 \"m2\" 已存在" {
		t.Fatalf("重名应报错，实际 %d %s", code, body)
	}
	// 不存在
	if code, body := do(t, mux, http.MethodPut, "/api/providers/a/models/nope", `{"id":"nope"}`); code != 400 || apiError(t, body) != "模型不存在" {
		t.Fatalf("不存在应报错，实际 %d %s", code, body)
	}
	// 删除只影响目标
	if code, body := do(t, mux, http.MethodDelete, "/api/providers/a/models/m1x", ""); code != 200 {
		t.Fatalf("删除应成功: %d %s", code, body)
	}
	code, body = do(t, mux, http.MethodGet, "/api/providers/a/models", "")
	models := decodeInto[[]modelDTO](t, body)
	if len(models) != 1 || models[0].ID != "m2" {
		t.Fatalf("删除后应只剩 m2，实际 %+v", models)
	}
	if code, body := do(t, mux, http.MethodDelete, "/api/providers/a/models/m1x", ""); code != 400 || apiError(t, body) != "模型不存在" {
		t.Fatalf("重复删除应报错，实际 %d %s", code, body)
	}
}

// 概览计数与供应商列表口径（供重构后比对）。
func TestOverviewAndProviderListShape(t *testing.T) {
	mux, _ := newTestServer(t)
	do(t, mux, http.MethodPost, "/api/providers", `{"name":"A","id":"a","base_url":"https://x","protocols":["chat"]}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m1"}`)
	do(t, mux, http.MethodPost, "/api/providers/a/models", `{"id":"m2","enabled":false}`)

	code, body := do(t, mux, http.MethodGet, "/api/overview", "")
	if code != 200 {
		t.Fatalf("概览应可读: %d %s", code, body)
	}
	ov := decodeInto[struct {
		Counts struct {
			Providers     int `json:"providers"`
			Models        int `json:"models"`
			EnabledModels int `json:"enabled_models"`
		} `json:"counts"`
	}](t, body)
	if ov.Counts.Providers != 1 || ov.Counts.Models != 2 || ov.Counts.EnabledModels != 1 {
		t.Fatalf("计数应为 1/2/1（enabled_models 只数可用模型），实际 %+v", ov.Counts)
	}

	code, body = do(t, mux, http.MethodGet, "/api/providers", "")
	list := decodeInto[[]providerDTO](t, body)
	if len(list) != 1 || list[0].ModelCount != 2 {
		t.Fatalf("供应商列表应带模型数量 2，实际 %+v", list)
	}
}
