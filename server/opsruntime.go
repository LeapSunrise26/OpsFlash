package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"opsflash/server/daemon"
	"opsflash/server/exec"
	"opsflash/server/pty"
)

// outputDecoder 跨块输出解码器：UTF-8 优先、GBK 兜底（复用 exec.DecodeOutput），
// 并处理 8192 字节块边界截断的多字节字符（xterm 需要完整 UTF-8 流，不能按块二次解码）。
type outputDecoder struct {
	pending []byte // 上一块末尾未完成的多字节序列
}

// Decode 解码一块输出，返回完整的 UTF-8 字符串（保留 ANSI 控制码）
func (d *outputDecoder) Decode(chunk []byte) string {
	all := append(d.pending, chunk...)
	cut := utf8CompleteLen(all)
	d.pending = append(d.pending[:0], all[cut:]...)
	if cut == 0 {
		return ""
	}
	return exec.DecodeOutput(all[:cut])
}

// utf8CompleteLen 返回 b 中最长的"完整 UTF-8 前缀"长度（从末尾扫描最多 4 字节找序列边界）
func utf8CompleteLen(b []byte) int {
	n := len(b)
	for i := n - 1; i >= 0 && i >= n-4; i-- {
		c := b[i]
		if c < 0x80 {
			return i + 1 // ASCII 之后的字节已在之前迭代确认完整
		}
		if utf8.RuneStart(c) {
			_, size := utf8.DecodeRune(b[i:])
			if size > 1 && i+size <= n {
				return i + size // 完整多字节序列
			}
			// 不完整序列（被块边界截断）→ 截断保留到 pending，等待下一块补全
			return i
		}
	}
	return n
}

// ==================== 命令执行相关请求/响应类型 ====================

type RunCommandRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

// 守护进程请求/响应
type StartDaemonRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type StopDaemonRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type ProcessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Running bool   `json:"running"`
}

// 交互式命令请求/响应
type StartInteractiveRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type StopInteractiveRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type GetInteractiveOutputRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type SendInteractiveInputRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
	Input string `json:"input"`
}

type GetInteractiveOutputResponse struct {
	Success   bool   `json:"success"`
	Output    string `json:"output"`
	Done      bool   `json:"done"`
	ExitError string `json:"exitError"`
	Message   string `json:"message"`
}

type InteractiveInputResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type RunCommandResponse struct {
	Success    bool              `json:"success"`
	Output     string            `json:"output"`
	ResultType string            `json:"resultType"` // "" | "text" | "table"（数据库查询类返回表格）
	Result     *exec.QueryResult `json:"result"`
	Message    string            `json:"message"`
}

// interactiveSession 交互式命令会话
type interactiveSession struct {
	transport pty.Transport
	output    []byte
	readPos   int
	mu        sync.Mutex
	done      bool
	exitError string
}

// ==================== 命令执行辅助 ====================
// 命令构建（buildCmd/buildCmdContext）→ cmd 包；输出解码（decodeOutput）→ exec 包；
// PTY 归一化（normalizePTY/stripANSI）与传输层 → pty 包；守护进程 → daemon 包。

// ==================== 非交互式命令执行 ====================

