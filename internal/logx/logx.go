package logx

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// rollingWriter 按天滚动日志文件。
type rollingWriter struct {
	mu   sync.Mutex
	dir  string
	day  string
	file *os.File
	std  io.Writer
}

func newRollingWriter(dir string, std io.Writer) (*rollingWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &rollingWriter{dir: dir, std: std}
	if err := w.rotate(time.Now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rollingWriter) rotate(now time.Time) error {
	day := now.Format("2006-01-02")
	if w.file != nil && w.day == day {
		return nil
	}
	if w.file != nil {
		w.file.Close()
	}
	f, err := os.OpenFile(filepath.Join(w.dir, "app-"+day+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.file, w.day = f, day
	return nil
}

func (w *rollingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotate(time.Now()); err != nil {
		return 0, err
	}
	if w.std != nil {
		w.std.Write(p)
	}
	return w.file.Write(p)
}

// Cleanup 删除超过保留天数的日志文件。
func Cleanup(dir string, retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "app-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "app-"), ".log")
		t, err := time.ParseInLocation("2006-01-02", day, time.Local)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			os.Remove(filepath.Join(dir, name))
		}
	}
	// 保证目录内不会无限增长：最多保留 retentionDays 个文件。
	files := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "app-") && strings.HasSuffix(e.Name(), ".log") {
			files = append(files, e.Name())
		}
	}
	if len(files) > retentionDays {
		sort.Strings(files)
		for _, f := range files[:len(files)-retentionDays] {
			os.Remove(filepath.Join(dir, f))
		}
	}
}

// Setup 初始化全局 slog。
func Setup(dir string, level string, retentionDays int, console bool) error {
	Cleanup(dir, retentionDays)
	var std io.Writer
	if console {
		std = os.Stderr
	}
	w, err := newRollingWriter(dir, std)
	if err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}
	lv := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv}))
	slog.SetDefault(logger)
	slog.Info("日志已初始化", "dir", dir, "level", level)
	return nil
}
