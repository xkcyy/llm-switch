package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats", "usage.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.Add(Entry{Time: now, ProviderID: "p", ProviderName: "P", ModelID: "m", ModelName: "M", Caller: "Codex",
		OK: true, HasUsage: true, Input: 10, Output: 5, Total: 15, Cached: 2})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	tot := reopened.Totals(now.Add(-time.Hour), now.Add(time.Hour))
	if tot.Requests != 1 || tot.OK != 1 || tot.Input != 10 || tot.Output != 5 || tot.Total != 15 || tot.Cached != 2 {
		t.Fatalf("重启后用量丢失：%+v", tot)
	}
	res := reopened.Query(Query{From: now.Add(-time.Hour), To: now.Add(time.Hour), Granularity: GranDay, Group: GroupModel})
	if len(res.Groups) != 1 || res.Groups[0].ModelName != "M" || res.Groups[0].ProviderName != "P" {
		t.Fatalf("名称快照未持久化：%+v", res.Groups)
	}
}

// TestStorePrunesOldHourBuckets 小时桶超保留期清理，日桶永久保留。
func TestStorePrunesOldHourBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.AddDate(0, 0, -8)
	s.Add(Entry{Time: old, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Input: 1, Output: 1, Total: 2})
	s.Add(Entry{Time: now, ProviderID: "p", ModelID: "m", OK: true, HasUsage: true, Input: 3, Output: 3, Total: 6})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	reopened.mu.Lock()
	hours, days := len(reopened.hours), len(reopened.days)
	reopened.mu.Unlock()
	if hours != 1 {
		t.Fatalf("小时桶应只留 1 个（超期清理），实际 %d", hours)
	}
	if days != 2 {
		t.Fatalf("日桶应永久保留 2 个，实际 %d", days)
	}
}
