//go:build !windows

package daemon

import (
	"io"
	"os/exec"
	"strings"
	"syscall"
)

// ==================== Unix 守护进程（进程组管理）====================
// 用 Setpgid 让 sh 成为独立进程组组长，终止时 kill(-pgid) 杀掉整个进程组，
// 避免只杀 sh 而遗留其子进程（如 ssh）。

// LocalRecord 本机守护进程运行记录（Unix: 独立进程组管理）
type LocalRecord struct {
	cmd    *exec.Cmd
	stderr *strings.Builder // 启动失败时捕获的错误输出
}

// StartDaemonProcess 以独立进程组启动守护进程
// Unix 上解释器概念不适用，统一 sh -c
func StartDaemonProcess(cmdText string, interpreter string) (*LocalRecord, error) {
	return StartProcess(cmdText, interpreter, nil, &strings.Builder{})
}

// StartProcess 启动进程（独立进程组），支持指定 stdout/stderr 输出流
func StartProcess(cmdText string, interpreter string, stdout, stderr io.Writer) (*LocalRecord, error) {
	cmd := exec.Command("sh", "-c", cmdText)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &LocalRecord{cmd: cmd}, nil
}

// Terminate 终止守护进程的整个进程组
func (r *LocalRecord) Terminate() error {
	if r.cmd.Process == nil {
		return nil
	}
	// 杀整个进程组（负 PID 表示进程组）
	_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
	// 再杀 sh 本体（wait goroutine 负责回收）
	_ = r.cmd.Process.Kill()
	return nil
}

// Cleanup Unix 无额外资源需释放
func (r *LocalRecord) Cleanup() {
	// no-op
}

// PID 返回进程 PID
func (r *LocalRecord) PID() int {
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Pid
	}
	return 0
}

// Running 进程是否仍在运行
func (r *LocalRecord) Running() bool {
	return r.cmd != nil && r.cmd.ProcessState == nil
}

// StderrText 返回启动后捕获的错误输出
func (r *LocalRecord) StderrText() string {
	if r.stderr == nil {
		return ""
	}
	return r.stderr.String()
}

// Wait 等待进程退出
func (r *LocalRecord) Wait() error {
	return r.cmd.Wait()
}
