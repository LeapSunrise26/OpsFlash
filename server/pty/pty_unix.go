//go:build !windows

package pty

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// ==================== Unix 伪终端（PTY）实现 ====================
// 使用 creack/pty 在 Linux/Mac 上为交互式命令分配真实伪终端。

// unixPTYTransport PTY 传输层
type unixPTYTransport struct {
	ptmx   *os.File
	cmd    *exec.Cmd
	closed bool
}

func (t *unixPTYTransport) Read(p []byte) (int, error)  { return t.ptmx.Read(p) }
func (t *unixPTYTransport) Write(p []byte) (int, error) { return t.ptmx.Write(p) }

// Resize 调整 PTY 终端尺寸（TIOCSWINSZ）
func (t *unixPTYTransport) Resize(cols, rows int) error {
	ws := &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
	return pty.Setsize(t.ptmx, ws)
}

// Kill 终止子进程
func (t *unixPTYTransport) Kill() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Kill()
	}
	return nil
}

// Wait 等待子进程退出
func (t *unixPTYTransport) Wait() error {
	return t.cmd.Wait()
}

// Close 关闭 PTY 主端
func (t *unixPTYTransport) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	return t.ptmx.Close()
}

// StartPTY 启动运行在 PTY 中的交互式命令
// Unix 上解释器概念不适用，统一使用 sh -c（保持跨平台一致）
func StartPTY(cmdText string, interpreter string) (Transport, error) {
	cmd := exec.Command("sh", "-c", cmdText)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("启动 PTY 失败: %w", err)
	}
	return &unixPTYTransport{ptmx: ptmx, cmd: cmd}, nil
}

// StartShell 启动交互式 shell 会话（不执行命令，等待逐行输入）。
// Unix 上忽略解释器，统一使用交互式 sh。
func StartShell(interpreter string) (Transport, error) {
	cmd := exec.Command("sh", "-i")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("启动交互 shell 失败: %w", err)
	}
	return &unixPTYTransport{ptmx: ptmx, cmd: cmd}, nil
}

// PreparePTYInput Unix PTY 下直接透传（行结束符 \n 由 tty 行规程处理）
func PreparePTYInput(s string) string {
	return s
}
