package server

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"opsflash/server/daemon"
)

// ==================== 指令库（命令管理：数据增删改查）====================
// v0.4.0：命令 CRUD + 执行目标（terminal 本地 / ssh 远程 / redis / mysql / tdengine 数据库）。
// 环境归属复用 ScriptsService 的环境管理（environments 表同源）。

// Command 命令信息
type Command struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Command        string `json:"command"`
	Remark         string `json:"remark"`
	Type           string `json:"type"`       // "non-interactive" | "interactive" | "daemon"
	Interpreter    string `json:"interpreter"` // "cmd" | "powershell" | "bash"，脚本解释器（仅 terminal 模式生效）
	SortOrder      int    `json:"sortOrder"`  // 排序值，越大越靠前，默认 10
	EnvironmentId  int    `json:"environmentId"`
	EnvName        string `json:"envName"`
	Running        bool   `json:"running"` // daemon/interactive 类型的运行状态
	CreatedAt      string `json:"createdAt"`
	Mode           string `json:"mode"`           // "terminal" | "ssh" | "redis" | "mysql" | "tdengine"
	ConnectionId   int    `json:"connectionId"`   // 执行目标连接 ID，0 = 本地（terminal）
	ConnectionName string `json:"connectionName"` // JOIN connections 带出，如 "prod-01@10.0.0.1"
}

// 请求类型：命令
type GetCommandsRequest struct {
	Token         string `json:"token"`
	EnvironmentId int    `json:"environmentId"`
}

type CreateCommandRequest struct {
	Token         string `json:"token"`
	Name          string `json:"name"`
	Command       string `json:"command"`
	Remark        string `json:"remark"`
	Type          string `json:"type"` // "non-interactive" | "interactive" | "daemon"
	Interpreter   string `json:"interpreter"` // "cmd" | "powershell" | "bash"
	SortOrder     int    `json:"sortOrder"`
	EnvironmentId int    `json:"environmentId"`
	Mode          string `json:"mode"`         // "terminal" | "ssh" | "redis" | "mysql" | "tdengine"
	ConnectionId  int    `json:"connectionId"` // 0 = 本地
}

type UpdateCommandRequest struct {
	Token         string `json:"token"`
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Command       string `json:"command"`
	Remark        string `json:"remark"`
	Type          string `json:"type"`
	Interpreter   string `json:"interpreter"`
	SortOrder     int    `json:"sortOrder"`
	EnvironmentId int    `json:"environmentId"`
	Mode          string `json:"mode"`
	ConnectionId  int    `json:"connectionId"`
}

type DeleteCommandRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

// 响应类型
type CommandsResponse struct {
	Success  bool      `json:"success"`
	Commands []Command `json:"commands"`
	Message  string    `json:"message"`
}

type CommandResponse struct {
	Success bool    `json:"success"`
	Command Command `json:"command"`
	Message string  `json:"message"`
}

type DeleteCommandResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// OpsService 指令库服务
type OpsService struct {
	mu                  sync.Mutex
	cmdDaemons          map[int]*daemon.Record       // 守护进程命令（按命令 ID 跟踪）
	interactiveSessions map[int]*interactiveSession // 交互式命令会话
	streamSessions      map[int]*streamSession      // 非交互式流式执行会话（实时输出）
	eventEmitter        EventEmitter                // 终端输出事件发射器（xterm 事件流，main 注入）
}

// EventEmitter 前端事件发射器（由 main 注入 application.EventManager）
type EventEmitter interface {
	Emit(name string, data ...interface{}) bool
}

// TerminalOutputEvent 终端输出事件（pty:output，后端 → 前端）
// Data 为 UTF-8 字节流（保留 ANSI 控制码，xterm 直接渲染）；
// Done=true 表示会话结束（读循环退出）。
type TerminalOutputEvent struct {
	ID        int    `json:"id"`
	Data      string `json:"data"`
	Done      bool   `json:"done"`
	ExitError string `json:"exitError"`
}

// SetEventEmitter 注入事件发射器（main.go 在创建 app 后调用）
func (s *OpsService) SetEventEmitter(ee EventEmitter) {
	s.eventEmitter = ee
}

// emitTerminalOutput 推送终端输出事件（xterm 事件流）
func (s *OpsService) emitTerminalOutput(id int, data string, done bool, exitError string) {
	if s.eventEmitter == nil {
		return
	}
	s.eventEmitter.Emit("pty:output", TerminalOutputEvent{ID: id, Data: data, Done: done, ExitError: exitError})
}

