//go:build windows

package pty

import (
	"strings"
	"testing"
	"time"
)

// ==================== Windows ConPTY 冒烟测试 ====================
// 验证伪控制台能真实运行 cmd 命令、接收输入、正常退出。

// TestConPTYBasicOutput 基本输出：echo 命令的输出能被读取
func TestConPTYBasicOutput(t *testing.T) {
	tr, err := StartPTY(`echo hello-pty`, "cmd")
	if err != nil {
		t.Fatalf("StartPTY 失败: %v", err)
	}
	defer tr.Close()

	chunks := startDrain(tr)
	out, err := drainUntil(chunks, "hello-pty", 15*time.Second)
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Logf("输出: %q", out)

	// 进程应能正常退出
	waitCh := make(chan error, 1)
	go func() { waitCh <- tr.Wait() }()
	select {
	case <-waitCh:
	case <-time.After(10 * time.Second):
		t.Fatal("进程未在预期时间内退出")
	}
}

// TestConPTYInteractiveInput 交互输入：写入命令 + 回车，应能看到回显与执行结果
func TestConPTYInteractiveInput(t *testing.T) {
	tr, err := StartPTY(`cmd /q`, "cmd")
	if err != nil {
		t.Fatalf("StartPTY 失败: %v", err)
	}
	defer tr.Close()

	chunks := startDrain(tr)

	// 写入命令并回车（模拟用户输入）
	if _, err := tr.Write([]byte("echo pty-input-test\r\n")); err != nil {
		t.Fatalf("写入输入失败: %v", err)
	}

	out, err := drainUntil(chunks, "pty-input-test", 15*time.Second)
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Logf("交互回显输出: %q", out)

	// 发送 exit 退出
	if _, err := tr.Write([]byte("exit\r\n")); err != nil {
		t.Fatalf("写入 exit 失败: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- tr.Wait() }()
	select {
	case <-waitCh:
	case <-time.After(10 * time.Second):
		t.Fatal("exit 后进程未退出")
	}
}

// TestConPTYOutputNormalized 输出归一化：原始 PTY 输出经 NormalizePTY 后应无 \r 和 ANSI 序列
func TestConPTYOutputNormalized(t *testing.T) {
	tr, err := StartPTY(`echo line-a`, "cmd")
	if err != nil {
		t.Fatalf("StartPTY 失败: %v", err)
	}
	defer tr.Close()

	chunks := startDrain(tr)
	out, err := drainUntil(chunks, "line-a", 15*time.Second)
	if err != nil {
		t.Fatalf("%v", err)
	}
	normalized := string(NormalizePTY([]byte(out)))
	if strings.Contains(normalized, "\r") {
		t.Fatalf("归一化后仍包含 \\r: %q", normalized)
	}
	if strings.ContainsRune(normalized, 0x1b) {
		t.Fatalf("归一化后仍包含 ANSI 转义序列: %q", normalized)
	}
	if !strings.Contains(normalized, "line-a") {
		t.Fatalf("归一化后丢失内容: %q", normalized)
	}
	t.Logf("归一化输出: %q", normalized)

	waitCh := make(chan error, 1)
	go func() { waitCh <- tr.Wait() }()
	select {
	case <-waitCh:
	case <-time.After(10 * time.Second):
		t.Fatal("进程未在预期时间内退出")
	}
}
