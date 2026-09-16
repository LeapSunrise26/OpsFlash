//go:build windows

package server

import (
	"strings"
	"testing"
	"time"

	"opsflash/server/pty"
)

// 回归测试：ConPTY 启动噪声清洗 + 会话式逐行执行。
// 背景：Windows 11 ConPTY 在 cmd/powershell/bash 启动时统一注入
// "\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H"（含清屏+归位），cmd 还会输出
// "\x1b[K\r\n" 填满 30 行屏幕。这些序列原样推给 xterm 会清掉命令展示与历史
// （用户反馈"结果覆盖命令展示、总是在最上面、历史被覆盖"）。

func TestSanitizeStartupNoise(t *testing.T) {
	// cmd 单行完整原始输出（真实抓取）
	cmdRaw := "\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H\x1b]0;C:\\Windows\\SYSTEM32\\cmd.exe\a\x1b[?25h\x1b[?25lhello-opsflash\x1b[K\r\n\x1b[K\r\n\x1b[K\r\n\x1b[2;1H\x1b[?25h"
	got := sanitizeStartupNoise(cmdRaw)
	if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b[K") {
		t.Errorf("cmd 仍含清屏/填屏序列: %q", got)
	}
	if !strings.Contains(got, "hello-opsflash") {
		t.Errorf("cmd 真实输出丢失: %q", got)
	}
	if strings.Contains(got, "\x1b]") {
		t.Errorf("cmd OSC 未剥离: %q", got)
	}

	// powershell 单行完整原始输出（真实抓取）
	psRaw := "\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[Hhello-opsflash\x1b]0;C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe\a\x1b[?25h\r\n"
	got = sanitizeStartupNoise(psRaw)
	if strings.Contains(got, "\x1b[2J") {
		t.Errorf("powershell 仍含清屏序列: %q", got)
	}
	if !strings.Contains(got, "hello-opsflash") {
		t.Errorf("powershell 真实输出丢失: %q", got)
	}

	// bash 单行完整原始输出（真实抓取）
	bashRaw := "\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[Hhello-opsflash\r\n\x1b]0;C:\\Windows\\SYSTEM32\\bash.exe\a\x1b[?25h"
	got = sanitizeStartupNoise(bashRaw)
	if strings.Contains(got, "\x1b[2J") {
		t.Errorf("bash 仍含清屏序列: %q", got)
	}
	if !strings.Contains(got, "hello-opsflash") {
		t.Errorf("bash 真实输出丢失: %q", got)
	}

	// 用户主动清屏（孤立 \x1b[2J\x1b[H）必须保留（cmd cls / PS Clear-Host）
	userCls := "before\x1b[2J\x1b[Hafter"
	got = sanitizeStartupNoise(userCls)
	if !strings.Contains(got, "\x1b[2J") {
		t.Errorf("用户主动清屏被误剥: %q", got)
	}
}

// TestShellSessionCmd 真实启动 cmd 会话，验证：
//  1. 启动横幅/清屏被排空（feedShellLine 首行不含 \x1b[2J、不含版本横幅）
//  2. 逐行执行不卡死（旧实现 drain goroutine 泄漏抢数据会导致后续行超时）
//  3. 无哨兵残留
func TestShellSessionCmd(t *testing.T) {
	transport, err := pty.StartShell("cmd")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	reader := newShellReader(transport)
	drainReader(reader, 500*time.Millisecond)

	lines := []string{`echo hello-1`, `echo hello-2`, `echo %COMPUTERNAME%`}
	for i, line := range lines {
		cleaned, err := feedShellLine(reader, line)
		if err != nil {
			t.Fatalf("line %d (%s) err: %v", i, line, err)
		}
		if strings.Contains(cleaned, "\x1b[2J") || strings.Contains(cleaned, "\x1b[K") {
			t.Errorf("line %d 仍含清屏/填屏: %q", i, cleaned)
		}
		if strings.Contains(cleaned, "\x1b[") {
			t.Errorf("line %d 残留 ANSI 噪声: %q", i, cleaned)
		}
		if strings.Contains(cleaned, "OPS_SENTINEL") {
			t.Errorf("line %d 残留哨兵: %q", i, cleaned)
		}
		if strings.Contains(cleaned, "Microsoft Windows [版本") {
			t.Errorf("line %d 含 cmd 启动横幅（未排空）: %q", i, cleaned)
		}
		if i < 2 && !strings.Contains(cleaned, "hello") {
			t.Errorf("line %d 输出丢失: %q", i, cleaned)
		}
		t.Logf("line %d cleaned: %q", i, cleaned)
	}
}

