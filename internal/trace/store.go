// Package trace 保存最近若干条入站推理请求的原始请求体与元数据，
// 供管理界面按单条请求查看轨迹。不按会话合并，不落盘，重启即丢。
package trace

import (
	"sync"
	"time"
)

// Capacity 是内存里保留的入站请求条数上限；超过时丢弃最旧一条。
const Capacity = 50

// Entry 是一条入站请求的轨迹记录。Body 是原始入站请求体的副本。
type Entry struct {
	ID           int       `json:"id"`
	Time         time.Time `json:"time"`
	Source       string    `json:"source,omitempty"`
	Session      string    `json:"session,omitempty"`
	Protocol     string    `json:"protocol"`
	RequestModel string    `json:"request_model"`
	Provider     string    `json:"provider,omitempty"`
	Model        string    `json:"model,omitempty"`
	Status       int       `json:"status"`
	DurationMS   int64     `json:"duration_ms"`
	Bytes        int       `json:"bytes"`

	// 原始请求的传输信息，用于「原始请求」视图。
	Method string `json:"method,omitempty"`
	URL    string `json:"url,omitempty"`
	Proto  string `json:"proto,omitempty"`

	// Body 与 Headers 只在 Get/Raw 时返回，不进列表接口。
	Body    []byte   `json:"-"`
	Headers []Header `json:"-"`
}

// Header 是一条原始请求头；值已脱敏。
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Store 是固定容量的环形记录：读多写少，单锁足够。
type Store struct {
	mu      sync.Mutex
	entries []*Entry // 追加序（旧→新）
	nextID  int
}

func NewStore() *Store { return &Store{} }

// Add 记录一条请求，返回其 ID。超过容量时丢弃最旧一条。
func (s *Store) Add(e Entry) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	e.ID = s.nextID
	cp := e
	s.entries = append(s.entries, &cp)
	if len(s.entries) > Capacity {
		s.entries = s.entries[len(s.entries)-Capacity:]
	}
	return cp.ID
}

// Complete 回填请求结束后的状态与耗时。
func (s *Store) Complete(id int, status int, durationMS int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.ID == id {
			e.Status = status
			e.DurationMS = durationMS
			return
		}
	}
}

// List 返回最近的记录（新→旧），不含请求体。
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for i := len(s.entries) - 1; i >= 0; i-- {
		out = append(out, *s.entries[i])
	}
	return out
}

// Get 返回指定记录（含请求体副本）。
func (s *Store) Get(id int) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.ID == id {
			cp := *e
			return cp, true
		}
	}
	return Entry{}, false
}

// Clear 清空全部记录。
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
}
