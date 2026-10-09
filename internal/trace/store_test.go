package trace

import (
	"testing"
	"time"
)

func TestStoreRingDropsOldest(t *testing.T) {
	s := NewStore()
	for i := 0; i < Capacity+10; i++ {
		s.Add(Entry{Time: time.Now(), Protocol: "responses", RequestModel: "m", Body: []byte{byte(i)}})
	}
	list := s.List()
	if len(list) != Capacity {
		t.Fatalf("容量上限失效：%d", len(list))
	}
	// List 为新→旧；最新一条 ID 应为 Capacity+10，最旧一条应被丢弃。
	if list[0].ID != Capacity+10 {
		t.Fatalf("最新记录不是队首：%d", list[0].ID)
	}
	if list[len(list)-1].ID != 11 {
		t.Fatalf("最旧记录未被丢弃：%d", list[len(list)-1].ID)
	}
}

func TestStoreCompleteAndGet(t *testing.T) {
	s := NewStore()
	id := s.Add(Entry{Time: time.Now(), Protocol: "chat", RequestModel: "m", Body: []byte(`{}`)})
	s.Complete(id, 200, 42)
	e, ok := s.Get(id)
	if !ok {
		t.Fatal("记录未找到")
	}
	if e.Status != 200 || e.DurationMS != 42 {
		t.Fatalf("回填失败：status=%d ms=%d", e.Status, e.DurationMS)
	}
	if string(e.Body) != "{}" {
		t.Fatalf("请求体丢失：%q", e.Body)
	}
}

func TestStoreClear(t *testing.T) {
	s := NewStore()
	s.Add(Entry{Protocol: "responses"})
	s.Clear()
	if len(s.List()) != 0 {
		t.Fatal("清空后仍有记录")
	}
}
