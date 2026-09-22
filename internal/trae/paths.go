// Package trae 负责把 LLM Switch 的模型同步到 Trae SOLO（TRAE SOLO CN）的自定义模型列表。
//
// 实现方式：Trae 是 VS Code 分支（Electron），本包通过 CDP 连上正在运行的 Trae，
// 驱动它自己的「设置 → 模型 → 添加模型」界面完成注册（对应应用内协议
// chat/add_custom_model，由服务端注册）。不改 Trae 的数据库，也不改它的配置文件。
//
// 与手工操作完全等价；页面上的「一键接入」= 扫描差异 + 只对缺失项走一遍官方添加流程。
package trae

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultPort 是 Trae 的 CDP 调试端口默认值。
const DefaultPort = 9222

// Paths 描述 Trae 的安装与配置位置。
type Paths struct {
	Exe         string // TRAE SOLO CN.exe
	UserDataDir string // %APPDATA%\TRAE SOLO CN
	ArgvJSON    string // %USERPROFILE%\.trae-cn\argv.json
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func firstExistingFile(paths []string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func firstExistingDir(paths []string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

// DefaultPaths 自动探测 Trae 的安装位置与用户数据目录。
func DefaultPaths() Paths {
	local := os.Getenv("LOCALAPPDATA")
	roaming := os.Getenv("APPDATA")
	h := home()

	exe := firstExistingFile([]string{
		filepath.Join(local, "Programs", "TRAE SOLO CN", "TRAE SOLO CN.exe"),
		filepath.Join(local, "Programs", "Trae CN", "Trae CN.exe"),
		filepath.Join(local, "Programs", "Trae", "Trae.exe"),
	})
	userData := firstExistingDir([]string{
		filepath.Join(roaming, "TRAE SOLO CN"),
		filepath.Join(roaming, "Trae CN"),
		filepath.Join(roaming, "Trae"),
	})
	// argv.json 放在 ~/<dataFolderName>/argv.json，按用户数据目录名推导
	argv := ""
	switch {
	case strings.Contains(userData, "Trae CN"), strings.Contains(userData, "TRAE SOLO CN"):
		argv = firstExistingFile([]string{filepath.Join(h, ".trae-cn", "argv.json"), filepath.Join(h, ".trae", "argv.json")})
		if argv == "" {
			argv = filepath.Join(h, ".trae-cn", "argv.json")
		}
	case strings.Contains(userData, "Trae"):
		argv = firstExistingFile([]string{filepath.Join(h, ".trae", "argv.json"), filepath.Join(h, ".trae-cn", "argv.json")})
		if argv == "" {
			argv = filepath.Join(h, ".trae", "argv.json")
		}
	default:
		argv = firstExistingFile([]string{filepath.Join(h, ".trae-cn", "argv.json"), filepath.Join(h, ".trae", "argv.json")})
		if argv == "" {
			argv = filepath.Join(h, ".trae-cn", "argv.json")
		}
	}
	return Paths{Exe: exe, UserDataDir: userData, ArgvJSON: argv}
}

// Installed 表示找到了 Trae 的安装位置。
func (p Paths) Installed() bool { return p.Exe != "" }

var portRe = regexp.MustCompile(`"remote-debugging-port"\s*:\s*"?(\d+)"?`)

// DebugPort 返回 argv.json 里配置的调试端口，未配置时返回默认值。
func (p Paths) DebugPort() int {
	if b, err := os.ReadFile(p.ArgvJSON); err == nil {
		if m := portRe.FindSubmatch(b); len(m) == 2 {
			if n, err := strconv.Atoi(string(m[1])); err == nil && n > 0 && n < 65536 {
				return n
			}
		}
	}
	return DefaultPort
}

// DebugPortConfigured 表示 argv.json 里已经写了调试端口。
func (p Paths) DebugPortConfigured() bool {
	b, err := os.ReadFile(p.ArgvJSON)
	return err == nil && portRe.Match(b)
}

// EnsureDebugPort 把调试端口写进 argv.json（保留原有内容与注释），返回是否有改动。
// 这是官方支持的做法：remote-debugging-port 在 VS Code 的 argv.json 白名单里。
func (p Paths) EnsureDebugPort() (bool, error) {
	if p.ArgvJSON == "" {
		return false, fmt.Errorf("找不到 Trae 的 argv.json 路径")
	}
	content := ""
	if b, err := os.ReadFile(p.ArgvJSON); err == nil {
		content = string(b)
		if portRe.Match(b) {
			return false, nil
		}
	}
	if strings.TrimSpace(content) == "" {
		content = "{\n}\n"
	}
	idx := strings.Index(content, "{")
	if idx < 0 {
		return false, fmt.Errorf("%s 不是合法的 JSON 配置", p.ArgvJSON)
	}
	if _, err := os.Stat(p.ArgvJSON); err == nil {
		_ = os.WriteFile(p.ArgvJSON+".bak-llmswitch", []byte(content), 0o644)
	}
	insert := fmt.Sprintf("\n\t// 由 LLM Switch 添加：允许本机 CDP 自动化（调试端口 %d）\n\t\"remote-debugging-port\": \"%d\",", DefaultPort, DefaultPort)
	next := content[:idx+1] + insert + content[idx+1:]
	if err := os.MkdirAll(filepath.Dir(p.ArgvJSON), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(p.ArgvJSON, []byte(next), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveDebugPort 移除本插件写入的调试端口（用于卸载/回滚）。
func (p Paths) RemoveDebugPort() (bool, error) {
	b, err := os.ReadFile(p.ArgvJSON)
	if err != nil {
		return false, nil
	}
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if strings.Contains(line, "remote-debugging-port") || strings.Contains(line, "由 LLM Switch 添加") {
			changed = true
			continue
		}
		out = append(out, line)
	}
	if !changed {
		return false, nil
	}
	return true, os.WriteFile(p.ArgvJSON, []byte(strings.Join(out, "\n")), 0o644)
}

// Running 判断 Trae 主进程是否在运行。
func (p Paths) Running(ctx context.Context) bool {
	if p.Exe == "" {
		return false
	}
	name := filepath.Base(p.Exe)
	cmd := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq "+name, "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(name))
}

// Launch 启动 Trae（带调试端口参数；argv.json 里通常也已经有）。
func (p Paths) Launch() error {
	if p.Exe == "" {
		return fmt.Errorf("未找到 Trae 可执行文件")
	}
	cmd := exec.Command(p.Exe, fmt.Sprintf("--remote-debugging-port=%d", p.DebugPort()))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	return cmd.Start()
}

// KillAll 结束所有 Trae 进程（会中断正在运行的会话，需用户确认后再调用）。
func (p Paths) KillAll(ctx context.Context) error {
	if p.Exe == "" {
		return fmt.Errorf("未找到 Trae 可执行文件")
	}
	cmd := exec.CommandContext(ctx, "taskkill", "/IM", filepath.Base(p.Exe), "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run() // 没有进程时 taskkill 会返回非零，忽略
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !p.Running(ctx) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("Trae 未能在 20 秒内退出")
}

// Restart 关闭并重新启动 Trae（保留默认 profile 与登录态）。
func (p Paths) Restart(ctx context.Context) error {
	if err := p.KillAll(ctx); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	return p.Launch()
}
