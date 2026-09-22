package trae

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"llm-switch/internal/cdp"
)

// 这些用例需要「Trae 正在运行且调试端口可用」的真实环境，默认跳过。
// 开启方式：$env:TRAE_E2E=1; go test ./internal/trae -run E2E -v
func requireTrae(t *testing.T, ctx context.Context) Paths {
	t.Helper()
	if os.Getenv("TRAE_E2E") == "" {
		t.Skip("未设置 TRAE_E2E=1，跳过需要真实 Trae 的端到端用例")
	}
	paths := DefaultPaths()
	if !paths.Installed() {
		t.Skipf("未找到 Trae 安装：%s", paths.Exe)
	}
	if !cdp.Alive(ctx, paths.DebugPort()) {
		t.Skipf("Trae 调试端口 %d 不可用", paths.DebugPort())
	}
	return paths
}

// TestE2EDumpRows 打印设置页表格里每一行的原始识别结果（排查用）。
func TestE2EDumpRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	paths := requireTrae(t, ctx)

	drv, err := attachDriver(ctx, paths.DebugPort())
	if err != nil {
		t.Fatalf("连接 Trae 失败：%v", err)
	}
	defer drv.Close()
	if err := drv.EnsureModelsPage(ctx); err != nil {
		t.Fatalf("打开模型设置页失败：%v", err)
	}
	rows, err := drv.readRows(ctx)
	if err != nil {
		t.Fatalf("读行失败：%v", err)
	}
	t.Logf("共 %d 行", len(rows))
	for i, r := range rows {
		t.Logf("  [%02d] name=%-42q vendor=%-28q delete=%v", i, r.Name, r.Vendor, len(r.Del) == 2)
	}
	all, _ := drv.ReadCustomModels(ctx)
	t.Logf("判定为自定义模型：%d 个：%v", len(all), all)
}

// TestE2EScan 验证：连上 Trae → 打开模型设置页 → 读出自定义模型列表。
func TestE2EScan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	paths := requireTrae(t, ctx)

	drv, err := attachDriver(ctx, paths.DebugPort())
	if err != nil {
		t.Fatalf("连接 Trae 失败：%v", err)
	}
	defer drv.Close()

	if err := drv.EnsureModelsPage(ctx); err != nil {
		t.Fatalf("打开模型设置页失败：%v", err)
	}
	models, err := drv.ReadCustomModels(ctx)
	if err != nil {
		t.Fatalf("读取自定义模型失败：%v", err)
	}
	t.Logf("读到的自定义模型 %d 个：%s", len(models), strings.Join(models, ", "))
	if len(models) == 0 {
		t.Log("提示：当前没有自定义模型，这本身不算失败")
	}
}

// TestE2EDeleteTarget 维护者工具：删除 TRAE_E2E_TARGET 指定的自定义模型。
// 用于验证「页面同步会把缺失的模型补回来」这条主路径。
func TestE2EDeleteTarget(t *testing.T) {
	target := os.Getenv("TRAE_E2E_TARGET")
	if target == "" {
		t.Skip("未设置 TRAE_E2E_TARGET，跳过")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	paths := requireTrae(t, ctx)

	drv, err := attachDriver(ctx, paths.DebugPort())
	if err != nil {
		t.Fatalf("连接 Trae 失败：%v", err)
	}
	defer drv.Close()
	if err := drv.EnsureModelsPage(ctx); err != nil {
		t.Fatalf("打开模型设置页失败：%v", err)
	}
	if err := drv.DeleteModel(ctx, target); err != nil {
		t.Fatalf("删除 %s 失败：%v", target, err)
	}
	models, err := drv.ReadCustomModels(ctx)
	if err != nil {
		t.Fatalf("删除后读取失败：%v", err)
	}
	for _, m := range models {
		if m == target {
			t.Fatalf("删除后仍存在 %s", target)
		}
	}
	t.Logf("已从 Trae 删除 %s，剩余 %d 个自定义模型", target, len(models))
}

// TestE2EAddDeleteRoundTrip 验证：走完整界面流程添加一个自定义模型，再删除它。
// 用一个代理不认识的模型 ID，正好覆盖「连通性测试失败 → 直接保存」这条兜底路径。
func TestE2EAddDeleteRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	paths := requireTrae(t, ctx)

	const probe = "zz/e2e-probe"

	drv, err := attachDriver(ctx, paths.DebugPort())
	if err != nil {
		t.Fatalf("连接 Trae 失败：%v", err)
	}
	defer drv.Close()

	if err := drv.EnsureModelsPage(ctx); err != nil {
		t.Fatalf("打开模型设置页失败：%v", err)
	}
	// 先清理可能残留的探针，保证用例可重复执行
	if err := drv.DeleteModel(ctx, probe); err != nil {
		t.Logf("无残留探针（可忽略）：%v", err)
	}
	before, err := drv.ReadCustomModels(ctx)
	if err != nil {
		t.Fatalf("读取自定义模型失败：%v", err)
	}
	for _, m := range before {
		if m == probe {
			t.Fatalf("探针仍存在，环境不干净：%v", before)
		}
	}

	svc := &Service{paths: paths}
	res, err := drv.AddModel(ctx, ModelSpec{ID: probe, Context: 200000, MaxOutput: 16000}, "http://127.0.0.1:8317/v1", PlaceholderKey, addTimeout)
	if err != nil {
		t.Fatalf("添加失败：%v（结果 %s %s）", err, res.Status, res.Detail)
	}
	t.Logf("添加结果：%s %s", res.Status, res.Detail)
	if res.Status != "added" && res.Status != "saved_directly" {
		t.Fatalf("预期 added 或 saved_directly，实际 %s", res.Status)
	}
	_ = svc

	after, err := drv.ReadCustomModels(ctx)
	if err != nil {
		t.Fatalf("二次读取失败：%v", err)
	}
	found := false
	for _, m := range after {
		if m == probe {
			found = true
		}
	}
	if !found {
		t.Fatalf("添加后列表里没有 %s：%v", probe, after)
	}
	if len(after) != len(before)+1 {
		t.Logf("提醒：列表数量 from %d to %d（可能并发变动）", len(before), len(after))
	}

	if err := drv.DeleteModel(ctx, probe); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	final, err := drv.ReadCustomModels(ctx)
	if err != nil {
		t.Fatalf("删除后读取失败：%v", err)
	}
	for _, m := range final {
		if m == probe {
			t.Fatalf("删除后仍存在 %s：%v", probe, final)
		}
	}
	t.Logf("已删除 %s，剩余 %d 个自定义模型", probe, len(final))
}
