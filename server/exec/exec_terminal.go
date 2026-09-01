package exec

import (
	"context"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"opsflash/server/cmd"
	"opsflash/server/daemon"
	"opsflash/server/pty"
)

// ==================== terminalExecutor：本地执行器 ====================
// 封装 cmd.BuildCmdScript/BuildCmdContextScript/pty.StartPTY/daemon.StartDaemonProcess，
// 行为与重构前完全一致（纯重构，回归零风险）。
// interpreter（cmd/powershell/bash）在构造时注入，仅本地执行有意义。

type terminalExecutor struct {
	interpreter string
}

// NewTerminalExecutor 创建本地执行器
func NewTerminalExecutor(interpreter string) Executor {
	if interpreter == "" {
		interpreter = "cmd"
	}
	return &terminalExecutor{interpreter: interpreter}
}

// Run 本地非交互执行（Windows: cmd /s /c 或 powershell -EncodedCommand，Unix: sh -c；60s 超时由调用方 ctx 控制）
func (e *terminalExecutor) Run(ctx context.Context, cmdText string) (string, error) {
	command := cmd.BuildCmdContextScript(ctx, e.interpreter, cmdText)
	var stdoutBuf, stderrBuf strings.Builder
	command.Stdout = &stdoutBuf
	command.Stderr = &stderrBuf
	err := command.Run()

	output := DecodeOutput([]byte(stdoutBuf.String()))
	if stderrStr := DecodeOutput([]byte(stderrBuf.String())); stderrStr != "" {
		if output != "" {
			output += "\n"
		}
		output += stderrStr
	}
	return output, err
}

// StartInteractive 本地伪终端（PTY）交互
func (e *terminalExecutor) StartInteractive(cmdText string) (pty.Transport, error) {
	return pty.StartPTY(cmdText, e.interpreter)
}

// StartDaemon 本地守护进程（Job Object 进程树管理）
func (e *terminalExecutor) StartDaemon(cmdText string) (daemon.Record, error) {
	return daemon.StartDaemonProcess(cmdText, e.interpreter)
}

// DecodeOutput 将原始字节解码为 UTF-8 字符串
// 优先尝试 UTF-8，失败后回退到 GBK 解码（Windows 中文系统常见编码）
func DecodeOutput(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	// GBK 解码兜底
	decoder := simplifiedchinese.GBK.NewDecoder()
	decoded, err := decoder.Bytes(raw)
	if err != nil {
		return string(raw) // 解码失败，返回原始字符串
	}
	return string(decoded)
}
