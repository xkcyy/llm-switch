package usage

import (
	"testing"
	"time"
)

// TestStoreSeparatesSameModelAcrossProviders 同名模型挂在不同供应商下必须分开落桶。
func TestStoreSeparatesSameModelAcrossProviders(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Add(Entry{Time: now, ProviderID: "km", ProviderName: "KmAiModelHub", ModelID: "glm-5.3", ModelName: "GLM-5.3", Caller: "Codex", OK: true, HasUsage: true, Input: 100, Output: 20, Total: 120})
	s.Add(Entry{Time: now, ProviderID: "ocg", ProviderName: "OpenCode Go", ModelID: "glm-5.3", ModelName: "GLM-5.3", Caller: "OpenCode", OK: true, HasUsage: true, Input: 7, Output: 3, Total: 10})

	if len(s.days) != 2 {
		t.Fatalf("同名模型应落 2 个桶，实际 %d", len(s.days))
	}
	tot := s.Totals(now.Add(-time.Hour), now.Add(time.Hour))
	if tot.Requests != 2 || tot.Input != 107 || tot.Output != 23 || tot.Total != 130 {
		t.Fatalf("合计错误：%+v", tot)
	}
	res := s.Query(Query{From: now.Add(-time.Hour), To: now.Add(time.Hour), Granularity: GranDay, Group: GroupModel})
	if len(res.Groups) != 2 || res.Groups[0].ModelName != "GLM-5.3" {
		t.Fatalf("模型名快照错误：%+v", res.Groups)
	}
}

// TestStoreCountsMissingOnlyWhenOK 只有成功且无用量的调用才计入 missing_usage。
func TestStoreCountsMissingOnlyWhenOK(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Add(Entry{Time: now, ProviderID: "p", ModelID: "m", OK: true, HasUsage: false})
	s.Add(Entry{Time: now, ProviderID: "p", ModelID: "m", OK: false, HasUsage: false})
	s.Add(Entry{Time: now, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Input: 3, Output: 1, Total: 4})

	tot := s.Totals(now.Add(-time.Hour), now.Add(time.Hour))
	if tot.Requests != 3 || tot.OK != 2 || tot.Err != 1 {
		t.Fatalf("requests/ok/err = %d/%d/%d", tot.Requests, tot.OK, tot.Err)
	}
	if tot.MissingUsage != 1 {
		t.Fatalf("missing_usage = %d，期望 1", tot.MissingUsage)
	}
	if tot.Total != 4 {
		t.Fatalf("total_tokens = %d，期望 4", tot.Total)
	}
}

// TestStoreTotalsRespectsRange 汇总只覆盖查询区间。
func TestStoreTotalsRespectsRange(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Add(Entry{Time: now, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Input: 1, Output: 1, Total: 2})
	s.Add(Entry{Time: now.AddDate(0, 0, -10), ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Input: 5, Output: 5, Total: 10})

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today := s.Totals(dayStart, dayStart)
	if today.Requests != 1 || today.Total != 2 {
		t.Fatalf("今日合计错误：%+v", today)
	}
}