// RunCommand 执行非交互式命令
func (s *OpsService) RunCommand(req RunCommandRequest) RunCommandResponse {
	if _, ok := validateSession(req.Token); !ok {
		return RunCommandResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName, cmdText, cmdType, mode, envName, interpreter string
	var connID int
	err := db.QueryRow(`SELECT c.name, c.command, c.type, c.mode, COALESCE(c.connection_id, 0), e.name, COALESCE(c.interpreter, 'cmd') FROM commands c
		INNER JOIN environments e ON c.environment_id = e.id
		WHERE c.id = ?`, req.ID).Scan(&cmdName, &cmdText, &cmdType, &mode, &connID, &envName, &interpreter)
	if err == sql.ErrNoRows {
		return RunCommandResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return RunCommandResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	if cmdType != "non-interactive" {
		typeName := "守护进程"
		if cmdType == "interactive" {
			typeName = "交互式"
		}
		return RunCommandResponse{Success: false, Message: typeName + "类型命令请使用启动/停止操作"}
	}

	slog.Info("执行非交互式命令", "id", req.ID, "name", cmdName, "env", envName, "mode", mode, "interpreter", interpreter, "command", cmdText)

	// 加载执行器（terminal 本地 / ssh 远程 / 其他模式）
	conn, err := loadConnectionByID(connID)
	if err != nil {
		return RunCommandResponse{Success: false, Message: err.Error()}
	}
	ex, err := executorFor(mode, conn, interpreter)
	if err != nil {
		return RunCommandResponse{Success: false, Message: err.Error()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()

	// 数据库类执行器（redis/mysql/tdengine）返回结构化结果，前端渲染表格
	if qex, ok := ex.(exec.QueryExecutor); ok {
		result, qerr := qex.Query(ctx, cmdText)
		elapsed := time.Since(start)
		if qerr != nil {
			slog.Warn("数据库指令执行失败", "name", cmdName, "mode", mode, "error", qerr, "elapsed", elapsed)
			return RunCommandResponse{
				Success: false,
				Output:  qerr.Error(),
				Message: "指令执行失败: " + qerr.Error(),
			}
		}
		slog.Info("数据库指令执行成功", "name", cmdName, "mode", mode, "elapsed", elapsed, "columns", len(result.Columns), "rows", len(result.Rows))
		return RunCommandResponse{
			Success:    true,
			Output:     "查询成功",
			ResultType: "table",
			Result:     result,
			Message:    "指令执行成功",
		}
	}

	// 本地 cmd 多行：按行拆分为独立命令逐条执行、逐行展示结果
	// （powershell/bash 为整体脚本执行，不拆分；ssh/数据库模式不适用）
	if mode == "terminal" && interpreter == "cmd" && strings.Contains(cmdText, "\n") {
		return runCommandLines(ctx, ex, cmdName, cmdText)
	}

	output, err := ex.Run(ctx, cmdText)
	elapsed := time.Since(start)
	output = strings.TrimSpace(output)

	if len(output) > 10000 {
		output = output[:10000] + "\n... (输出已截断)"
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			slog.Warn("命令执行超时", "name", cmdName, "timeout", "60s")
			return RunCommandResponse{
				Success: false,
				Output:  output + "\n[命令执行超时，60秒后自动终止]",
				Message: "命令执行超时",
			}
		}
		slog.Warn("命令执行失败", "name", cmdName, "error", err, "elapsed", elapsed)
		return RunCommandResponse{
			Success: false,
			Output:  output,
			Message: "命令执行失败: " + err.Error(),
		}
	}

	slog.Info("命令执行成功", "name", cmdName, "elapsed", elapsed)
	return RunCommandResponse{
		Success: true,
		Output:  output,
		Message: "命令执行成功",
	}
}

// runCommandLines 同步逐行执行 cmd 多行命令（共享 ctx 超时），
// 每行输出前加 "$ 行内容" 前缀便于区分，失败行继续执行并在最后汇总。
// 供 RunCommand 与批量执行（batchservice）复用。
func runCommandLines(ctx context.Context, ex exec.Executor, cmdName, cmdText string) RunCommandResponse {
	lines := splitExecLines(cmdText)
	var out strings.Builder
	failed := 0
	for i, line := range lines {
		out.WriteString("$ " + line + "\n")
		output, err := ex.Run(ctx, line)
		if output = strings.TrimSpace(output); output != "" {
			out.WriteString(output + "\n")
		}
		if err != nil {
			failed++
			out.WriteString("! 第 " + strconv.Itoa(i+1) + " 行执行失败: " + err.Error() + "\n")
		}
	}

	result := strings.TrimSpace(out.String())
	if failed > 0 {
		msg := fmt.Sprintf("共 %d 行，%d 行执行失败", len(lines), failed)
		slog.Warn("cmd 多行命令执行完成（含失败）", "name", cmdName, "total", len(lines), "failed", failed)
		return RunCommandResponse{Success: false, Output: result, Message: msg}
	}
	slog.Info("cmd 多行命令执行成功", "name", cmdName, "lines", len(lines))
	return RunCommandResponse{Success: true, Output: result, Message: fmt.Sprintf("共 %d 行全部执行成功", len(lines))}
}

// ==================== 非交互式流式执行（实时输出 + 可停止）====================
// 非交互命令默认的 RunCommand 是同步一次性返回（进程跑完才给全部输出），
// 对部署脚本这类长任务体验差且无法中途停止。
// 流式会话：管道捕获 stdout/stderr → 轮询增量输出（前端 300ms）→ 进程树终止可停止。
// 仅支持本地 terminal 模式（ssh/数据库模式保持原 RunCommand）。

type StartStreamRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type StopStreamRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type GetStreamOutputRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

// streamSession 非交互式流式执行会话
// 两种执行模式：
//   - 进程模式（单行命令）：rec 持有单进程（cmd /s /c 或 powershell EncodedCommand），runStreamLines 驱动
//   - 会话模式（多行命令）：transport 持有交互式 shell（PTY），runSessionLines 逐行喂入，
//     同一 shell 进程内执行 → 变量跨行共享、每行自动回显
type streamSession struct {
	id        int            // 命令 ID（事件推送用）
	rec       daemon.Record  // 进程模式：当前进程（本机 Job Object 进程树）
	transport pty.Transport  // 会话模式：交互式 shell 传输层
	cancel    context.CancelFunc
	output    []byte // 已解码的累积输出（UTF-8）
	readPos   int
	mu        sync.Mutex
	done      bool
	exitError string
	lines     []string // 待执行行队列
	lineIdx   int
	stopped   bool // 用户手动停止
}

// StartStream 启动非交互式命令的流式执行（管道捕获，无 60s 超时）
func (s *OpsService) StartStream(req StartStreamRequest) ProcessResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ProcessResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName, cmdText, cmdType, mode, interpreter string
	var connID int
	err := db.QueryRow(`SELECT c.name, c.command, c.type, c.mode, COALESCE(c.connection_id, 0), COALESCE(c.interpreter, 'cmd') FROM commands c
		INNER JOIN environments e ON c.environment_id = e.id
		WHERE c.id = ?`, req.ID).Scan(&cmdName, &cmdText, &cmdType, &mode, &connID, &interpreter)
	if err == sql.ErrNoRows {
		return ProcessResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return ProcessResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	if cmdType != "non-interactive" {
		typeName := "守护进程"
		if cmdType == "interactive" {
			typeName = "交互式"
		}
		return ProcessResponse{Success: false, Message: typeName + "类型命令请使用启动/停止操作"}
	}
	if mode != "" && mode != "terminal" && mode != "ssh" {
		return ProcessResponse{Success: false, Message: "该模式暂不支持流式执行，请使用运行按钮"}
	}

	s.mu.Lock()
	s.ensureMap()
	if existing, exists := s.streamSessions[req.ID]; exists {
		if !existing.done {
			s.mu.Unlock()
			return ProcessResponse{Success: false, Message: cmdName + " 正在执行中", Running: true}
		}
		delete(s.streamSessions, req.ID)
	}
	s.mu.Unlock()

	// SSH 非交互流式：远程请求 PTY（xterm）整段执行命令 → 远程命令检测到 TTY 后行缓冲，
	// 输出实时推送（替代 CombinedOutput 的"运行完才一次性显示"），保留颜色与进度条
	if mode == "ssh" {
		conn, err := loadConnectionByID(connID)
		if err != nil {
			return ProcessResponse{Success: false, Message: err.Error()}
		}
		ex, err := executorFor(mode, conn, interpreter)
		if err != nil {
			return ProcessResponse{Success: false, Message: err.Error()}
		}
		transport, err := ex.StartInteractive(cmdText) // sshTransport：远程 PTY + 合并 stdout/stderr
		if err != nil {
			slog.Error("SSH 流式启动失败", "id", req.ID, "name", cmdName, "error", err)
			return ProcessResponse{Success: false, Message: "启动 SSH 流式执行失败: " + err.Error()}
		}
		session := &streamSession{id: req.ID, lines: []string{cmdText}, transport: transport}
		s.mu.Lock()
		s.streamSessions[req.ID] = session
		s.mu.Unlock()
		slog.Info("SSH 流式命令已启动", "id", req.ID, "name", cmdName)
		go s.runSSHStream(session, transport)
		return ProcessResponse{Success: true, Message: cmdName + " 已开始执行", Running: true}
	}

	// 多行命令 → 会话式逐行执行（任意解释器）：
	// 启动一个交互式 shell 挂 PTY，逐行喂入，同一会话内共享变量、每行自动回显。
	// 单行命令 → 进程模式（cmd /s /c 或 powershell EncodedCommand，单进程）。
	sessionMode := false
	lines := []string{cmdText}
	if strings.Contains(cmdText, "\n") {
		lines = splitExecLines(cmdText)
		if len(lines) == 0 {
			return ProcessResponse{Success: false, Message: "命令内容为空"}
		}
		sessionMode = true
	}

	session := &streamSession{id: req.ID, lines: lines}
	s.mu.Lock()
	s.streamSessions[req.ID] = session
	s.mu.Unlock()

	if sessionMode {
		transport, err := pty.StartShell(interpreter)
		if err != nil {
			s.mu.Lock()
			delete(s.streamSessions, req.ID)
			s.mu.Unlock()
			slog.Error("启动交互 shell 失败", "id", req.ID, "name", cmdName, "interpreter", interpreter, "error", err)
			return ProcessResponse{Success: false, Message: "启动会话失败: " + err.Error()}
		}
		session.mu.Lock()
		session.transport = transport
		session.mu.Unlock()
		slog.Info("会话式命令已启动", "id", req.ID, "name", cmdName, "interpreter", interpreter, "lines", len(lines))
		go s.runSessionLines(session, transport)
	} else {
		slog.Info("流式命令已启动", "id", req.ID, "name", cmdName, "interpreter", interpreter, "lines", len(lines))
		go s.runStreamLines(session, interpreter)
	}

	return ProcessResponse{Success: true, Message: cmdName + " 已开始执行", Running: true}
}

// ensureTrailingNewline 若会话已推送的输出不以换行结尾，补发一个 \r\n。
// PowerShell 部分输出（如 pwd 的 Format-Table：Path/----/路径）最后一行无换行，
// 直接发 done 会让前端的「执行完成」提示紧贴上一行；补换行保证提示另起一行。
func (s *OpsService) ensureTrailingNewline(session *streamSession) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.output) > 0 && session.output[len(session.output)-1] != '\n' {
		session.output = append(session.output, '\r', '\n')
		s.emitTerminalOutput(session.id, "\r\n", false, "")
	}
}

// runSSHStream SSH 非交互流式驱动：远程 PTY 整段执行，实时输出推送 + 可停止。
// 读取链与本地一致（stripper 跨帧剥启动噪声 → sanitize 帧级兜底 → oscStripper 全流剥 OSC）。
// 结束信号 = Wait 返回 OR 输出静默超时：
//   - SSH 远程 PTY 下「命令退出 ≠ channel 关闭」——命令衍生的后台进程（nohup/服务重启等）
//     仍持有 PTY fd 时 sshd 不发 EOF，session.Wait() 可能永久阻塞；
//   - 因此除 Wait 外增加「输出静默 N 秒」判定：命令输出停止即视为执行完成，强制收尾，
//     否则前端将一直显示"运行中"只能手动点停止。
func (s *OpsService) runSSHStream(session *streamSession, transport pty.Transport) {
	defer func() {
		s.ensureTrailingNewline(session)
		session.mu.Lock()
		session.done = true
		exitErr := session.exitError
		session.mu.Unlock()
		s.emitTerminalOutput(session.id, "", true, exitErr)
	}()

	decoder := &outputDecoder{}
	buf := make([]byte, 8192)
	readDone := make(chan struct{})
	dataCh := make(chan struct{}, 1) // 有数据信号：主流程据此重置静默计时
	stripper := &noiseStripper{}
	osc := &oscStripper{}
	go func() {
		defer close(readDone)
		for {
			n, rerr := transport.Read(buf)
			if n > 0 {
				data := osc.Process(sanitizeStartupNoise(stripper.Process(decoder.Decode(buf[:n]))))
				if data != "" {
					session.mu.Lock()
					session.output = append(session.output, []byte(data)...)
					session.mu.Unlock()
					s.emitTerminalOutput(session.id, data, false, "")
				}
				// 有新输出 → 通知主流程重置静默计时（channel 满则跳过，下轮仍会通知）
				select {
				case dataCh <- struct{}{}:
				default:
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// 输出静默阈值：部署/重启类命令收尾足够，又不误杀慢输出任务（长驻任务请用交互式类型）
	const sshIdleTimeout = 10 * time.Second

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- transport.Wait()
	}()

	idle := time.NewTimer(sshIdleTimeout)
	if !idle.Stop() {
		select {
		case <-idle.C:
		default:
		}
	}
	defer idle.Stop()

	var werr error
	waiting := true
	for waiting {
		select {
		case werr = <-waitDone:
			waiting = false
		case <-idle.C:
			// Wait 未返回但输出已静默超时 → 判定命令已完成（PTY 被后台进程持有等），强制收尾
			slog.Info("SSH 流式执行静默超时，判定完成", "id", session.id, "timeout", sshIdleTimeout)
			transport.Close()
			werr = <-waitDone
			waiting = false
		case <-dataCh:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(sshIdleTimeout)
		}
	}

	// 排空尾部输出：读 goroutine 将因远程 channel 关闭或 Close()（关闭 stdout pipe）退出
	transport.Close()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		// 防御性兜底：不等待 readDone（读 goroutine 若仍阻塞则随会话一起丢弃，避免卡死收尾）
	}

	session.mu.Lock()
	if werr != nil {
		session.exitError = werr.Error()
	}
	session.mu.Unlock()
}

// runSessionLines 会话式逐行执行驱动：
// 向交互式 shell 逐行写入命令（+ 回车），每行后写哨兵行 echo OPS_SENTINEL_<行号>_<随机>，
// 检测到哨兵输出即判定该行执行完成（PTY 自动回显命令，输出顺序 = 真实终端）。
// 全部行完成后发送 exit 关闭 shell。
// sanitizeShellData 会话行输出的统一清理：剥哨兵 → 启动噪声清洗（\x1b[2J 清屏/定位/光标显隐/OSC/填屏）→ 剥提示符。
// PowerShell 冷启动 >300ms 时 ConPTY 初始化序列（\x1b[?25l\x1b[2J\x1b[m\x1b[H）在 drainReader 排空窗口之后到达，
// 会混入 feedShellLine 的第一行输出——若不剥掉，\x1b[2J 清屏会清掉前端已显示的 "$ command" 与历史内容。
func sanitizeShellData(lineData, sentinel string) string {
	return stripPrompt(sanitizeStartupNoise(stripSentinel(lineData, sentinel)))
}

// feedShellLine 向交互 shell 写入一行命令并收集该行全部输出：
// 写行 + 哨兵 echo OPS_SENTINEL_<随机>，回显/输出两个哨兵都出现（Count>=2）判定完成；
// 返回去除哨兵、剥离提示符后的该行输出。供流式会话（runSessionLines）与批量同步会话（runSessionBatchSync）复用。
// 读取经 shellReader（会话级单 reader，无泄漏/竞争）；双哨兵后排空残留提示符。
func feedShellLine(r *shellReader, line string) (string, error) {
	line = strings.TrimLeft(line, " \t")
	if _, err := r.transport.Write([]byte(line + "\r")); err != nil {
		return "", err
	}
	sentinel := fmt.Sprintf("OPS_SENTINEL_%d", rand.Intn(1000000))
	if _, err := r.transport.Write([]byte("echo " + sentinel + "\r")); err != nil {
		return "", err
	}

	// 累积本行全部输出（回显 + 执行结果 + 哨兵回显 + 哨兵输出），双哨兵判定完成
	// 注意：不用 NormalizePTY（它会 \r→\n 且剥 ANSI，导致 scp/wget 进度条被拆成多行）；
	// 保留原始流（\r 原地更新 + ANSI 颜色由 xterm 处理），跨块解码解决多字节截断。
	decoder := &outputDecoder{}
	var lineData string
	for {
		chunk, ok, err := r.readChunk(10 * time.Second)
		if err != nil {
			return sanitizeShellData(lineData, sentinel), fmt.Errorf("会话读取失败: %w", err)
		}
		if !ok {
			return sanitizeShellData(lineData, sentinel), errors.New("会话读取提前结束")
		}
		if chunk == nil {
			// 10s 无数据：命令仍在运行（长任务），继续等待
			continue
		}
		lineData += decoder.Decode(chunk)
		if strings.Count(lineData, sentinel) >= 2 {
			break
		}
	}
	if strings.Count(lineData, sentinel) < 2 {
		// 读取提前结束（会话异常/被停止），本行未完整执行
		return sanitizeShellData(lineData, sentinel), errors.New("会话读取提前结束")
	}
	// 排空哨兵后的残留（新提示符/换行）：残留未消费会导致下一行命令回显时光标状态
	// 错位（命令前出现大量空格）。经会话级 reader 排空，无泄漏/竞争。
	drainReader(r, 150*time.Millisecond)
	cleaned := sanitizeShellData(lineData, sentinel)
	// 剥离 shell 命令回显首行（前端已统一展示 $ command）：避免命令重复显示
	cleaned = stripEchoLine(cleaned, line)
	return cleaned, nil
}

// stripEchoLine 剥离 shell 对命令的回显行（提示符已由 stripPrompt 剥掉）：
// 输出首行若与该命令文本一致则删除——命令由前端/批量层统一展示，回显重复且
// 可能因脚本缩进而错位。仅在"首行整行匹配"时剥离（安全降级：不匹配则保留）。
func stripEchoLine(cleaned, line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if cleaned == trimmed {
		return ""
	}
	if strings.HasPrefix(cleaned, trimmed+"\r\n") {
		return cleaned[len(trimmed)+2:]
	}
	if strings.HasPrefix(cleaned, trimmed+"\n") {
		return cleaned[len(trimmed)+1:]
	}
	return cleaned
}

// shellReader 会话级 shell 读取器：会话内唯一读取方，通过 channel 分发读取结果。
// 替代逐次启动的 drain goroutine——旧 drainShellOutput 在超时后 goroutine 仍阻塞在
// Read 上不退出（os.File.Read 无法中断），泄漏的 goroutine 会抢走下一行命令的输出，
// 导致回显丢失/错位（"回显错位"反复出现的隐藏根因之一）。
type shellReader struct {
	transport pty.Transport
	data      chan []byte
	err       chan error
}

func newShellReader(transport pty.Transport) *shellReader {
	r := &shellReader{
		transport: transport,
		data:      make(chan []byte, 128),
		err:       make(chan error, 1),
	}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, rerr := transport.Read(buf)
			if n > 0 {
				r.data <- append([]byte{}, buf[:n]...)
			}
			if rerr != nil {
				r.err <- rerr
				close(r.data)
				return
			}
		}
	}()
	return r
}

// readChunk 读取一块数据。timeout 内无新数据返回 (nil, true, nil)（空闲判定）；
// channel 关闭返回 (nil, false, nil)；读取错误返回 (nil, false, err)。
func (r *shellReader) readChunk(timeout time.Duration) ([]byte, bool, error) {
	select {
	case b, ok := <-r.data:
		return b, ok, nil
	case err := <-r.err:
		return nil, false, err
	case <-time.After(timeout):
		return nil, true, nil
	}
}

// drainReader 消费 shellReader 的当前残留数据（timeout 空闲窗口，丢弃数据）。
// 用于：① shell 启动横幅/清屏序列排空；② feedShellLine 双哨兵后的提示符残留排空。
// 与旧 drainShellOutput 不同：无 goroutine 泄漏（复用会话级 reader，无竞争）。
func drainReader(r *shellReader, window time.Duration) {
	for {
		chunk, ok, err := r.readChunk(window)
		if chunk == nil || err != nil || !ok {
			return
		}
	}
}

// ==================== ConPTY 启动噪声清洗 ====================
// Windows 11 ConPTY 在子进程（cmd/powershell/bash 均可）启动时统一注入初始化序列：
//
//	\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H
//
// 其中 \x1b[2J（清屏）+ \x1b[H（光标归位）被原样推给 xterm 会清掉前端已写入的命令行、
// 把输出挤到最顶部、覆盖可见历史（用户反馈"结果覆盖命令展示、总是在最上面、历史被覆盖"）。
// cmd 额外在每条命令输出后输出 \x1b[K\r\n 填满 30 行 ConPTY 屏幕（清行+换行）。
// 这些序列对运维工具无展示价值，统一剥离。
var (
	// ConPTY 统一初始化序列（启用 VT、光标显隐、清屏、重置、归位）
	reConPTYInit = regexp.MustCompile(`\x1b\[\?9001h\x1b\[\?1004h\x1b\[\?25l\x1b\[2J\x1b\[m\x1b\[H`)
	// 兜底：孤立清屏+重置+归位（其他 Windows 版本/终端序列可能有差异）
	reClearHome = regexp.MustCompile(`\x1b\[2J\x1b\[m\x1b\[H`)
	// cmd 填屏：连续"清行+换行"（≥2 次），可尾随光标定位（cmd 把光标放回屏幕顶部附近）
	reCmdFillScreen = regexp.MustCompile(`(?:\x1b\[K\r?\n){2,}(?:\x1b\[[0-9]+;[0-9]+H)?`)
	// 孤立清行残片：\x1b[K 后跟 ESC 序列或行尾（cmd 填屏的最后一个 \x1b[K 无 \r\n）
	reLoneClearK = regexp.MustCompile(`\x1b\[K(\x1b|$)`) // 捕获组保留后随 ESC/结尾，替换为 $1
	// cmd 光标定位序列：\x1b[<行>;<列>H（cmd 用屏幕坐标重绘提示符，剥掉避免 xterm 光标跳位）
	reCmdCursorPos = regexp.MustCompile(`\x1b\[[0-9]+;[0-9]+H`)
	// 光标显隐切换：\x1b[?25l / \x1b[?25h（cmd 重绘提示符时反复切换，剥掉保持 xterm 光标可见）
	reCursorShow = regexp.MustCompile(`\x1b\[\?25[hl]`)
	// OSC 标题序列：\x1b]0;...\x07（设置窗口标题，工具内无意义；跨帧拆分会让 xterm 挂起解析）
	reOSCTitle = regexp.MustCompile(`\x1b\][^\x07]*\x07`)
	// ConPTY VT 启用残片：\x1b[?9001h（启用 VT 模式）/ \x1b[?1004h（启用 focus 事件）。
	// PowerShell 初始化序列不稳定（实测 pwd 输出 `\x1b[?9001h\x1b[?1004h\x1b[2J\x1b[m`，
	// 缺 ?25l 与 H），完整序列 pattern 匹配不上时这些残片单独剥。
	reVTEnable9001 = regexp.MustCompile(`\x1b\[\?9001h`)
	reVTEnable1004 = regexp.MustCompile(`\x1b\[\?1004h`)
	// 清屏+重置残片（无 H）：PowerShell 注入的 \x1b[2J\x1b[m；不匹配用户 cls 的 \x1b[2J\x1b[H（无 \x1b[m）
	reClearReset = regexp.MustCompile(`\x1b\[2J\x1b\[m`)
)

// sanitizeStartupNoise 剥离 ConPTY 初始化清屏序列、cmd 填屏/定位/光标显隐序列与 OSC 标题，
// 避免它们被推给 xterm 导致清屏覆盖历史、输出置顶。
// 填屏收敛为单个 \r\n：cmd 输出行尾自带 \x1b[K\r\n（清行+换行），整段剥空会连正常行尾换行一起吞掉
// （「执行完成」等提示紧贴上一行）；保留一个 \r\n 既消除 30 行填屏噪声，又不破坏行尾换行。
func sanitizeStartupNoise(s string) string {
	s = reConPTYInit.ReplaceAllString(s, "")
	s = reVTEnable9001.ReplaceAllString(s, "")
	s = reVTEnable1004.ReplaceAllString(s, "")
	s = reClearHome.ReplaceAllString(s, "")
	s = reClearReset.ReplaceAllString(s, "")
	s = reCmdFillScreen.ReplaceAllString(s, "\r\n")
	s = reLoneClearK.ReplaceAllString(s, "$1")
	s = reCmdCursorPos.ReplaceAllString(s, "")
	s = reCursorShow.ReplaceAllString(s, "")
	s = reOSCTitle.ReplaceAllString(s, "")
	return s
}

// sanitizeInteractiveNoise 交互模式专用清洗：剥"完整 ConPTY 初始化序列"、cmd 填屏、OSC 标题，
// 以及 PowerShell 冷启动漏剥的清屏/光标显隐/定位序列。
// **不**剥孤立的 \x1b[2J 清屏（保留用户 cls/clear 等主动清屏的真实行为——cls 输出 \x1b[2J\x1b[H，
// 与 ConPTY 注入的 \x1b[2J\x1b[m\x1b[H 不同，reClearHome/reClearReset 不会误伤）。
// \x1b[?25l/h（光标显隐）与 \x1b[<r>;<c>H（定位）是 PowerShell host 命令执行期间的重绘噪声，
// 交互读取必须剥掉，否则光标状态异常导致后续输出覆盖/位置错乱（xterm 自身管理光标）。
func sanitizeInteractiveNoise(s string) string {
	s = reConPTYInit.ReplaceAllString(s, "")
	s = reVTEnable9001.ReplaceAllString(s, "")
	s = reVTEnable1004.ReplaceAllString(s, "")
	s = reClearHome.ReplaceAllString(s, "")
	s = reClearReset.ReplaceAllString(s, "")
	s = reCmdFillScreen.ReplaceAllString(s, "\r\n")
	s = reOSCTitle.ReplaceAllString(s, "")
	s = reCmdCursorPos.ReplaceAllString(s, "")
	s = reCursorShow.ReplaceAllString(s, "")
	return s
}

// oscStripper 全流跨帧剥离 OSC 标题序列（\x1b]...\x07）。
// 背景：PowerShell 在命令执行**后**输出 OSC 更新窗口标题，且可能被 Read 分块；
// 帧级正则（reOSCTitle 需要完整闭合）跨帧匹配不上，残留的未闭合 OSC 会让 xterm
// 挂起解析等待 \x07。OSC 设置标题对工具无内容价值，任意位置剥离。
type oscStripper struct {
	pending string // 未闭合 OSC 前缀（等待 \x07）
}

func (os *oscStripper) Process(chunk string) string {
	data := os.pending + chunk
	os.pending = ""
	if !strings.Contains(data, "\x1b]") {
		return data
	}
	var out strings.Builder
	for {
		idx := strings.Index(data, "\x1b]")
		if idx < 0 {
			out.WriteString(data)
			break
		}
		out.WriteString(data[:idx])
		data = data[idx:]
		if end := strings.IndexByte(data, 0x07); end >= 0 {
			data = data[end+1:]
			continue
		}
		os.pending = data
		break
	}
	return out.String()
}

// noiseStripper 流式剥离输出开头的 ConPTY 启动噪声（**跨帧安全**）。
// 背景：Win11 ConPTY 注入的初始化序列（\x1b[?9001h...\x1b[2J\x1b[m\x1b[H 等）可能被
// Read 分块到达——PowerShell 冷启动慢、输出节奏不连续，序列常被拆到多个帧；
// 帧级正则（sanitizeStartupNoise）在跨帧时匹配不上 → \x1b[2J 清屏漏剥 → xterm 里
// 结果覆盖命令展示。stripper 按"开头前缀匹配"跨帧累积剥离，遇到第一个非噪声内容
// （普通文本/命令输出）即结束（done），不影响后续实时输出。
type noiseStripper struct {
	pending []byte // 未确认的启动噪声前缀缓存（等待后续帧补齐）
	done    bool
}

// startupNoisePatterns 启动噪声完整序列（长序列在前，避免短序列抢先匹配）。
// 含 \r\n 换行：ConPTYInit/填屏序列之间常夹换行，启动阶段的换行是布局噪声。
var startupNoisePatterns = [][]byte{
	[]byte("\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H"), // ConPTYInit（Win11）
	[]byte("\x1b[?9001h\x1b[?1004h\x1b[2J\x1b[m"),                  // ConPTYInit 变体（缺 ?25l/H，PowerShell pwd 实测）
	[]byte("\x1b[?9001h\x1b[?1004h"),                              // VT 启用残片
	[]byte("\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H"),
	[]byte("\x1b[?25l\x1b[2J\x1b[m\x1b[H"),
	[]byte("\x1b[?9001h"),
	[]byte("\x1b[?1004h"),
	[]byte("\x1b[2J\x1b[m"),
	[]byte("\x1b[?25l"),
	[]byte("\x1b[?25h"),
	[]byte("\x1b[2J"),
	[]byte("\x1b[m"),
	[]byte("\x1b[H"),
	[]byte("\x1b[K"),
	[]byte("\r\n"),
	[]byte("\n"),
	[]byte("\r"),
}

// Process 处理一帧数据：剥离开头的启动噪声，返回应推送的内容。
// 调用方须按序传入所有帧（含空帧无需调用）。
func (ns *noiseStripper) Process(chunk string) string {
	if ns.done {
		return chunk
	}
	data := append([]byte(nil), ns.pending...)
	data = append(data, chunk...)
	ns.pending = ns.pending[:0]

	for len(data) > 0 {
		// 1. OSC 标题序列：\x1b]...\x07（未闭合则缓存等待）
		if data[0] == 0x1b && len(data) > 1 && data[1] == ']' {
			if idx := bytes.IndexByte(data, 0x07); idx >= 0 {
				data = data[idx+1:]
				continue
			}
			ns.pending = append(ns.pending, data...)
			return ""
		}
		// 2. 完整噪声序列
		matched := false
		for _, pat := range startupNoisePatterns {
			if bytes.HasPrefix(data, pat) {
				data = data[len(pat):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// 3. data 是某噪声序列的前缀（需更多帧补齐）
		for _, pat := range startupNoisePatterns {
			if len(data) < len(pat) && bytes.HasPrefix(pat, data) {
				ns.pending = append(ns.pending, data...)
				return ""
			}
		}
		// 4. 非噪声内容 → 启动阶段结束，剩余内容正常推送
		ns.done = true
		return string(data)
	}
	return ""
}

// runSessionLines 流式会话驱动：逐行喂入（feedShellLine），输出实时累积并推送 xterm 事件
func (s *OpsService) runSessionLines(session *streamSession, transport pty.Transport) {
	// 会话级单 reader（唯一读取方，无泄漏/竞争）
	reader := newShellReader(transport)
	// 排空 shell 启动输出（cmd 横幅 + ConPTY 清屏序列 + 初始提示符）：
	// 不推给前端，避免 \x1b[2J 清屏覆盖 xterm 里已有的命令展示与历史。
	drainReader(reader, 300*time.Millisecond)

	defer func() {
		// 会话结束：发送 exit 优雅退出，超时则强杀
		transport.Write([]byte("exit\r"))
		doneCh := make(chan struct{})
		go func() {
			transport.Wait()
			close(doneCh)
		}()
		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			transport.Kill()
		}
		transport.Close()
		s.ensureTrailingNewline(session)
		session.mu.Lock()
		session.done = true
		exitErr := session.exitError
		session.mu.Unlock()
		// xterm 会话结束信号
		s.emitTerminalOutput(session.id, "", true, exitErr)
	}()

	for {
		session.mu.Lock()
		if session.stopped || session.lineIdx >= len(session.lines) {
			session.mu.Unlock()
			return
		}
		line := session.lines[session.lineIdx]
		session.lineIdx++
		session.mu.Unlock()

		cleaned, err := feedShellLine(reader, line)
		if cleaned != "" {
			session.mu.Lock()
			session.output = append(session.output, []byte(cleaned)...)
			session.mu.Unlock()
			// 推送该行处理结果（已剥哨兵/提示符/启动噪声，保留 ANSI）
			s.emitTerminalOutput(session.id, cleaned, false, "")
		}
		if err != nil {
			session.mu.Lock()
			session.exitError = err.Error()
			session.mu.Unlock()
			return
		}
	}
}

// stripPrompt 剥离 shell 提示符（cmd/PS 的 "盘符:\路径>"，bash 行首 "xxx$ "）。
// 提示符常与输出粘连（如 "value=hello-opsD:\path>"），字符串级删除避免按行误伤。
// xterm 时代提示符可能穿插 ANSI 颜色码（PowerShell profile 配色），需容错匹配。
var (
	// ANSI CSI 序列片段：\x1b[...字母（可穿插在提示符中间或尾随，如 \x1b[1C 光标右移）
	ansiSeq = `(?:\x1b\[[0-9;?]*[a-zA-Z])*`
	// 行首提示符：可穿插 ANSI 的 "PS C:\path>" / "C:\path>"，吃掉尾随 ANSI 与空格
	reCmdPromptLine = regexp.MustCompile(`(?m)^` + ansiSeq + `(?:PS ` + ansiSeq + `)?[A-Za-z]:\\` + ansiSeq + `[^>\r\n]*>` + ansiSeq + `[ \t]*`)
	// 粘连提示符（不在行首，如 "value=hello-opsD:\path>"）
	reCmdPromptGlue = regexp.MustCompile(`(?:PS )?[A-Za-z]:\\[^>\r\n]*>`)
	// bash 行首提示符（可穿插 ANSI）
	reBashPrompt = regexp.MustCompile(`(?m)^` + ansiSeq + `[^\r\n$]*\$ `)
	// PowerShell 提示符尾巴的光标右移序列（strip 后残留会导致后续文本列偏移）
	reCursorRight = regexp.MustCompile(`\x1b\[\d*C`)
)

func stripPrompt(s string) string {
	s = reCmdPromptLine.ReplaceAllString(s, "")
	s = reCmdPromptGlue.ReplaceAllString(s, "")
	s = reBashPrompt.ReplaceAllString(s, "")
	// 清理提示符尾巴残留的光标右移（\x1b[1C 等），避免 xterm 从错误列渲染
	s = reCursorRight.ReplaceAllString(s, "")
	// 清理 cmd 重绘噪声（光标定位/显隐/OSC 标题——reCmdCursorPos/reCursorShow/reOSCTitle
	// 声明于上方 sanitize 部分的 var 块，包级变量跨块引用），避免 xterm 光标跳位或挂起解析
	s = reCmdCursorPos.ReplaceAllString(s, "")
	s = reCursorShow.ReplaceAllString(s, "")
	s = reOSCTitle.ReplaceAllString(s, "")
	return s
}

// stripSentinel 移除输出中的哨兵痕迹（字符串级，兼容 cmd/PS 提示符不换行导致的粘连）：
//   - 完整哨兵：命令回显形式 "{提示符}echo SENTINEL" → 删 "echo SENTINEL" 保留提示符；
//     输出形式 "SENTINEL{提示符}" → 删 "SENTINEL" 保留提示符
//   - 截断残片：PS 提示符重绘可能把哨兵拆成不完整片段（如 "OPS_SENTINEL_2_21"），按前缀+数字清理
// 只匹配哨兵紧邻前的 "echo "，避免误删用户命令输出中的 echo 字样。
func stripSentinel(data, sentinel string) string {
	// 1. 完整哨兵
	for strings.Contains(data, sentinel) {
		idx := strings.Index(data, sentinel)
		pre := data[:idx]
		if len(pre) >= 5 && strings.HasSuffix(pre, "echo ") {
			// 回显形式（提示符+echo，如 ">echo SENTINEL" / "$ echo SENTINEL"）：删除 "echo "（5 字符），保留提示符
			data = pre[:len(pre)-5] + data[idx+len(sentinel):]
		} else {
			// 输出形式：仅删除哨兵本身
			data = pre + data[idx+len(sentinel):]
		}
	}
	// 2. 截断残片：哨兵前缀 + 紧随数字（PS 重绘插入导致的不完整哨兵）
	prefix := sentinel[:strings.LastIndex(sentinel, "_")]
	for {
		i := strings.Index(data, prefix)
		if i < 0 {
			break
		}
		start := i
		// 残片前紧邻 "echo "（回显残片）一并删除
		if start >= 5 && strings.HasSuffix(data[:start], "echo ") {
			start -= 5
		}
		// 吃掉前缀后的数字
		j := i + len(prefix)
		for j < len(data) && data[j] >= '0' && data[j] <= '9' {
			j++
		}
		data = data[:start] + data[j:]
	}
	return data
}

// runStreamLines 流式执行驱动（进程模式）：每行用 PTY 启动（ConPTY），实时输出 + 等待退出 → 下一行。
// PTY 让程序检测到 TTY 后行缓冲/即时刷新——解决管道全缓冲导致"运行完才显示全部输出"的问题
// （pnpm run build 这类持续输出命令在管道下攒到结束才 flush），同时保留 ANSI 颜色。
// 任一行失败不中断，最终 done 时 exitError 记录最后失败信息。
func (s *OpsService) runStreamLines(session *streamSession, interpreter string) {
	defer func() {
		s.ensureTrailingNewline(session)
		session.mu.Lock()
		session.done = true
		exitErr := session.exitError
		session.mu.Unlock()
		// xterm 会话结束信号
		s.emitTerminalOutput(session.id, "", true, exitErr)
	}()

	for {
		session.mu.Lock()
		if session.stopped || session.lineIdx >= len(session.lines) {
			session.mu.Unlock()
			return
		}
		line := session.lines[session.lineIdx]
		session.lineIdx++
		session.mu.Unlock()

		// PTY 启动当前行命令（ConPTY，stdout/stderr 由控制台合并）
		transport, err := pty.StartPTY(line, interpreter)
		if err != nil {
			msg := "! 行启动失败: " + err.Error() + "\n"
			session.mu.Lock()
			session.output = append(session.output, []byte(msg)...)
			session.exitError = err.Error()
			session.mu.Unlock()
			s.emitTerminalOutput(session.id, msg, false, "")
			continue
		}
		session.mu.Lock()
		session.transport = transport
		session.mu.Unlock()

		// 读取该行输出（跨块解码 + xterm 事件推送，与交互式会话一致）。
		// 注意：ConPTY 子进程退出后输出管道不会自动 EOF，Read 会一直阻塞，
		// 因此读取放到子 goroutine，主流程以 Wait() 判定进程退出后，采用
		// "排空到静默"结束：进程退出后每读到数据就重置静默计时，超过阈值无新
		// 数据才关闭管道，确保尾部输出（ConPTY 延迟 flush/子进程收尾打印）不被丢弃。
		decoder := &outputDecoder{}
		buf := make([]byte, 8192)
		readDone := make(chan struct{})
		// 每行新进程 → 每行独立剥离启动噪声（跨帧安全，防 PowerShell 分块漏剥清屏）；
		// oscStripper 全流剥 OSC 标题（PS 在命令执行后输出，帧级正则会漏）
		stripper := &noiseStripper{}
		osc := &oscStripper{}
		idle := time.NewTimer(time.Hour)
		if !idle.Stop() {
			<-idle.C
		}
		defer idle.Stop()
		go func() {
			defer close(readDone)
			for {
				n, rerr := transport.Read(buf)
				if n > 0 {
					// 跨帧剥离启动噪声 → 帧级正则兜底（cmd 填屏/定位等）→ 全流 OSC 剥离
					data := osc.Process(sanitizeStartupNoise(stripper.Process(decoder.Decode(buf[:n]))))
					if data != "" {
						session.mu.Lock()
						session.output = append(session.output, []byte(data)...)
						session.mu.Unlock()
						s.emitTerminalOutput(session.id, data, false, "")
					}
					// 有数据：重置静默计时（进程退出后才有意义）
					if !idle.Stop() {
						select {
						case <-idle.C:
						default:
						}
					}
					idle.Reset(300 * time.Millisecond)
				}
				if rerr != nil {
					return
				}
			}
		}()

		werr := transport.Wait()
		// 进程已退出：等待读取 goroutine 读完尾部输出（静默超时）或兜底超时后关闭
		select {
		case <-readDone:
		case <-idle.C:
			transport.Close()
			<-readDone
		case <-time.After(2 * time.Second):
			transport.Close()
			<-readDone
		}
		transport.Close()
		session.mu.Lock()
		session.transport = nil
		if werr != nil {
			session.exitError = werr.Error()
		}
		session.mu.Unlock()
	}
}

// GetStreamOutput 获取流式执行的新输出（前端轮询调用）
func (s *OpsService) GetStreamOutput(req GetStreamOutputRequest) GetInteractiveOutputResponse {
	if _, ok := validateSession(req.Token); !ok {
		return GetInteractiveOutputResponse{Success: false, Message: "会话已过期"}
	}

	s.mu.Lock()
	session, exists := s.streamSessions[req.ID]
	s.mu.Unlock()

	if !exists {
		return GetInteractiveOutputResponse{Success: false, Message: "命令未在执行"}
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	var newOutput string
	if session.readPos < len(session.output) {
		newOutput = string(session.output[session.readPos:])
		session.readPos = len(session.output)
	}

	return GetInteractiveOutputResponse{
		Success:   true,
		Output:    newOutput,
		Done:      session.done,
		ExitError: session.exitError,
	}
}

// StopStream 停止流式执行（终止整个进程树）
func (s *OpsService) StopStream(req StopStreamRequest) ProcessResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ProcessResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName string
	err := db.QueryRow("SELECT name FROM commands WHERE id = ?", req.ID).Scan(&cmdName)
	if err == sql.ErrNoRows {
		return ProcessResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return ProcessResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	s.mu.Lock()
	session, exists := s.streamSessions[req.ID]
	if !exists {
		s.mu.Unlock()
		return ProcessResponse{Success: false, Message: cmdName + " 未在执行"}
	}
	delete(s.streamSessions, req.ID)
	session.mu.Lock()
	session.stopped = true
	rec := session.rec
	cancel := session.cancel
	transport := session.transport
	session.mu.Unlock()
	s.mu.Unlock()

	// 终止当前行进程树（Windows: Job Object / taskkill；Unix: 进程组）
	// 会话模式终止交互 shell（ConPTY 进程树）；行间间隙 rec 为 nil：靠 stopped 标记退出
	if cancel != nil {
		cancel()
	}
	if rec != nil {
		if err := rec.Terminate(); err != nil {
			slog.Error("停止流式执行失败", "id", req.ID, "name", cmdName, "error", err)
			return ProcessResponse{Success: false, Message: "停止失败: " + err.Error()}
		}
	}
	if transport != nil {
		transport.Kill()
		transport.Close()
	}

	slog.Info("流式命令已停止", "id", req.ID, "name", cmdName)
	return ProcessResponse{Success: true, Message: cmdName + " 已停止", Running: false}
}

// ==================== 守护进程命令管理 ====================

// StartDaemon 启动守护进程类型命令（后台运行）
func (s *OpsService) StartDaemon(req StartDaemonRequest) ProcessResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ProcessResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName, cmdText, cmdType, mode, envName, interpreter string
	var connID int
	err := db.QueryRow(`SELECT c.name, c.command, c.type, c.mode, COALESCE(c.connection_id, 0), e.name, COALESCE(c.interpreter, 'cmd') FROM commands c
		INNER JOIN environments e ON c.environment_id = e.id
		WHERE c.id = ?`, req.ID).Scan(&cmdName, &cmdText, &cmdType, &mode, &connID, &envName, &interpreter)
	if err == sql.ErrNoRows {
		return ProcessResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return ProcessResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	if cmdType != "daemon" {
		return ProcessResponse{Success: false, Message: "该命令不是守护进程类型，请使用运行按钮"}
	}

	// CMD 不支持多行——仅本地 terminal 模式适用（cmd /s /c 包装才受换行/引号影响；
	// SSH 模式命令由远程 sh/bash 解释，不受本地 cmd 多行限制）
	if mode == "" || mode == "terminal" {
		if err := validateScriptForInterpreter(interpreter, cmdText); err != nil {
			return ProcessResponse{Success: false, Message: err.Error()}
		}
	}

	// 加载执行器（ssh 等远程模式暂不支持守护进程）
	conn, err := loadConnectionByID(connID)
	if err != nil {
		return ProcessResponse{Success: false, Message: err.Error()}
	}
	ex, err := executorFor(mode, conn, interpreter)
	if err != nil {
		return ProcessResponse{Success: false, Message: err.Error()}
	}

	s.mu.Lock()
	s.ensureMap()

	if existing, exists := s.cmdDaemons[req.ID]; exists {
		if existing.Running() {
			s.mu.Unlock()
			return ProcessResponse{Success: false, Message: cmdName + " 已在运行中", Running: true}
		}
		delete(s.cmdDaemons, req.ID)
	}

	// 启动守护进程（terminal: Job Object 进程树管理）
	rec, err := ex.StartDaemon(cmdText)
	if err != nil {
		s.mu.Unlock()
		if errors.Is(err, exec.ErrDaemonUnsupported) {
			slog.Warn("守护进程类型不受支持", "id", req.ID, "name", cmdName, "mode", mode, "error", err)
			return ProcessResponse{Success: false, Message: err.Error()}
		}
		slog.Error("启动守护进程失败", "id", req.ID, "name", cmdName, "error", err)
		return ProcessResponse{Success: false, Message: "启动失败: " + err.Error()}
	}

	s.cmdDaemons[req.ID] = rec
	pid := rec.PID()
	s.mu.Unlock()

	slog.Info("守护进程已启动", "id", req.ID, "name", cmdName, "env", envName, "interpreter", interpreter, "pid", pid)

	go func() {
		err := rec.Wait()
		s.mu.Lock()
		if cur, ok := s.cmdDaemons[req.ID]; ok && cur == rec {
			delete(s.cmdDaemons, req.ID)
		}
		s.mu.Unlock()
		// 释放 Job 句柄（KILL_ON_JOB_CLOSE 会终止 Job 内残留的子进程）
		rec.Cleanup()
		if err != nil {
			slog.Warn("守护进程退出", "id", req.ID, "name", cmdName, "pid", pid, "error", err)
		} else {
			slog.Info("守护进程正常退出", "id", req.ID, "name", cmdName, "pid", pid)
		}
	}()

	time.Sleep(2 * time.Second)

	s.mu.Lock()
	_, stillRunning := s.cmdDaemons[req.ID]
	s.mu.Unlock()

	if !stillRunning {
		output := strings.TrimSpace(rec.StderrText())
		msg := cmdName + " 启动失败，进程已退出"
		if output != "" {
			msg += "：" + output
		}
		slog.Error("守护进程启动后立即退出", "id", req.ID, "name", cmdName, "stderr", output)
		return ProcessResponse{Success: false, Message: msg}
	}

	return ProcessResponse{Success: true, Message: cmdName + " 已启动", Running: true}
}

// StopDaemon 停止守护进程类型命令（终止整个进程树）
func (s *OpsService) StopDaemon(req StopDaemonRequest) ProcessResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ProcessResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName string
	err := db.QueryRow("SELECT name FROM commands WHERE id = ?", req.ID).Scan(&cmdName)
	if err == sql.ErrNoRows {
		return ProcessResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return ProcessResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	s.mu.Lock()
	rec, exists := s.cmdDaemons[req.ID]
	if !exists {
		s.mu.Unlock()
		return ProcessResponse{Success: false, Message: cmdName + " 未在运行"}
	}
	delete(s.cmdDaemons, req.ID)
	pid := rec.PID()
	s.mu.Unlock()

	// 终止整个进程树（Windows: Job Object / Unix: 进程组）
	if err := rec.Terminate(); err != nil {
		slog.Error("停止守护进程失败", "id", req.ID, "name", cmdName, "pid", pid, "error", err)
		return ProcessResponse{Success: false, Message: "停止失败: " + err.Error()}
	}

	slog.Info("守护进程已停止", "id", req.ID, "name", cmdName, "pid", pid)
	return ProcessResponse{Success: true, Message: cmdName + " 已停止", Running: false}
}

// Shutdown 应用退出时终止所有运行中的守护进程、交互式会话和流式执行
// （Windows 下 Job Object 的 KILL_ON_JOB_CLOSE 也会兜底，这里显式清理更可靠）
func (s *OpsService) Shutdown() {
	s.mu.Lock()
	daemons := make([]daemon.Record, 0, len(s.cmdDaemons))
	for id, rec := range s.cmdDaemons {
		daemons = append(daemons, rec)
		delete(s.cmdDaemons, id)
	}
	sessions := make([]*interactiveSession, 0, len(s.interactiveSessions))
	for id, session := range s.interactiveSessions {
		sessions = append(sessions, session)
		delete(s.interactiveSessions, id)
	}
	streams := make([]*streamSession, 0, len(s.streamSessions))
	for id, session := range s.streamSessions {
		streams = append(streams, session)
		delete(s.streamSessions, id)
	}
	s.mu.Unlock()

	for _, rec := range daemons {
		name := "pid=" + strconv.Itoa(rec.PID())
		rec.Terminate()
		slog.Info("应用退出，已终止守护进程", "name", name)
	}
	for _, session := range sessions {
		if session.transport != nil {
			session.transport.Kill()
			session.transport.Close()
		}
	}
	for _, session := range streams {
		session.mu.Lock()
		session.stopped = true
		rec := session.rec
		cancel := session.cancel
		transport := session.transport
		session.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if rec != nil {
			pid := rec.PID()
			rec.Terminate()
			slog.Info("应用退出，已终止流式执行", "pid", strconv.Itoa(pid))
		}
		if transport != nil {
			transport.Kill()
			transport.Close()
			slog.Info("应用退出，已终止会话式执行")
		}
	}
	slog.Info("应用退出，进程清理完成")
}

// ==================== 交互式命令管理 ====================

// StartInteractive 启动交互式命令（带 stdin 管道，前端可发送输入和轮询输出）
func (s *OpsService) StartInteractive(req StartInteractiveRequest) ProcessResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ProcessResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName, cmdText, cmdType, mode, envName, interpreter string
	var connID int
	err := db.QueryRow(`SELECT c.name, c.command, c.type, c.mode, COALESCE(c.connection_id, 0), e.name, COALESCE(c.interpreter, 'cmd') FROM commands c
		INNER JOIN environments e ON c.environment_id = e.id
		WHERE c.id = ?`, req.ID).Scan(&cmdName, &cmdText, &cmdType, &mode, &connID, &envName, &interpreter)
	if err == sql.ErrNoRows {
		return ProcessResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return ProcessResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	if cmdType != "interactive" {
		return ProcessResponse{Success: false, Message: "该命令不是交互式类型"}
	}

	// CMD 不支持多行——仅本地 terminal 模式适用（cmd /s /c 包装才受换行/引号影响；
	// SSH 模式命令由远程 sh/bash 解释，不受本地 cmd 多行限制）
	if mode == "" || mode == "terminal" {
		if err := validateScriptForInterpreter(interpreter, cmdText); err != nil {
			return ProcessResponse{Success: false, Message: err.Error()}
		}
	}

	// 检查是否已在运行
	s.mu.Lock()
	s.ensureMap()
	if existing, exists := s.interactiveSessions[req.ID]; exists {
		if !existing.done {
			s.mu.Unlock()
			return ProcessResponse{Success: false, Message: cmdName + " 已在运行中", Running: true}
		}
		delete(s.interactiveSessions, req.ID)
	}
	s.mu.Unlock()

	// 加载执行器（terminal: 本地 PTY；ssh: 远程 PTY）
	conn, err := loadConnectionByID(connID)
	if err != nil {
		return ProcessResponse{Success: false, Message: err.Error()}
	}
	ex, err := executorFor(mode, conn, interpreter)
	if err != nil {
		return ProcessResponse{Success: false, Message: err.Error()}
	}

	// 在伪终端(PTY)中启动进程，支持 sudo / ssh 等需要 TTY 的密码交互
	transport, err := ex.StartInteractive(cmdText)
	if err != nil {
		slog.Error("启动交互式命令失败", "id", req.ID, "name", cmdName, "mode", mode, "error", err)
		return ProcessResponse{Success: false, Message: "启动失败: " + err.Error()}
	}

	// 注册会话（再检查一次，避免并发重复启动）
	s.mu.Lock()
	if existing, exists := s.interactiveSessions[req.ID]; exists {
		if !existing.done {
			s.mu.Unlock()
			transport.Close()
			return ProcessResponse{Success: false, Message: cmdName + " 已在运行中", Running: true}
		}
		delete(s.interactiveSessions, req.ID)
	}
	session := &interactiveSession{transport: transport}
	s.interactiveSessions[req.ID] = session
	s.mu.Unlock()

	slog.Info("交互式命令已启动", "id", req.ID, "name", cmdName, "env", envName, "interpreter", interpreter)

	// goroutine: 读取 PTY 原始输出（stderr 已由控制台合并进同一输出流）
	// xterm 事件流：跨块解码后原样推送（保留 ANSI 控制码，前端 xterm 渲染），
	// 同时累积到 output 兼容旧的轮询 API。
	go func() {
		decoder := &outputDecoder{}
		buf := make([]byte, 8192)
		// 跨帧剥离启动噪声（PowerShell 冷启动分块，帧级正则会漏剥清屏）；
		// oscStripper 全流剥 OSC 标题（PS 在命令执行后输出）
		stripper := &noiseStripper{}
		osc := &oscStripper{}
		for {
			n, rerr := transport.Read(buf)
			if n > 0 {
				// 跨帧剥离 ConPTY 初始化清屏 → 帧级兜底（cmd 填屏/OSC，保留用户主动清屏）
				data := osc.Process(sanitizeInteractiveNoise(stripper.Process(decoder.Decode(buf[:n]))))
				if data != "" {
					session.mu.Lock()
					session.output = append(session.output, []byte(data)...)
					session.mu.Unlock()
					s.emitTerminalOutput(req.ID, data, false, "")
				}
			}
			if rerr != nil {
				break
			}
		}
		// 读循环退出 → 会话结束信号
		s.emitTerminalOutput(req.ID, "", true, "")
	}()

	// goroutine: 等待进程退出
	go func() {
		werr := transport.Wait()
		// 短暂等待 reader 排空最后的输出
		time.Sleep(150 * time.Millisecond)
		session.mu.Lock()
		session.done = true
		if werr != nil {
			session.exitError = werr.Error()
		}
		session.mu.Unlock()
		transport.Close()

		if werr != nil {
			slog.Warn("交互式命令退出", "id", req.ID, "name", cmdName, "error", werr)
		} else {
			slog.Info("交互式命令正常退出", "id", req.ID, "name", cmdName)
		}
	}()

	// 等待 2 秒检查进程是否立即退出
	time.Sleep(2 * time.Second)

	session.mu.Lock()
	isDone := session.done
	errMsg := session.exitError
	captured := string(session.output)
	session.mu.Unlock()

	if isDone {
		msg := cmdName + " 启动失败，进程已退出"
		if errMsg != "" {
			msg += "：" + errMsg
		}
		// 附上已捕获的输出（通常是 sudo/ssh 的真实报错），便于排查
		if captured != "" {
			captured = strings.TrimSpace(captured)
			if len(captured) > 2000 {
				captured = captured[:2000] + "\n... (输出已截断)"
			}
			msg += "\n\n输出：\n" + captured
		}
		// 常见原因提示：仅本地（terminal）模式时提示 PTY 用法（ssh 模式自身已分配远程 PTY）
		if mode == "" || mode == "terminal" {
			if strings.Contains(cmdText, "sudo") || strings.Contains(cmdText, "ssh") {
				msg += "\n\n提示：需要输入密码的命令（如 sudo / ssh 登录）必须分配伪终端(PTY)。" +
					"请给 ssh 加 -t 参数（例如 ssh -t user@host \"sudo ls\"）。"
			}
		}
		s.mu.Lock()
		delete(s.interactiveSessions, req.ID)
		s.mu.Unlock()
		slog.Error("交互式命令启动后立即退出", "id", req.ID, "name", cmdName, "error", errMsg)
		return ProcessResponse{Success: false, Message: msg}
	}

	return ProcessResponse{Success: true, Message: cmdName + " 已启动", Running: true}
}

// GetInteractiveOutput 获取交互式命令的新输出（前端轮询调用）
func (s *OpsService) GetInteractiveOutput(req GetInteractiveOutputRequest) GetInteractiveOutputResponse {
	if _, ok := validateSession(req.Token); !ok {
		return GetInteractiveOutputResponse{Success: false, Message: "会话已过期"}
	}

	s.mu.Lock()
	session, exists := s.interactiveSessions[req.ID]
	s.mu.Unlock()

	if !exists {
		return GetInteractiveOutputResponse{Success: false, Message: "交互式命令未在运行"}
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	var newOutput string
	if session.readPos < len(session.output) {
		newOutput = string(session.output[session.readPos:])
		session.readPos = len(session.output)
	}

	return GetInteractiveOutputResponse{
		Success:   true,
		Output:    newOutput,
		Done:      session.done,
		ExitError: session.exitError,
	}
}

// SendInteractiveInput 向交互式命令发送输入
func (s *OpsService) SendInteractiveInput(req SendInteractiveInputRequest) InteractiveInputResponse {
	if _, ok := validateSession(req.Token); !ok {
		return InteractiveInputResponse{Success: false, Message: "会话已过期"}
	}

	s.mu.Lock()
	session, exists := s.interactiveSessions[req.ID]
	s.mu.Unlock()

	if !exists {
		return InteractiveInputResponse{Success: false, Message: "交互式命令未在运行"}
	}

	session.mu.Lock()
	isDone := session.done
	session.mu.Unlock()

	if isDone {
		return InteractiveInputResponse{Success: false, Message: "交互式命令已结束"}
	}

	// 写入 PTY 输入（Windows 下将 \n 转为 \r，控制台以回车作为行结束符）
	_, err := session.transport.Write([]byte(pty.PreparePTYInput(req.Input)))
	if err != nil {
		return InteractiveInputResponse{Success: false, Message: "发送输入失败: " + err.Error()}
	}

	return InteractiveInputResponse{Success: true}
}

// ResizeTerminalRequest 调整终端尺寸请求
type ResizeTerminalRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
}

// ResizeTerminal 调整终端尺寸（xterm 前端窗口变化时同步到 PTY）
func (s *OpsService) ResizeTerminal(req ResizeTerminalRequest) ProcessResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ProcessResponse{Success: false, Message: "会话已过期"}
	}

	s.mu.Lock()
	isess, iok := s.interactiveSessions[req.ID]
	ssess, sok := s.streamSessions[req.ID]
	s.mu.Unlock()

	var transport pty.Transport
	switch {
	case iok:
		isess.mu.Lock()
		transport = isess.transport
		isess.mu.Unlock()
	case sok:
		ssess.mu.Lock()
		transport = ssess.transport
		ssess.mu.Unlock()
	default:
		return ProcessResponse{Success: false, Message: "会话不存在"}
	}

	if transport == nil {
		return ProcessResponse{Success: false, Message: "会话不支持调整尺寸"}
	}
	if err := transport.Resize(req.Cols, req.Rows); err != nil {
		return ProcessResponse{Success: false, Message: "调整终端尺寸失败: " + err.Error()}
	}
	return ProcessResponse{Success: true, Message: "终端尺寸已调整"}
}

// SendStreamInputRequest 流式会话输入请求（多行 shell 会话执行中的交互输入，如 sudo/ssh 密码）
type SendStreamInputRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
	Input string `json:"input"`
}

// SendStreamInput 向流式（会话模式）shell 发送输入
func (s *OpsService) SendStreamInput(req SendStreamInputRequest) InteractiveInputResponse {
	if _, ok := validateSession(req.Token); !ok {
		return InteractiveInputResponse{Success: false, Message: "会话已过期"}
	}

	s.mu.Lock()
	session, exists := s.streamSessions[req.ID]
	s.mu.Unlock()

	if !exists {
		return InteractiveInputResponse{Success: false, Message: "流式会话未在运行"}
	}

	session.mu.Lock()
	transport := session.transport
	isDone := session.done
	session.mu.Unlock()

	if isDone {
		return InteractiveInputResponse{Success: false, Message: "流式会话已结束"}
	}
	if transport == nil {
		return InteractiveInputResponse{Success: false, Message: "当前会话不支持交互输入（单行进程模式）"}
	}

	if _, err := transport.Write([]byte(pty.PreparePTYInput(req.Input))); err != nil {
		return InteractiveInputResponse{Success: false, Message: "发送输入失败: " + err.Error()}
	}
	return InteractiveInputResponse{Success: true}
}

// StopInteractive 停止交互式命令
func (s *OpsService) StopInteractive(req StopInteractiveRequest) ProcessResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ProcessResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName string
	err := db.QueryRow("SELECT name FROM commands WHERE id = ?", req.ID).Scan(&cmdName)
	if err == sql.ErrNoRows {
		return ProcessResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return ProcessResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	s.mu.Lock()
	session, exists := s.interactiveSessions[req.ID]
	if !exists {
		s.mu.Unlock()
		return ProcessResponse{Success: false, Message: cmdName + " 未在运行"}
	}
	delete(s.interactiveSessions, req.ID)
	s.mu.Unlock()

	// 终止进程并关闭伪终端
	if session.transport != nil {
		session.transport.Kill()
		session.transport.Close()
	}

	slog.Info("交互式命令已停止", "id", req.ID, "name", cmdName)
	return ProcessResponse{Success: true, Message: cmdName + " 已停止", Running: false}
}