// TestStartPTYSingleLineNoEcho 验证单行命令（StartPTY + cmd /Q）：ConPTY 初始化清屏被剥离、
// 命令回显被关闭（前端已展示命令）、真实输出保留。
func TestStartPTYSingleLineNoEcho(t *testing.T) {
	transport, err := pty.StartPTY(`echo hello-single`, "cmd")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	dec := &outputDecoder{}
	buf := make([]byte, 8192)
	var raw string
	// ConPTY 输出管道不保证 EOF，Read 可能一直阻塞：读取放 goroutine，超时后 Close 退出
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			n, rerr := transport.Read(buf)
			if n > 0 {
				raw += dec.Decode(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()
	select {
	case <-readDone:
	case <-time.After(3 * time.Second):
		transport.Close()
		<-readDone
	}
	cleaned := sanitizeStartupNoise(raw)
	t.Logf("raw:   %q", raw)
	t.Logf("clean: %q", cleaned)
	if strings.Contains(cleaned, "\x1b[2J") || strings.Contains(cleaned, "\x1b[K") {
		t.Errorf("残留清屏/填屏: %q", cleaned)
	}
	if strings.Contains(cleaned, "echo hello-single") {
		t.Errorf("命令回显未关闭（/Q 失效）: %q", cleaned)
	}
	if !strings.Contains(cleaned, "hello-single") {
		t.Errorf("真实输出丢失: %q", cleaned)
	}
}

// TestStartPTYSingleLinePowerShell 验证 PowerShell 单行：ConPTYInit 清屏前缀 + OSC 被剥离
// （用户反馈 PowerShell 结果覆盖命令展示——冷启动分块导致帧级正则漏剥，stripper 跨帧兜底）
func TestStartPTYSingleLinePowerShell(t *testing.T) {
	transport, err := pty.StartPTY(`echo hello-ps-single`, "powershell")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	dec := &outputDecoder{}
	buf := make([]byte, 8192)
	var raw string
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			n, rerr := transport.Read(buf)
			if n > 0 {
				raw += dec.Decode(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()
	select {
	case <-readDone:
	case <-time.After(3 * time.Second):
		transport.Close()
		<-readDone
	}

	// 模拟真实推送链：stripper（跨帧剥启动噪声）→ sanitize（帧级兜底）→ oscStripper（全流剥 OSC）
	stripper := &noiseStripper{}
	osc := &oscStripper{}
	frames := splitFrames(raw, 5) // 按 5 字节强切模拟分块
	var pushed string
	for _, f := range frames {
		pushed += osc.Process(sanitizeStartupNoise(stripper.Process(f)))
	}
	t.Logf("raw:    %q", raw)
	t.Logf("pushed: %q", pushed)
	if strings.Contains(pushed, "\x1b[2J") {
		t.Errorf("残留清屏序列: %q", pushed)
	}
	if strings.Contains(pushed, "\x1b]") {
		t.Errorf("残留 OSC: %q", pushed)
	}
	if !strings.Contains(pushed, "hello-ps-single") {
		t.Errorf("真实输出丢失: %q", pushed)
	}
	if strings.Contains(pushed, "echo hello-ps-single") {
		t.Errorf("命令回显残留: %q", pushed)
	}
}

// TestNoiseStripperCrossFrame 验证跨帧剥离：PowerShell 冷启动输出分块，
// ConPTYInit 清屏序列被拆到多帧时必须完整剥离（帧级正则会漏）。
func TestNoiseStripperCrossFrame(t *testing.T) {
	conptyInit := "\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H"

	// 1. 完整一帧
	ns := &noiseStripper{}
	if got := ns.Process(conptyInit + "hello"); got != "hello" {
		t.Errorf("完整帧剥离失败: %q", got)
	}
	if !ns.done {
		t.Error("完整帧后应结束噪声阶段")
	}

	// 2. 跨帧（模拟 Read 分块：按 3 字节切）
	ns = &noiseStripper{}
	var got string
	frames := splitFrames(conptyInit+"hello-world", 3)
	for _, f := range frames {
		got += ns.Process(f)
	}
	if got != "hello-world" {
		t.Errorf("跨帧剥离失败: %q", got)
	}
	if !ns.done {
		t.Error("跨帧后应结束噪声阶段")
	}

	// 3. OSC 标题跨帧
	ns = &noiseStripper{}
	osc := "\x1b]0;C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe\x07"
	if got := ns.Process(osc[:10]); got != "" {
		t.Errorf("OSC 前缀应缓存: %q", got)
	}
	if got := ns.Process(osc[10:] + "echo"); got != "echo" {
		t.Errorf("OSC 跨帧剥离失败: %q", got)
	}

	// 4. 普通输出开头（无噪声）→ 原样返回
	ns = &noiseStripper{}
	if got := ns.Process("plain output"); got != "plain output" {
		t.Errorf("普通输出被误剥: %q", got)
	}

	// 5. 输出以颜色序列开头（如 \x1b[31m red）→ 保留（非启动噪声）
	ns = &noiseStripper{}
	if got := ns.Process("\x1b[31mred\x1b[0m"); got != "\x1b[31mred\x1b[0m" {
		t.Errorf("颜色序列被误剥: %q", got)
	}

	// 6. ConPTYInit 后跟填屏再跟输出
	ns = &noiseStripper{}
	fill := "\x1b[K\r\n\x1b[K\r\n\x1b[K\r\n"
	if got := ns.Process(conptyInit + fill + "done"); got != "done" {
		t.Errorf("含填屏剥离失败: %q", got)
	}
}

// splitFrames 按块大小切分字符串（模拟 Read 分块）
func splitFrames(s string, size int) []string {
	var frames []string
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		frames = append(frames, s[i:end])
	}
	return frames
}

// TestShellSessionPowerShell 验证 PowerShell 会话（-NoLogo 无横幅，确认无清屏/哨兵残留）
func TestShellSessionPowerShell(t *testing.T) {
	transport, err := pty.StartShell("powershell")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	reader := newShellReader(transport)
	drainReader(reader, 800*time.Millisecond)

	lines := []string{`echo hello-ps-1`, `$env:COMPUTERNAME`}
	for i, line := range lines {
		cleaned, err := feedShellLine(reader, line)
		if err != nil {
			t.Fatalf("line %d (%s) err: %v", i, line, err)
		}
		if strings.Contains(cleaned, "\x1b[2J") {
			t.Errorf("line %d 含清屏: %q", i, cleaned)
		}
		if strings.Contains(cleaned, "OPS_SENTINEL") {
			t.Errorf("line %d 残留哨兵: %q", i, cleaned)
		}
		if i == 0 && !strings.Contains(cleaned, "hello-ps-1") {
			t.Errorf("line 0 输出丢失: %q", cleaned)
		}
		t.Logf("line %d cleaned: %q", i, cleaned)
	}
}