func (s *OpsService) ensureMap() {
	if s.cmdDaemons == nil {
		s.cmdDaemons = make(map[int]*daemon.Record)
	}
	if s.interactiveSessions == nil {
		s.interactiveSessions = make(map[int]*interactiveSession)
	}
	if s.streamSessions == nil {
		s.streamSessions = make(map[int]*streamSession)
	}
}

// validateModeAndConnection 校验执行模式与目标连接的一致性
// terminal 模式：connectionId 必须为 0；ssh/redis/mysql/tdengine 模式：连接必须存在且类型匹配
func validateModeAndConnection(mode string, connectionId int) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "terminal"
	}
	if !validCommandModes[mode] {
		return "", fmt.Errorf("执行模式无效，仅支持 terminal、ssh、redis、mysql、tdengine")
	}
	if mode == "terminal" {
		if connectionId != 0 {
			return "", errors.New("本地（terminal）模式无需选择连接")
		}
		return mode, nil
	}
	if connectionId <= 0 {
		return "", errors.New("请选择目标连接")
	}
	var connType string
	err := db.QueryRow("SELECT type FROM connections WHERE id = ?", connectionId).Scan(&connType)
	if err == sql.ErrNoRows {
		return "", errors.New("目标连接不存在")
	}
	if err != nil {
		return "", fmt.Errorf("查询连接失败: %w", err)
	}
	if connType != mode {
		return "", fmt.Errorf("连接类型与执行模式不匹配：连接 %d 是 %s 类型，命令模式是 %s", connectionId, connType, mode)
	}
	return mode, nil
}

// ==================== 命令管理 API ====================

// GetCommands 获取命令列表（可按环境筛选）
func (s *OpsService) GetCommands(req GetCommandsRequest) CommandsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return CommandsResponse{Success: false, Message: "会话已过期"}
	}

	query := `SELECT c.id, c.name, c.command, c.remark, c.type, c.sort_order, c.environment_id, e.name, c.created_at,
			COALESCE(c.mode, 'terminal'), COALESCE(c.connection_id, 0), COALESCE(conn.name, ''), COALESCE(c.interpreter, 'cmd')
		FROM commands c
		INNER JOIN environments e ON c.environment_id = e.id
		LEFT JOIN connections conn ON c.connection_id = conn.id`
	var args []interface{}

	if req.EnvironmentId > 0 {
		query += " WHERE c.environment_id = ?"
		args = append(args, req.EnvironmentId)
	}
	// 排序值越大越靠前，相同排序值时按创建时间倒序
	query += " ORDER BY c.sort_order DESC, c.id DESC"

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("查询命令列表失败", "error", err)
		return CommandsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	s.mu.Lock()
	s.ensureMap()
	// 快照运行中的命令 ID
	runningDaemons := make(map[int]bool, len(s.cmdDaemons))
	for id := range s.cmdDaemons {
		runningDaemons[id] = true
	}
	runningInteractive := make(map[int]bool, len(s.interactiveSessions))
	for id := range s.interactiveSessions {
		runningInteractive[id] = true
	}
	s.mu.Unlock()

	var cmds []Command
	for rows.Next() {
		var cmd Command
		if err := rows.Scan(&cmd.ID, &cmd.Name, &cmd.Command, &cmd.Remark, &cmd.Type, &cmd.SortOrder, &cmd.EnvironmentId, &cmd.EnvName, &cmd.CreatedAt,
			&cmd.Mode, &cmd.ConnectionId, &cmd.ConnectionName, &cmd.Interpreter); err != nil {
			slog.Error("扫描命令记录失败", "error", err)
			continue
		}
		if cmd.Type == "" {
			cmd.Type = "non-interactive"
		}
		if cmd.Mode == "" {
			cmd.Mode = "terminal"
		}
		if cmd.Interpreter == "" {
			cmd.Interpreter = "cmd"
		}
		cmd.Running = runningDaemons[cmd.ID] || runningInteractive[cmd.ID]
		cmds = append(cmds, cmd)
	}
	return CommandsResponse{Success: true, Commands: cmds}
}

// normalizeInterpreter 规范化脚本解释器
// 合法值：cmd（默认）/ powershell / bash；非法值回退 cmd。
// 非本地执行模式（ssh）下脚本由远端环境解析，解释器强制回退 cmd。
func normalizeInterpreter(interpreter, mode string) string {
	if mode != "" && mode != "terminal" {
		return "cmd"
	}
	switch interpreter {
	case "powershell", "bash":
		return interpreter
	default:
		return "cmd"
	}
}

