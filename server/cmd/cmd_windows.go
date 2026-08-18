//go:build windows

package cmd

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
)

// CreateNoWindow = CREATE_NO_WINDOW (0x08000000)
// 让子进程不创建控制台窗口。相比 SysProcAttr.HideWindow（SW_HIDE）更可靠：
// SW_HIDE 只是把新控制台窗口设为"初始隐藏"，在某些 Windows 环境（Win10 21H1+/Win11
// 控制台宿主）下仍会闪现/显示 cmd 窗口；CREATE_NO_WINDOW 直接从根上不创建窗口。
const CreateNoWindow = 0x08000000

// 脚本解释器常量（命令 interpreter 字段取值）
const (
	InterpreterCmd        = "cmd"        // Windows: cmd /s /c；Unix: sh -c
	InterpreterPowerShell = "powershell" // Windows: powershell -EncodedCommand
	InterpreterBash       = "bash"       // Windows: WSL bash（System32/bash.exe）；Unix: 回退 sh
)

// PowershellEncodedCommand 将脚本编码为 PowerShell -EncodedCommand 所需的
// UTF-16LE + Base64 格式。整段脚本作为单个参数传递，换行/引号/中文/特殊字符
// 全部免疫，无需任何转义（也规避 cmd 对 & | > < 的运算符解析）。
func PowershellEncodedCommand(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, len(u16)*2)
	for i, r := range u16 {
		buf[i*2] = byte(r)
		buf[i*2+1] = byte(r >> 8)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// buildCmdline 按解释器构造最终命令行
func buildCmdline(interpreter, script string) (exe string, cmdline string) {
	switch interpreter {
	case InterpreterPowerShell:
		// 直接启动 powershell.exe（Windows 自带，无需 chcp 包装；
		// 脚本内的 [Console]::OutputEncoding / chcp 由脚本自行处理）
		// 前缀 $ProgressPreference：抑制模块首次加载等进度流在管道捕获时
		// 序列化为 CLIXML（<Objs> 噪音）混入 stdout
		script = `$ProgressPreference='SilentlyContinue'; ` + script
		return "powershell",
			`powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ` + PowershellEncodedCommand(script)
	case InterpreterBash:
		// WSL bash：bash -c "script"，脚本内双引号转义为 \"
		escaped := strings.ReplaceAll(script, `"`, `\"`)
		return BashPath(),
			`"` + BashPath() + `" -c "` + escaped + `"`
	default: // cmd（默认，保持重构前行为）
		return "cmd",
			`cmd /s /c "chcp 65001 >nul && ` + script + `"`
	}
}

// BashPath 返回 bash 可执行文件。
// Windows：直接返回 "bash"（走 PATH 解析的 bash——WSL 的 System32/bash.exe 或 Git Bash 等）。
func BashPath() string {
	return "bash"
}

// bashCwdCache bash 内当前工作目录（POSIX 格式，由 bash 自行映射）
var (
	bashCwdOnce sync.Once
	bashCwdVal  string
	bashCwdErr  error
)

// bashCwd 获取 bash 内的当前工作目录（POSIX 格式，缓存）。
// bash 启动时继承 Windows 进程 cwd 并映射到自身挂载路径：
//   - WSL:     /mnt/d/workspace/project/wails-demo
//   - Git Bash: /d/workspace/project/wails-demo
// 探测一次后缓存（应用生命周期内 cwd 不变）。
func bashCwd() (string, error) {
	bashCwdOnce.Do(func() {
		cmd := exec.Command(BashPath(), "-c", "pwd")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: CreateNoWindow,
		}
		out, err := cmd.Output()
		if err != nil {
			bashCwdErr = err
			return
		}
		bashCwdVal = strings.TrimSpace(string(out))
	})
	return bashCwdVal, bashCwdErr
}

// wslFallback 探测失败时的兜底：按 WSL 默认挂载 /mnt/<drive>/... 转换
func wslFallback(abs string) string {
	// D:\path → /mnt/d/path
	if len(abs) >= 2 && abs[1] == ':' {
		drive := strings.ToLower(abs[:1])
		rest := strings.ReplaceAll(abs[2:], "\\", "/")
		return "/mnt/" + drive + rest
	}
	// 无盘符（UNC/相对）→ 反斜杠转正斜杠
	return strings.ReplaceAll(abs, "\\", "/")
}

// ToBashPath 将 Windows 路径（相对或绝对）转换为当前 bash 可识别的 POSIX 路径。
// 思路（用户建议）：先探测 bash 内的 cwd（pwd，由 bash 自行完成 Windows→挂载路径映射），
// 再把相对 cwd 的部分拼接上去——天然兼容 WSL（/mnt/d/...）、Git Bash（/d/...）等任意挂载方式，
// 不硬编码任何挂载前缀。
func ToBashPath(p string) string {
	// 已是 POSIX 绝对路径 → 原样返回
	if strings.HasPrefix(p, "/") {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	cwd, err := bashCwd()
	if err != nil || cwd == "" {
		return wslFallback(abs)
	}
	// Windows cwd → 相对路径 → 拼接到 bash cwd
	wd, err := os.Getwd()
	if err != nil {
		return wslFallback(abs)
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		return wslFallback(abs)
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "." {
		return cwd
	}
	return strings.TrimSuffix(cwd, "/") + "/" + rel
}

// BuildCmd 构建命令执行对象（无 context，默认 cmd 解释器）
func BuildCmd(cmdText string) *exec.Cmd {
	return BuildCmdScript(InterpreterCmd, cmdText)
}

// BuildCmdContext 同 BuildCmd，但带 context（用于超时控制）
func BuildCmdContext(ctx context.Context, cmdText string) *exec.Cmd {
	return BuildCmdContextScript(ctx, InterpreterCmd, cmdText)
}

// BuildCmdScript 按解释器构建命令执行对象（无 context）
func BuildCmdScript(interpreter, script string) *exec.Cmd {
	exe, cmdline := buildCmdline(interpreter, script)
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: CreateNoWindow,
		CmdLine:       cmdline,
	}
	return cmd
}

// BuildCmdContextScript 同 BuildCmdScript，但带 context（用于超时控制）
// 说明：cmd 模式下 CmdLine 直接指定完整命令行，绕过 Go 的 EscapeArg，
//       通过 /s /c 保留内部引号（修复 SSH 带引号命令被拆分），
//       前置 chcp 65001 设置 UTF-8 代码页（修复中文输出乱码）。
//       HideWindow + CREATE_NO_WINDOW 双重确保命令执行全程无窗口弹出。
func BuildCmdContextScript(ctx context.Context, interpreter, script string) *exec.Cmd {
	exe, cmdline := buildCmdline(interpreter, script)
	cmd := exec.CommandContext(ctx, exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: CreateNoWindow,
		CmdLine:       cmdline,
	}
	return cmd
}
