package trae

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeDriver 是驱动 seam 的内存实现，用来验证接入编排的行为。
// 它复刻真机驱动的两条关键事实：新加的模型排在列表最前面；删除按显示名移除。
type fakeDriver struct {
	models    []string
	addStatus map[string]string
	addErr    map[string]error
	delErr    map[string]error
	readErr   error
	calls     []string
}

func (f *fakeDriver) Close() {}

func (f *fakeDriver) EnsureModelsPage(context.Context) error { return nil }

func (f *fakeDriver) ReadCustomModels(context.Context) ([]string, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return append([]string(nil), f.models...), nil
}

func (f *fakeDriver) AddModel(_ context.Context, spec ModelSpec, _, _ string, _ time.Duration) (AddResult, error) {
	f.calls = append(f.calls, "add:"+spec.ID)
	if err := f.addErr[spec.ID]; err != nil {
		return AddResult{Status: "failed"}, err
	}
	f.models = append([]string{spec.ID}, f.models...)
	if st := f.addStatus[spec.ID]; st != "" {
		return AddResult{Status: st}, nil
	}
	return AddResult{Status: "added"}, nil
}

func (f *fakeDriver) DeleteModel(_ context.Context, display string) error {
	f.calls = append(f.calls, "del:"+display)
	if err := f.delErr[display]; err != nil {
		return err
	}
	kept := make([]string, 0, len(f.models))
	for _, m := range f.models {
		if m != display {
			kept = append(kept, m)
		}
	}
	f.models = kept
	return nil
}

