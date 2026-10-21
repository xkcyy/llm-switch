package usage

import (
	"testing"
	"time"
)

func TestQueryGroupsByModel(t *testing.T) {
	s := NewStore()
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	s.Add(Entry{Time: day, ProviderID: "km", ProviderName: "KmAiModelHub", ModelID: "glm-5.3", ModelName: "GLM-5.3",
		Caller: "Codex", OK: true, HasUsage: true, Input: 100, Output: 20, Total: 120})
	s.Add(Entry{Time: day, ProviderID: "ocg", ProviderName: "OpenCode Go", ModelID: "glm-5.3", ModelName: "GLM-5.3",
		Caller: "OpenCode", OK: true, HasUsage: true, Input: 7, Output: 3, Total: 10})

	res := s.Query(Query{From: day, To: day, Granularity: GranDay, Group: GroupModel})
	if res.Totals.Requests != 2 || res.Totals.Total != 130 {
		t.Fatalf("合计错误：%+v", res.Totals)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("不同供应商下的同名模型应分两行，实际 %d", len(res.Groups))
	}
	if res.Groups[0].Total != 120 || res.Groups[0].ProviderName != "KmAiModelHub" || res.Groups[0].ModelName != "GLM-5.3" {
		t.Fatalf("分组行错误：%+v", res.Groups[0])
	}
	byGroup := map[string]int{}
	for _, p := range res.Series {
		byGroup[p.Group] = p.Total
	}
	if len(res.Series) != 2 || byGroup["km/glm-5.3"] != 120 || byGroup["ocg/glm-5.3"] != 10 {
		t.Fatalf("每个分组应各有一条序列：%+v", res.Series)
	}
}

func TestQueryFiltersByProviderAndGroupsByCaller(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Add(Entry{Time: now, ProviderID: "km", ModelID: "a", Caller: "Codex", OK: true, HasUsage: true, Total: 10})
	s.Add(Entry{Time: now, ProviderID: "km", ModelID: "a", Caller: "OpenCode", OK: true, HasUsage: true, Total: 5})
	s.Add(Entry{Time: now, ProviderID: "ocg", ModelID: "b", Caller: "Codex", OK: true, HasUsage: true, Total: 7})

	res := s.Query(Query{From: now, To: now, Granularity: GranDay, Group: GroupCaller, ProviderID: "km"})
	if res.Totals.Requests != 2 || res.Totals.Total != 15 {
		t.Fatalf("供应商筛选未生效：%+v", res.Totals)
	}
	if len(res.Groups) != 2 || res.Groups[0].Caller != "Codex" {
		t.Fatalf("按调用方分组错误：%+v", res.Groups)
	}
}

func TestQueryMonthGranularity(t *testing.T) {
	s := NewStore()
	now := time.Now()
	thisMonth := time.Date(now.Year(), now.Month(), 3, 0, 0, 0, 0, now.Location())
	lastMonth := thisMonth.AddDate(0, -1, 0)
	s.Add(Entry{Time: thisMonth, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Total: 10})
	s.Add(Entry{Time: lastMonth, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Total: 4})

	res := s.Query(Query{From: lastMonth, To: thisMonth, Granularity: GranMonth, Group: GroupModel})
	if len(res.Series) != 2 {
		t.Fatalf("按月应有两点，实际 %d", len(res.Series))
	}
	if res.Series[0].Period != lastMonth.Format("2006-01") || res.Series[0].Total != 4 {
		t.Fatalf("月度序列错误：%+v", res.Series)
	}
	if res.Totals.Total != 14 {
		t.Fatalf("合计错误：%+v", res.Totals)
	}
}

func TestQueryHourGranularity(t *testing.T) {
	s := NewStore()
	now := time.Now()
	hour := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
	s.Add(Entry{Time: hour, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Total: 3})

	res := s.Query(Query{From: hour, To: hour, Granularity: GranHour, Group: GroupModel})
	if len(res.Series) != 1 || res.Series[0].Period != hour.Format("2006-01-02T15") || res.Series[0].Total != 3 {
		t.Fatalf("小时序列错误：%+v", res.Series)
	}
}

