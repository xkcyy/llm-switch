package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"llm-switch/internal/app"
	"llm-switch/internal/autostart"
	"llm-switch/internal/buildinfo"
	"llm-switch/internal/config"
	"llm-switch/internal/instance"
	"llm-switch/internal/tray"
	"llm-switch/internal/winutil"
	"llm-switch/web"
)

// version 可通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	silent := flag.Bool("silent", false, "静默启动：不打开管理页（用于开机启动）")
	noTray := flag.Bool("no-tray", false, "不启用系统托盘（调试用）")
	showVersion := flag.Bool("version", false, "输出版本号")
	flag.Parse()

	if *showVersion {
		fmt.Println("LLM Switch", version)
		return
	}
	buildinfo.Version = version

	release, already, err := instance.Acquire()
	if err != nil {
		fmt.Fprintln(os.Stderr, "获取单实例锁失败:", err)
		os.Exit(1)
	}
	defer release()
	if already {
		a, err := app.New(web.FS)
		if err == nil {
			a.OpenUI()
		}
		return
	}

	a, err := app.New(web.FS)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
	cfg := a.Store.Snapshot()
	open := !*silent && cfg.Settings.OpenBrowserOnStart
	if err := a.Start(open); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}

	if *noTray {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		a.Shutdown()
		return
	}

	runtime.LockOSThread()
	tray.Run(tray.Actions{
		OpenUI: a.OpenUI,
		StartProxy: func() {
			if err := a.Proxy.Start(); err != nil {
				fmt.Fprintln(os.Stderr, "启动代理失败:", err)
			}
		},
		StopProxy: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.Proxy.Stop(ctx)
		},
		RestartProxy: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.Proxy.Restart(ctx)
		},
		OpenConfigDir: func() { openDir(config.Dir()) },
		AutoStartOn:   autostart.Enabled,
		SetAutoStart: func(enabled bool) error {
			if err := autostart.Set(enabled); err != nil {
				return err
			}
			return a.Store.Update(func(c *config.Config) error {
				c.Settings.AutoStart = enabled
				return nil
			})
		},
		Quit: a.Shutdown,
	})
}

func openDir(dir string) {
	os.MkdirAll(dir, 0o755)
	winutil.Open(dir)
}