func calls(f *fakeDriver, prefix string) []string {
	out := []string{}
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// 补齐缺失时按逆序添加，最终顺序等于配置顺序（Trae 新加的排最前）。
func TestSyncModelsAddsMissingInReverseOrder(t *testing.T) {
	targets := []string{"ocg/a", "ocg/b", "km/c"}
	drv := &fakeDriver{}
	svc := NewService(newStore(t))

	var msg string
	svc.syncModels(context.Background(), drv, targets, []string{}, func(m string) { msg = m }, func(m string) { t.Fatalf("不应失败: %s", m) })

	want := []string{"add:km/c", "add:ocg/b", "add:ocg/a"}
	if got := calls(drv, "add:"); !reflect.DeepEqual(got, want) {
		t.Fatalf("添加顺序应为逆序 %v，实际 %v", want, got)
	}
	if !reflect.DeepEqual(drv.models, targets) {
		t.Fatalf("最终列表应等于配置顺序 %v，实际 %v", targets, drv.models)
	}
	if !strings.Contains(msg, "已与本地代理一致") {
		t.Fatalf("结论应报告已一致: %s", msg)
	}
}

// 先删多余再补缺失，避免两者交错。
// 注意：新加的模型排在最前面，既有项位置不变，因此补齐后顺序通常不等于配置顺序，
// 结论里应如实提示顺序未对齐（这是真机行为，不是缺陷）。
func TestSyncModelsDeletesBeforeAdds(t *testing.T) {
	targets := []string{"ocg/a", "ocg/b"}
	existing := []string{"stale/x", "ocg/a"}
	drv := &fakeDriver{models: append([]string(nil), existing...)}
	svc := NewService(newStore(t))

	var msg string
	svc.syncModels(context.Background(), drv, targets, existing, func(m string) { msg = m }, func(m string) { t.Fatalf("不应失败: %s", m) })

	joined := strings.Join(drv.calls, ",")
	if joined != "del:stale/x,add:ocg/b" {
		t.Fatalf("调用顺序应为先删后加，实际 %v", drv.calls)
	}
	want := []string{"ocg/b", "ocg/a"}
	if !reflect.DeepEqual(drv.models, want) {
		t.Fatalf("新增项应排在最前、既有项保持原位 %v，实际 %v", want, drv.models)
	}
	if !strings.Contains(msg, "顺序未对齐") {
		t.Fatalf("结论应提示顺序未对齐: %s", msg)
	}
}

// 已经一致且顺序正确时不产生任何界面操作。
func TestSyncModelsAlreadyAligned(t *testing.T) {
	targets := []string{"ocg/a", "km/c"}
	drv := &fakeDriver{models: append([]string(nil), targets...)}
	svc := NewService(newStore(t))

	var msg string
	svc.syncModels(context.Background(), drv, targets, targets, func(m string) { msg = m }, func(m string) { t.Fatalf("不应失败: %s", m) })

	if len(drv.calls) != 0 {
		t.Fatalf("一致时不应有界面操作，实际 %v", drv.calls)
	}
	if !strings.Contains(msg, "顺序按供应商排列") {
		t.Fatalf("结论应报告顺序已对齐: %s", msg)
	}
}

// 集合一致但顺序不对时如实提示，不谎报「一切正常」。
func TestSyncModelsReportsOrderMismatch(t *testing.T) {
	targets := []string{"ocg/a", "km/c"}
	existing := []string{"km/c", "ocg/a"}
	drv := &fakeDriver{models: append([]string(nil), existing...)}
	svc := NewService(newStore(t))

	var msg string
	svc.syncModels(context.Background(), drv, targets, existing, func(m string) { msg = m }, func(m string) { t.Fatalf("不应失败: %s", m) })

	if len(drv.calls) != 0 {
		t.Fatalf("顺序问题不该触发增删，实际 %v", drv.calls)
	}
	if !strings.Contains(msg, "顺序未对齐") {
		t.Fatalf("结论应提示顺序未对齐: %s", msg)
	}
}

// 添加失败时如实报告仍缺，不吞错。
func TestSyncModelsReportsStillMissing(t *testing.T) {
	targets := []string{"ocg/a"}
	drv := &fakeDriver{addErr: map[string]error{"ocg/a": errTestAdd}}
	svc := NewService(newStore(t))

	var msg string
	svc.syncModels(context.Background(), drv, targets, []string{}, func(m string) { msg = m }, func(m string) { t.Fatalf("不应走失败回调: %s", m) })

	if !strings.Contains(msg, "仍缺 1 个") {
		t.Fatalf("结论应报告仍缺: %s", msg)
	}
}

// 重新排序：删掉全部代理模型后按逆序重建，最终顺序等于配置顺序。
func TestReorderModelsRebuildsOrder(t *testing.T) {
	targets := []string{"ocg/a", "ocg/b", "km/c"}
	existing := []string{"km/c", "ocg/b"}
	drv := &fakeDriver{models: append([]string(nil), existing...)}
	svc := NewService(newStore(t))

	svc.reorderModels(context.Background(), drv, targets, existing, func(string) {}, func(m string) { t.Fatalf("不应失败: %s", m) })

	if !reflect.DeepEqual(drv.models, targets) {
		t.Fatalf("重建后顺序应等于配置顺序 %v，实际 %v", targets, drv.models)
	}
	if len(calls(drv, "del:")) != len(existing) {
		t.Fatalf("应先删掉原有代理模型，实际 %v", drv.calls)
	}
	if len(calls(drv, "add:")) != len(targets) {
		t.Fatalf("应新增全部目标模型，实际 %v", drv.calls)
	}
}

// 没有可用模型时不触碰界面。
func TestReorderModelsWithoutTargetsFails(t *testing.T) {
	drv := &fakeDriver{}
	svc := NewService(newStore(t))

	var failed string
	svc.reorderModels(context.Background(), drv, []string{}, []string{}, func(string) {}, func(m string) { failed = m })

	if failed == "" {
		t.Fatal("没有目标模型时应走失败回调")
	}
	if len(drv.calls) != 0 {
		t.Fatalf("不应产生界面操作，实际 %v", drv.calls)
	}
}

// 逐个处理的结果映射：added / saved_directly 计入，exists 记为 present，失败计入 failed。
func TestApplyAddsResultMapping(t *testing.T) {
	drv := &fakeDriver{
		addStatus: map[string]string{"b": "saved_directly", "c": "exists"},
		addErr:    map[string]error{"d": errTestAdd},
	}
	svc := NewService(newStore(t))
	svc.job = &Job{State: "running"}

	added, saved, failed := svc.applyAdds(context.Background(), drv, []string{"a", "b", "c", "d"}, 0)
	if added != 1 || saved != 1 || failed != 1 {
		t.Fatalf("计数应为 added=1 saved=1 failed=1，实际 %d/%d/%d", added, saved, failed)
	}
	got := map[string]string{}
	for _, item := range svc.job.Items {
		got[item.Model] = item.Result
	}
	want := map[string]string{"a": "added", "b": "saved_directly", "c": "present", "d": "failed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("结果映射应为 %v，实际 %v", want, got)
	}
}

type addError struct{}

func (addError) Error() string { return "添加失败" }

var errTestAdd = addError{}
