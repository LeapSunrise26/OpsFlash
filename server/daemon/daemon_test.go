//go:build windows

package daemon

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ==================== 守护进程进程树终止测试 ====================
// 验证 StopDaemon 能真正终止 cmd 及其子进程（而不是只杀 cmd 外壳）。

// tasklistPid 用 tasklist 判断进程是否存在
func tasklistPid(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strconv.Itoa(pid))
}

// findPidByName 用 tasklist 按进程名查 pid（取第一个）
func findPidByName(name string) int {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+name, "/NH").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if pid, err := strconv.Atoi(fields[1]); err == nil {
				return pid
			}
		}
	}
	return 0
}

// TestDaemonTerminateKillsProcessTree 启动 ping（cmd 子进程），终止后确认父子进程都消失
func TestDaemonTerminateKillsProcessTree(t *testing.T) {
	// ping -n 100 会持续运行约 100 秒，足够测试
	rec, err := StartDaemonProcess(`ping 127.0.0.1 -n 100`, "cmd")
	if err != nil {
		t.Fatalf("StartDaemonProcess 失败: %v", err)
	}
	cmdPid := rec.PID()
	t.Logf("cmd pid: %d, jobHandle: 0x%x", cmdPid, rec.jobHandle)
	if rec.jobHandle == 0 {
		t.Log("警告: jobHandle 为 0（Job Object 不可用，走 taskkill 兜底），仍继续验证进程树终止")
	}

	// 等待子进程出现
	var pingPid int
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if pingPid = findPidByName("ping.exe"); pingPid != 0 {
			break
		}
	}
	if pingPid == 0 {
		t.Fatal("未找到 ping 子进程，守护进程可能未正确启动")
	}
	t.Logf("找到 ping 子进程 pid: %d", pingPid)

	// 终止整个进程树
	if err := rec.Terminate(); err != nil {
		t.Fatalf("Record.Terminate 失败: %v", err)
	}

	// 等待进程结束
	time.Sleep(1 * time.Second)

	// 子进程必须消失
	if tasklistPid(pingPid) {
		t.Fatalf("子进程 ping(%d) 仍然存活 —— 进程树未被真实终止", pingPid)
	}
	t.Logf("子进程 ping(%d) 已终止 ✓", pingPid)

	// 外壳进程也应消失
	if tasklistPid(cmdPid) {
		t.Fatalf("外壳进程 cmd(%d) 仍然存活", cmdPid)
	}
	t.Logf("外壳进程 cmd(%d) 已终止 ✓", cmdPid)

	rec.Cleanup()
}
