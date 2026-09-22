// Package winutil 封装无控制台窗口的 Windows Shell 调用。
package winutil

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32           = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// Open 用系统默认程序打开 URL、文件或目录。
// 使用 ShellExecuteW 而不是 cmd/start，避免 GUI 程序弹出控制台黑框。
func Open(target string) error {
	if target == "" {
		return fmt.Errorf("打开目标为空")
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	const swShowNormal = 1
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		swShowNormal,
	)
	if ret <= 32 {
		return fmt.Errorf("打开 %s 失败（ShellExecute 返回 %d）", target, ret)
	}
	return nil
}
