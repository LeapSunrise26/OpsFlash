//go:build windows

package daemon

import (
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"opsflash/server/cmd"

	"golang.org/x/sys/windows"
)

// ==================== Windows 守护进程（Job Object 进程树管理）====================
// 守护进程通过 cmd /s /c 启动，真正干活的进程（如 ssh）是 cmd 的子进程。
// 只用 cmd.Process.Kill() 只会杀掉 cmd 外壳，子进程会变成孤儿继续运行。
// 方案：把 cmd 放入带 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 的 Job Object，
//   - 停止时：TerminateJobObject 终止整个进程树
//   - 应用退出时：Job 句柄随进程关闭，KILL_ON_JOB_CLOSE 自动终止 Job 内所有进程
// 若 Job 不可用（如应用本身位于受限 Job 中），降级为 taskkill /T /F。

// Record 守护进程运行记录（跨平台）
type Record struct {
	cmd       *exec.Cmd
	stderr    *strings.Builder // 启动失败时捕获的错误输出
	jobHandle syscall.Handle   // Windows: Job Object 句柄
}

// StartDaemonProcess 启动守护进程并放入 Job Object 管理
// interpreter 透传给 cmd 包：cmd 走 /s /c 包装，powershell 走 -EncodedCommand
func StartDaemonProcess(cmdText string, interpreter string) (*Record, error) {
	stderr := &strings.Builder{}
	return StartProcess(cmdText, interpreter, nil, stderr)
}

// StartProcess 启动进程并放入 Job Object 管理，支持指定 stdout/stderr 输出流
// （流式执行场景传入管道；守护进程场景传 nil / stderr builder）
func StartProcess(cmdText string, interpreter string, stdout, stderr io.Writer) (*Record, error) {
	command := cmd.BuildCmdScript(interpreter, cmdText)
	command.Stdout = stdout
	command.Stderr = stderr

	if err := command.Start(); err != nil {
		return nil, err
	}

	rec := &Record{cmd: command}
	if sb, ok := stderr.(*strings.Builder); ok {
		rec.stderr = sb
	}
	assignJobObject(command, rec)
	return rec, nil
}

// assignJobObject 将已启动的进程加入 KILL_ON_JOB_CLOSE 的 Job Object
// 失败时降级（停止守护进程时改用 taskkill），不阻断主流程
func assignJobObject(command *exec.Cmd, rec *Record) {
	// 创建 Job Object 并设置 KILL_ON_JOB_CLOSE
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		slog.Warn("创建 Job Object 失败，停止守护进程时将降级为 taskkill", "pid", command.Process.Pid, "error", err)
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		slog.Warn("设置 Job Object 限制失败，停止守护进程时将降级为 taskkill", "pid", command.Process.Pid, "error", err)
		windows.CloseHandle(job)
		return
	}
	// 打开进程句柄（os.Process.Handle 未导出，用 OpenProcess 获取）
	// AssignProcessToJobObject 要求进程句柄具备 PROCESS_SET_QUOTA | PROCESS_TERMINATE 权限
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(command.Process.Pid))
	if err != nil {
		slog.Warn("OpenProcess 失败，停止守护进程时将降级为 taskkill", "pid", command.Process.Pid, "error", err)
		windows.CloseHandle(job)
		return
	}
	// 把 cmd 加入 Job（其子进程自动继承 Job 关联）
	if err := windows.AssignProcessToJobObject(job, procHandle); err != nil {
		slog.Warn("AssignProcessToJobObject 失败，停止守护进程时将降级为 taskkill", "pid", command.Process.Pid, "error", err)
		windows.CloseHandle(procHandle)
		windows.CloseHandle(job)
		return
	}
	windows.CloseHandle(procHandle)
	rec.jobHandle = syscall.Handle(job)
}

// Terminate 终止守护进程的整个进程树
func (r *Record) Terminate() error {
	if r.cmd.Process == nil {
		return nil
	}
	pid := r.cmd.Process.Pid

	// 优先：终止整个 Job（进程树）
	if r.jobHandle != 0 {
		if err := windows.TerminateJobObject(windows.Handle(r.jobHandle), 1); err == nil {
			// 成功，wait goroutine 负责回收进程与关闭 Job 句柄
			return nil
		} else {
			slog.Warn("TerminateJobObject 失败，改用 taskkill", "pid", pid, "error", err)
		}
	}

	// 兜底：taskkill /T /F 强制终止进程树（CREATE_NO_WINDOW 避免弹出窗口）
	killCmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: cmd.CreateNoWindow}
	if err := killCmd.Run(); err != nil {
		slog.Warn("taskkill 终止进程树失败", "pid", pid, "error", err)
	}
	// 再杀 cmd 外壳本身（wait goroutine 负责回收）
	if err := r.cmd.Process.Kill(); err != nil {
		return err
	}
	return nil
}

// Cleanup 进程退出后释放 Job 句柄
// 关闭最后一个 Job 句柄时，KILL_ON_JOB_CLOSE 会终止 Job 内残留的子进程
func (r *Record) Cleanup() {
	if r.jobHandle != 0 {
		windows.CloseHandle(windows.Handle(r.jobHandle))
		r.jobHandle = 0
	}
}

// PID 返回进程 PID
func (r *Record) PID() int {
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Pid
	}
	return 0
}

// Running 进程是否仍在运行（ProcessState 为 nil 表示未退出）
func (r *Record) Running() bool {
	return r.cmd != nil && r.cmd.ProcessState == nil
}

// StderrText 返回启动后捕获的错误输出
func (r *Record) StderrText() string {
	if r.stderr == nil {
		return ""
	}
	return r.stderr.String()
}

// Wait 等待进程退出
func (r *Record) Wait() error {
	return r.cmd.Wait()
}