func TestQueryMinuteGranularityAndDerivedMetrics(t *testing.T) {
	s := NewStore()
	now := time.Now()
	minute := now.Truncate(time.Minute)
	s.Add(Entry{
		Time: minute, ProviderID: "p", ProviderName: "Provider", ModelID: "m",
		OK: true, Outcome: OutcomeSuccess, HasUsage: true, Output: 10, Total: 10,
		HasTiming: true, TTFTMS: 200, DecodeMS: 1000,
	})
	s.Add(Entry{
		Time: minute, ProviderID: "p", ProviderName: "Provider", ModelID: "m",
		OK: false, Outcome: OutcomeAuthError,
	})
	s.Add(Entry{
		Time: minute, ProviderID: "p", ProviderName: "Provider", ModelID: "m",
		OK: false, Outcome: OutcomeClientCancel,
	})

	res := s.Query(Query{From: minute, To: minute, Granularity: GranMinute, Group: GroupProvider})
	if res.Totals.Requests != 3 || res.Totals.OK != 1 || res.Totals.Err != 2 ||
		res.Totals.AuthErr != 1 || res.Totals.ClientCancel != 1 {
		t.Fatalf("分类合计错误：%+v", res.Totals)
	}
	metrics := res.Totals.Derive()
	if metrics.SuccessRate == nil || *metrics.SuccessRate != 0.5 {
		t.Fatalf("成功率应为 1/(1+1)=0.5：%+v", metrics)
	}
	if metrics.Excluded != 1 || metrics.Cancelled != 1 {
		t.Fatalf("客户端取消应排除：%+v", metrics)
	}
	if metrics.TPS == nil || *metrics.TPS != 10 {
		t.Fatalf("TPS 应为 10：%+v", metrics)
	}
	if metrics.TTFTMS == nil || *metrics.TTFTMS != 200 {
		t.Fatalf("TTFT 应为 200ms：%+v", metrics)
	}
	if len(res.Groups) != 1 || res.Groups[0].LastPeriod != minute.Format(minuteLayout) {
		t.Fatalf("最后监测时段错误：%+v", res.Groups)
	}
}

func TestDeriveTreatsLegacyErrAsProviderFailure(t *testing.T) {
	m := (Totals{Requests: 2, OK: 1, Err: 1}).Derive()
	if m.Failed != 1 || m.SuccessRate == nil || *m.SuccessRate != 0.5 {
		t.Fatalf("旧数据失败分类错误：%+v", m)
	}
}

// TestQueryKeepsNameSnapshotPerPeriod 改名后，旧时段的历史仍显示当时的名字。
func TestQueryKeepsNameSnapshotPerPeriod(t *testing.T) {
	s := NewStore()
	now := time.Now()
	old := now.AddDate(0, 0, -5)
	s.Add(Entry{Time: old, ProviderID: "p", ProviderName: "P", ModelID: "m", ModelName: "旧名", OK: true, HasUsage: true, Total: 1})
	s.Add(Entry{Time: now, ProviderID: "p", ProviderName: "P", ModelID: "m", ModelName: "新名", OK: true, HasUsage: true, Total: 2})

	oldOnly := s.Query(Query{From: old, To: old, Granularity: GranDay, Group: GroupModel})
	if len(oldOnly.Groups) != 1 || oldOnly.Groups[0].ModelName != "旧名" {
		t.Fatalf("旧时段应显示旧名：%+v", oldOnly.Groups)
	}
	all := s.Query(Query{From: old, To: now, Granularity: GranDay, Group: GroupModel})
	if len(all.Groups) != 1 || all.Groups[0].ModelName != "新名" || all.Groups[0].Total != 3 {
		t.Fatalf("跨时段应取最新快照并合并合计：%+v", all.Groups)
	}
}