// validateScriptForInterpreter 校验命令内容与脚本类型的匹配
// CMD 解释器不支持整体多行脚本：命令被包装进 cmd /s /c "..."，换行/引号会破坏其引号规则。
// 交互式 / 守护进程类型的 cmd 多行在此拦截（提示改用 PowerShell/Bash）；
// 非交互式（non-interactive）的 cmd 多行不拦截——执行时按行拆分为独立命令逐条执行。
func validateScriptForInterpreter(interpreter, command string) error {
	if interpreter == "cmd" && strings.Contains(command, "\n") {
		return errors.New("CMD 命令不支持整体多行脚本，多行脚本请将脚本类型改为 PowerShell 或 Bash")
	}
	return nil
}

// splitExecLines 将命令文本按行拆分为非空行列表（兼容 \r\n 与 \n）
func splitExecLines(cmdText string) []string {
	var lines []string
	for _, l := range strings.Split(cmdText, "\n") {
		l = strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// CreateCommand 创建命令
func (s *OpsService) CreateCommand(req CreateCommandRequest) CommandResponse {
	if _, ok := validateSession(req.Token); !ok {
		return CommandResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	command := strings.TrimSpace(req.Command)
	if name == "" || command == "" {
		return CommandResponse{Success: false, Message: "命令名称和命令本身不能为空"}
	}

	cmdType := strings.TrimSpace(req.Type)
	if cmdType == "" {
		cmdType = "non-interactive"
	}
	if cmdType != "non-interactive" && cmdType != "interactive" && cmdType != "daemon" {
		return CommandResponse{Success: false, Message: "命令类型无效，仅支持 non-interactive、interactive 或 daemon"}
	}

	// 校验执行模式与目标连接
	mode, err := validateModeAndConnection(req.Mode, req.ConnectionId)
	if err != nil {
		return CommandResponse{Success: false, Message: err.Error()}
	}

	// 脚本解释器：仅本地 terminal 模式生效，其他模式强制 cmd
	interpreter := normalizeInterpreter(req.Interpreter, mode)

	var envName string
	err = db.QueryRow("SELECT name FROM environments WHERE id = ?", req.EnvironmentId).Scan(&envName)
	if err == sql.ErrNoRows {
		return CommandResponse{Success: false, Message: "所属环境不存在"}
	}
	if err != nil {
		return CommandResponse{Success: false, Message: "查询环境失败: " + err.Error()}
	}

	// 排序值：未设置（0）时默认 10
	sortOrder := req.SortOrder
	if sortOrder == 0 {
		sortOrder = 10
	}

	result, err := db.Exec("INSERT INTO commands (name, command, remark, type, sort_order, environment_id, mode, connection_id, interpreter) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		name, command, strings.TrimSpace(req.Remark), cmdType, sortOrder, req.EnvironmentId, mode, req.ConnectionId, interpreter)
	if err != nil {
		slog.Error("创建命令失败", "name", name, "error", err)
		return CommandResponse{Success: false, Message: "创建失败: " + err.Error()}
	}

	id, _ := result.LastInsertId()
	slog.Info("命令创建成功", "id", id, "name", name, "type", cmdType, "mode", mode, "interpreter", interpreter, "connectionId", req.ConnectionId, "sortOrder", sortOrder, "env", envName)

	return CommandResponse{
		Success: true,
		Command: Command{
			ID:            int(id),
			Name:          name,
			Command:       command,
			Remark:        req.Remark,
			Type:          cmdType,
			Interpreter:   interpreter,
			SortOrder:     sortOrder,
			EnvironmentId: req.EnvironmentId,
			EnvName:       envName,
			Mode:          mode,
			ConnectionId:  req.ConnectionId,
		},
		Message: "命令创建成功",
	}
}

// UpdateCommand 修改命令
func (s *OpsService) UpdateCommand(req UpdateCommandRequest) CommandResponse {
	if _, ok := validateSession(req.Token); !ok {
		return CommandResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	command := strings.TrimSpace(req.Command)
	if name == "" || command == "" {
		return CommandResponse{Success: false, Message: "命令名称和命令本身不能为空"}
	}

	cmdType := strings.TrimSpace(req.Type)
	if cmdType == "" {
		cmdType = "non-interactive"
	}
	if cmdType != "non-interactive" && cmdType != "interactive" && cmdType != "daemon" {
		return CommandResponse{Success: false, Message: "命令类型无效，仅支持 non-interactive、interactive 或 daemon"}
	}

	// 校验执行模式与目标连接
	mode, err := validateModeAndConnection(req.Mode, req.ConnectionId)
	if err != nil {
		return CommandResponse{Success: false, Message: err.Error()}
	}

	// 脚本解释器：仅本地 terminal 模式生效，其他模式强制 cmd
	interpreter := normalizeInterpreter(req.Interpreter, mode)

	// 检查命令是否存在
	var oldName, oldType string
	err = db.QueryRow("SELECT name, type FROM commands WHERE id = ?", req.ID).Scan(&oldName, &oldType)
	if err == sql.ErrNoRows {
		return CommandResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return CommandResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 命令正在运行时不允许修改
	s.mu.Lock()
	s.ensureMap()
	running := false
	if _, exists := s.cmdDaemons[req.ID]; exists {
		running = true
	}
	if _, exists := s.interactiveSessions[req.ID]; exists {
		running = true
	}
	if session, exists := s.streamSessions[req.ID]; exists && !session.done {
		running = true
	}
	s.mu.Unlock()
	if running {
		return CommandResponse{Success: false, Message: "命令正在运行中，请先停止再修改"}
	}

	// 检查目标环境是否存在
	var envName string
	err = db.QueryRow("SELECT name FROM environments WHERE id = ?", req.EnvironmentId).Scan(&envName)
	if err == sql.ErrNoRows {
		return CommandResponse{Success: false, Message: "所属环境不存在"}
	}
	if err != nil {
		return CommandResponse{Success: false, Message: "查询环境失败: " + err.Error()}
	}

	// 排序值：未设置（0）时默认 10
	sortOrder := req.SortOrder
	if sortOrder == 0 {
		sortOrder = 10
	}

	_, err = db.Exec("UPDATE commands SET name = ?, command = ?, remark = ?, type = ?, sort_order = ?, environment_id = ?, mode = ?, connection_id = ?, interpreter = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		name, command, strings.TrimSpace(req.Remark), cmdType, sortOrder, req.EnvironmentId, mode, req.ConnectionId, interpreter, req.ID)
	if err != nil {
		slog.Error("修改命令失败", "id", req.ID, "name", name, "error", err)
		return CommandResponse{Success: false, Message: "修改失败: " + err.Error()}
	}

	slog.Info("命令修改成功", "id", req.ID, "oldName", oldName, "newName", name, "oldType", oldType, "newType", cmdType, "mode", mode, "interpreter", interpreter, "connectionId", req.ConnectionId, "sortOrder", sortOrder)

	return CommandResponse{
		Success: true,
		Command: Command{
			ID:            req.ID,
			Name:          name,
			Command:       command,
			Remark:        req.Remark,
			Type:          cmdType,
			Interpreter:   interpreter,
			SortOrder:     sortOrder,
			EnvironmentId: req.EnvironmentId,
			EnvName:       envName,
			Mode:          mode,
			ConnectionId:  req.ConnectionId,
		},
		Message: "命令修改成功",
	}
}

// DeleteCommand 删除单个命令
func (s *OpsService) DeleteCommand(req DeleteCommandRequest) DeleteCommandResponse {
	if _, ok := validateSession(req.Token); !ok {
		return DeleteCommandResponse{Success: false, Message: "会话已过期"}
	}

	var cmdName, cmdType string
	err := db.QueryRow("SELECT name, type FROM commands WHERE id = ?", req.ID).Scan(&cmdName, &cmdType)
	if err == sql.ErrNoRows {
		return DeleteCommandResponse{Success: false, Message: "命令不存在"}
	}
	if err != nil {
		return DeleteCommandResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 停止运行中的守护进程、交互式会话或流式执行
	s.mu.Lock()
	s.ensureMap()
	if rec, exists := s.cmdDaemons[req.ID]; exists {
		delete(s.cmdDaemons, req.ID)
		rec.Terminate()
		slog.Info("删除命令时自动停止守护进程", "id", req.ID, "name", cmdName)
	}
	if session, exists := s.interactiveSessions[req.ID]; exists {
		delete(s.interactiveSessions, req.ID)
		if session.transport != nil {
			session.transport.Kill()
			session.transport.Close()
		}
		slog.Info("删除命令时自动停止交互式会话", "id", req.ID, "name", cmdName)
	}
	if session, exists := s.streamSessions[req.ID]; exists {
		delete(s.streamSessions, req.ID)
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
			rec.Terminate()
		}
		if transport != nil {
			transport.Kill()
			transport.Close()
		}
		slog.Info("删除命令时自动停止流式执行", "id", req.ID, "name", cmdName)
	}
	s.mu.Unlock()

	_, err = db.Exec("DELETE FROM commands WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("删除命令失败", "id", req.ID, "error", err)
		return DeleteCommandResponse{Success: false, Message: "删除失败: " + err.Error()}
	}

	slog.Info("命令删除成功", "id", req.ID, "name", cmdName, "type", cmdType)
	return DeleteCommandResponse{Success: true, Message: "命令「" + cmdName + "」已删除"}
}
