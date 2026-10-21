package usage

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const (
	fileVersion     = 2
	minuteRetention = 48 * time.Hour
	hourRetention   = 7 * 24 * time.Hour
	flushDelay      = 3 * time.Second
	usageFilePerms  = 0o600
)

type bucketRecord struct {
	Period       string `json:"period"`
	ProviderID   string `json:"provider_id,omitempty"`
	ModelID      string `json:"model_id,omitempty"`
	Caller       string `json:"caller,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	Totals
}

type fileFormat struct {
	Version int            `json:"version"`
	Minutes []bucketRecord `json:"minutes,omitempty"`
	Hours   []bucketRecord `json:"hours"`
	Days    []bucketRecord `json:"days"`
}

// Open 打开（或创建）落盘文件并载入历史用量；文件不存在视为全新开始。
func Open(path string) (*Store, error) {
	s := NewStore()
	s.path = path
	if err := s.load(); err != nil {
		return nil, err
	}
	s.prune(time.Now())
	return s, nil
}

// Close 停掉待刷盘定时器并立即落盘。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if !s.dirty {
		return nil
	}
	return s.flushLocked()
}

// markDirty 标脏并安排一次延迟落盘；调用方须持有锁。
func (s *Store) markDirty() {
	s.dirty = true
	if s.path == "" || s.timer != nil {
		return
	}
	s.timer = time.AfterFunc(flushDelay, s.flushLater)
}

func (s *Store) flushLater() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timer = nil
	if !s.dirty {
		return
	}
	if err := s.flushLocked(); err != nil {
		slog.Warn("用量落盘失败", "path", s.path, "error", err)
	}
}

func (s *Store) flushLocked() error {
	s.pruneLocked(time.Now())
	f := fileFormat{Version: fileVersion}
	for k, b := range s.minutes {
		f.Minutes = append(f.Minutes, recordOf(k, b))
	}
	for k, b := range s.days {
		f.Days = append(f.Days, recordOf(k, b))
	}
	for k, b := range s.hours {
		f.Hours = append(f.Hours, recordOf(k, b))
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, usageFilePerms); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func recordOf(k bucketKey, b *bucketData) bucketRecord {
	return bucketRecord{
		Period: k.Period, ProviderID: k.ProviderID, ModelID: k.ModelID, Caller: k.Caller,
		ProviderName: b.ProviderName, ModelName: b.ModelName, Totals: b.Totals,
	}
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f fileFormat
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("解析用量文件失败: %w", err)
	}
	for _, r := range f.Minutes {
		s.minutes[r.key()] = r.bucket()
	}
	for _, r := range f.Days {
		s.days[r.key()] = r.bucket()
	}
	for _, r := range f.Hours {
		s.hours[r.key()] = r.bucket()
	}
	return nil
}

func (r bucketRecord) key() bucketKey {
	return bucketKey{Period: r.Period, ProviderID: r.ProviderID, ModelID: r.ModelID, Caller: r.Caller}
}

func (r bucketRecord) bucket() *bucketData {
	t := r.Totals
	return &bucketData{Totals: t, ProviderName: r.ProviderName, ModelName: r.ModelName}
}

// prune 丢掉超过保留期的分钟/小时桶；日桶永久保留。
func (s *Store) prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
}

func (s *Store) pruneLocked(now time.Time) {
	minuteCutoff := now.Add(-minuteRetention).Format(minuteLayout)
	for k := range s.minutes {
		if k.Period < minuteCutoff {
			delete(s.minutes, k)
		}
	}
	hourCutoff := now.Add(-hourRetention).Format(hourLayout)
	for k := range s.hours {
		if k.Period < hourCutoff {
			delete(s.hours, k)
		}
	}
}
