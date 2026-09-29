// Package usage 聚合本机代理的调用用量：调用次数与上游上报的 token 数。
// 只采信上游上报值（见 docs/adr/0002），缺用量时单独计数，不估算。
package usage

import (
	"sync"
	"time"
)

const (
	dayLayout  = "2006-01-02"
	hourLayout = "2006-01-02T15"
)

// Totals 是一组调用在某个范围内的合计。
type Totals struct {
	Requests     int `json:"requests"`
	OK           int `json:"ok"`
	Err          int `json:"err"`
	Input        int `json:"input_tokens"`
	Output       int `json:"output_tokens"`
	Total        int `json:"total_tokens"`
	Cached       int `json:"cached_tokens"`
	CacheWrite   int `json:"cache_write_tokens"`
	Reasoning    int `json:"reasoning_tokens"`
	MissingUsage int `json:"missing_usage"`
}

// Entry 是一次调用的用量（由代理侧采集后交给 Store 落桶）。
type Entry struct {
	Time         time.Time
	ProviderID   string
	ProviderName string
	ModelID      string
	ModelName    string
	Caller       string
	OK           bool
	HasUsage     bool
	Input        int
	Output       int
	Total        int
	Cached       int
	CacheWrite   int
	Reasoning    int
}

type bucketKey struct {
	Period     string
	ProviderID string
	ModelID    string
	Caller     string
}

// bucketData 是一个桶：合计，加上首次写入时记下的名称快照。
// 改名或删除供应商、模型之后，旧时段仍显示当时的名字。
type bucketData struct {
	Totals
	ProviderName string
	ModelName    string
}

// Store 在内存里按小时桶与日桶聚合用量，读多写少，单锁足够。
type Store struct {
	mu    sync.Mutex
	hours map[bucketKey]*bucketData
	days  map[bucketKey]*bucketData

	// 落盘状态：path 为空时纯内存（测试与降级路径）。
	path  string
	dirty bool
	timer *time.Timer
}

func NewStore() *Store {
	return &Store{hours: map[bucketKey]*bucketData{}, days: map[bucketKey]*bucketData{}}
}

// Add 并入一次调用。缺用量且调用成功时计入 missing_usage；失败的调用只计次数。
func (s *Store) Add(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hour := bucketKey{Period: e.Time.Format(hourLayout), ProviderID: e.ProviderID, ModelID: e.ModelID, Caller: e.Caller}
	day := bucketKey{Period: e.Time.Format(dayLayout), ProviderID: e.ProviderID, ModelID: e.ModelID, Caller: e.Caller}
	accumulate(bucket(s.hours, hour), e)
	accumulate(bucket(s.days, day), e)
	s.markDirty()
}

func bucket(m map[bucketKey]*bucketData, k bucketKey) *bucketData {
	b, ok := m[k]
	if !ok {
		b = &bucketData{}
		m[k] = b
	}
	return b
}

func accumulate(b *bucketData, e Entry) {
	if b.ProviderName == "" {
		b.ProviderName = e.ProviderName
	}
	if b.ModelName == "" {
		b.ModelName = e.ModelName
	}
	b.Requests++
	if e.OK {
		b.OK++
	} else {
		b.Err++
	}
	if e.HasUsage {
		b.Input += e.Input
		b.Output += e.Output
		if e.Total > 0 {
			b.Total += e.Total
		} else {
			b.Total += e.Input + e.Output
		}
		b.Cached += e.Cached
		b.CacheWrite += e.CacheWrite
		b.Reasoning += e.Reasoning
	} else if e.OK {
		b.MissingUsage++
	}
}

// Totals 按日粒度汇总区间内的用量（日桶永久保留）。
// 区间两端都取到桶的粒度：只比较日期，同一天传入即可命中。
func (s *Store) Totals(from, to time.Time) Totals {
	return s.sum(s.days, from.Format(dayLayout), to.Format(dayLayout))
}

// HourTotals 按小时粒度汇总区间内的用量（仅当天可用），两端同样取到桶的粒度。
func (s *Store) HourTotals(from, to time.Time) Totals {
	return s.sum(s.hours, from.Format(hourLayout), to.Format(hourLayout))
}

func (s *Store) sum(m map[bucketKey]*bucketData, from, to string) Totals {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Totals
	for k, b := range m {
		if k.Period < from || k.Period > to {
			continue
		}
		out.add(&b.Totals)
	}
	return out
}

func (t *Totals) add(b *Totals) {
	t.Requests += b.Requests
	t.OK += b.OK
	t.Err += b.Err
	t.Input += b.Input
	t.Output += b.Output
	t.Total += b.Total
	t.Cached += b.Cached
	t.CacheWrite += b.CacheWrite
	t.Reasoning += b.Reasoning
	t.MissingUsage += b.MissingUsage
}
