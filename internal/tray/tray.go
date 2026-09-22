package tray

import (
	"log/slog"

	"fyne.io/systray"
)

type Actions struct {
	OpenUI        func()
	StartProxy    func()
	StopProxy     func()
	RestartProxy  func()
	OpenConfigDir func()
	AutoStartOn   func() bool
	SetAutoStart  func(bool) error
	Quit          func()
}

// Run 启动托盘（必须在主线程调用，内部会阻塞）。
func Run(a Actions) {
	systray.Run(func() { onReady(a) }, func() {})
}

func onReady(a Actions) {
	systray.SetIcon(Icon())
	systray.SetTitle("LLM Switch")
	systray.SetTooltip("LLM Switch · 本机模型代理")

	mOpen := systray.AddMenuItem("打开管理页", "在浏览器中打开 LLM Switch")
	systray.AddSeparator()
	mStart := systray.AddMenuItem("启动代理", "")
	mStop := systray.AddMenuItem("停止代理", "")
	mRestart := systray.AddMenuItem("重启代理", "")
	systray.AddSeparator()
	mDir := systray.AddMenuItem("打开配置目录", "")
	mAuto := systray.AddMenuItemCheckbox("开机启动", "登录 Windows 后自动运行", a.AutoStartOn != nil && a.AutoStartOn())
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "")

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				if a.OpenUI != nil {
					a.OpenUI()
				}
			case <-mStart.ClickedCh:
				if a.StartProxy != nil {
					a.StartProxy()
				}
			case <-mStop.ClickedCh:
				if a.StopProxy != nil {
					a.StopProxy()
				}
			case <-mRestart.ClickedCh:
				if a.RestartProxy != nil {
					a.RestartProxy()
				}
			case <-mDir.ClickedCh:
				if a.OpenConfigDir != nil {
					a.OpenConfigDir()
				}
			case <-mAuto.ClickedCh:
				next := !mAuto.Checked()
				if a.SetAutoStart != nil {
					if err := a.SetAutoStart(next); err != nil {
						slog.Error("设置开机启动失败", "error", err)
						continue
					}
				}
				if next {
					mAuto.Check()
				} else {
					mAuto.Uncheck()
				}
			case <-mQuit.ClickedCh:
				if a.Quit != nil {
					a.Quit()
				}
				systray.Quit()
				return
			}
		}
	}()
}
