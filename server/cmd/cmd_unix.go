//go:build !windows

package cmd

import (
	"context"
	"os/exec"
)

// 脚本解释器常量（与 Windows 对齐，Unix 上统一走 sh -c）
const (
	InterpreterCmd        = "cmd"
	InterpreterPowerShell = "powershell"
	InterpreterBash       = "bash"
)

// BuildCmd 构建命令执行对象（无 context）：Unix 使用 sh -c
func BuildCmd(cmdText string) *exec.Cmd {
	return BuildCmdScript(InterpreterCmd, cmdText)
}

// BuildCmdContext 同 BuildCmd，但带 context（用于超时控制）
func BuildCmdContext(ctx context.Context, cmdText string) *exec.Cmd {
	return BuildCmdContextScript(ctx, InterpreterCmd, cmdText)
}

// BuildCmdScript 按解释器构建命令执行对象（无 context）
// Unix 上解释器概念不适用（powershell 为 Windows 专属，bash 与 sh 行为等价），
// 统一使用 sh -c 保持跨平台一致。
func BuildCmdScript(interpreter, script string) *exec.Cmd {
	return exec.Command("sh", "-c", script)
}

// BuildCmdContextScript 同 BuildCmdScript，但带 context（用于超时控制）
func BuildCmdContextScript(ctx context.Context, interpreter, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}

// ToBashPath Unix 上路径本就是 POSIX 格式，原样返回
func ToBashPath(p string) string {
	return p
}
