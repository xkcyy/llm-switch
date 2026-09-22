package app

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"llm-switch/internal/admin"
	"llm-switch/internal/autostart"
	"llm-switch/internal/codex"
	"llm-switch/internal/config"
	"llm-switch/internal/logx"
	"llm-switch/internal/metadata"
	"llm-switch/internal/opencode"
	"llm-switch/internal/provider"
	"llm-switch/internal/proxy"
	"llm-switch/internal/trae"
	"llm-switch/internal/winutil"
)

type App struct {
	Store    *config.Store
	Index    *proxy.Holder
	Proxy    *proxy.Server
	Codex    *codex.Service
	OpenCode *opencode.Service
	Trae     *trae.Service
	Admin    *admin.Server
	LogDir   string

	syncMu    sync.Mutex
	syncTimer *time.Timer
}

func New(webFS fs.FS) (*App, error) {
	store, err := config.Open(config.DefaultPath())
	if err != nil {
		return nil, err
	}
	cfg := store.Snapshot()
	logDir := filepath.Join(config.Dir(), "logs")
	if err := logx.Setup(logDir, cfg.Settings.Log.Level, cfg.Settings.Log.RetentionDays, false); err != nil {
		return nil, err
	}
	// 启动自愈：修复不一致数据（默认模型失效、孤儿模型等），避免单条坏数据拖垮整个系统
	if repairs, err := store.Sanitize(); err != nil {
		slog.Warn("配置自愈失败", "error", err)
	} else {
		for _, r := range repairs {
			slog.Warn("配置已自动修复", "kind", r.Kind, "detail", r.Detail)
		}
	}
	cfg = store.Snapshot()
	index := &proxy.Holder{}
	index.Store(proxy.BuildIndex(&cfg))
	px := proxy.NewServer(store, index)
	metaClient := metadata.New(filepath.Join(config.Dir(), "metadata"))
	provSvc := provider.NewService(store, metaClient)
	cx := codex.NewService(store)
	oc := opencode.NewService(store)
	tr := trae.NewService(store)

	a := &App{Store: store, Index: index, Proxy: px, Codex: cx, OpenCode: oc, Trae: tr, LogDir: logDir}
	a.Admin = admin.New(store, provSvc, px, cx, oc, tr, webFS, logDir, autostart.Set)
	store.Subscribe(a.onConfigChanged)
	return a, nil
}

func (a *App) onConfigChanged() {
	cfg := a.Store.Snapshot()
	a.Index.Store(proxy.BuildIndex(&cfg))
	if cfg.Settings.CodexAutoSync || cfg.Settings.OpenCodeAutoSync {
		a.scheduleSync()
	}
}

// scheduleSync 去抖后执行统一同步（自动模式）。
func (a *App) scheduleSync() {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.syncTimer != nil {
		a.syncTimer.Stop()
	}
	a.syncTimer = time.AfterFunc(500*time.Millisecond, func() {
		cfg := a.Store.Snapshot()
		if cfg.Settings.CodexAutoSync {
			plan, err := a.Codex.BuildPlan(codex.Overrides{})
			if err != nil {
				slog.Error("Codex 自动同步：生成计划失败", "error", err)
			} else if err := a.Codex.Apply(plan); err != nil {
				slog.Error("Codex 自动同步：写入失败", "error", err)
			} else {
				slog.Info("Codex 配置已自动同步", "model", plan.DefaultModel, "base_url", plan.BaseURL)
			}
		}
		if cfg.Settings.OpenCodeAutoSync && a.OpenCode.Connected() {
			// 只维护已接入的配置，不会自行创建 opencode.json
			plan, err := a.OpenCode.BuildPlan(opencode.Overrides{})
			if err != nil {
				slog.Error("OpenCode 自动同步：生成计划失败", "error", err)
			} else if err := a.OpenCode.Apply(plan); err != nil {
				slog.Error("OpenCode 自动同步：写入失败", "error", err)
			} else {
				slog.Info("OpenCode 配置已自动同步", "models", len(plan.Models), "base_url", plan.BaseURL)
			}
		}
	})
}

func (a *App) Start(openBrowser bool) error {
	if err := a.Proxy.Start(); err != nil {
		slog.Warn("代理启动失败，管理界面仍可使用", "error", err)
	}
	if err := a.Admin.Start(); err != nil {
		return err
	}
	if openBrowser {
		a.OpenUI()
	}
	return nil
}

func (a *App) OpenUI() {
	url := a.Admin.URL()
	if err := winutil.Open(url); err != nil {
		slog.Error("打开管理页失败", "error", err)
	}
}

func (a *App) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.Proxy.Stop(ctx)
	a.Admin.Stop(ctx)
	slog.Info("LLM Switch 已退出")
}
