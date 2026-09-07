package server

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"opsflash/server/daemon"
)

// ==================== 命令管理（数据增删改查）====================

// Environment 环境信息
type Environment struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`       // 英文 key，作脚本磁盘子目录名 data/scripts/{key}/
	SortOrder int    `json:"sortOrder"`
	CreatedAt string `json:"createdAt"`
}

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

// 请求类型：环境
type GetEnvironmentsRequest struct {
	Token string `json:"token"`
}

type CreateEnvironmentRequest struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	SortOrder int    `json:"sortOrder"`
}

type UpdateEnvironmentRequest struct {
	Token     string `json:"token"`
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	SortOrder int    `json:"sortOrder"`
}

type DeleteEnvironmentRequest struct {
	Token    string `json:"token"`
	ID       int    `json:"id"`
	TargetId int    `json:"targetId"` // 删除时脚本迁移目标环境（不能等于自身；至少保留一个环境）
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
	Mode          string `json:"mode"`         // "terminal" | "ssh" | ...
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
type EnvironmentsResponse struct {
	Success      bool          `json:"success"`
	Environments []Environment `json:"environments"`
	Message      string        `json:"message"`
}

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

type DeleteEnvResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	DeletedCount int    `json:"deletedCount"`
}

type DeleteCommandResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// OpsService 运维操作服务
type OpsService struct {
	mu                  sync.Mutex
	cmdDaemons          map[int]daemon.Record       // 守护进程命令（按命令 ID 跟踪；本机 Job Object/进程组 / SSH 远程）
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
		s.cmdDaemons = make(map[int]daemon.Record)
	}
	if s.interactiveSessions == nil {
		s.interactiveSessions = make(map[int]*interactiveSession)
	}
	if s.streamSessions == nil {
		s.streamSessions = make(map[int]*streamSession)
	}
}

// validateModeAndConnection 校验执行模式与目标连接的一致性
// terminal 模式：connectionId 必须为 0；其他模式：连接必须存在且类型匹配
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

// ==================== 环境管理 API ====================

// GetEnvironments 获取所有环境列表
func (s *OpsService) GetEnvironments(req GetEnvironmentsRequest) EnvironmentsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return EnvironmentsResponse{Success: false, Message: "会话已过期"}
	}

	rows, err := db.Query("SELECT id, name, env_key, sort_order, created_at FROM environments ORDER BY sort_order ASC")
	if err != nil {
		slog.Error("查询环境列表失败", "error", err)
		return EnvironmentsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var envs []Environment
	for rows.Next() {
		var env Environment
		if err := rows.Scan(&env.ID, &env.Name, &env.Key, &env.SortOrder, &env.CreatedAt); err != nil {
			slog.Error("扫描环境记录失败", "error", err)
			continue
		}
		envs = append(envs, env)
	}
	return EnvironmentsResponse{Success: true, Environments: envs}
}

// CreateEnvironment 创建环境（含英文 key 与排序）
func (s *OpsService) CreateEnvironment(req CreateEnvironmentRequest) EnvironmentsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return EnvironmentsResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return EnvironmentsResponse{Success: false, Message: "环境名称不能为空"}
	}
	key := sanitizeEnvKey(req.Key)
	if key == "" {
		return EnvironmentsResponse{Success: false, Message: "环境 key 不能为空（仅限小写字母、数字、_、-）"}
	}
	// key 唯一性
	var cnt int
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE env_key = ?", key).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境 key 已存在"}
	}
	// 名称唯一性
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE name = ?", name).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境名称已存在"}
	}

	sortOrder := req.SortOrder
	if sortOrder <= 0 {
		var maxOrder int
		db.QueryRow("SELECT COALESCE(MAX(sort_order), 99) FROM environments").Scan(&maxOrder)
		sortOrder = maxOrder + 1
	}

	result, err := db.Exec("INSERT INTO environments (name, env_key, sort_order) VALUES (?, ?, ?)", name, key, sortOrder)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return EnvironmentsResponse{Success: false, Message: "环境名称或 key 已存在"}
		}
		slog.Error("创建环境失败", "name", name, "error", err)
		return EnvironmentsResponse{Success: false, Message: "创建失败: " + err.Error()}
	}

	id, _ := result.LastInsertId()
	slog.Info("环境创建成功", "id", id, "name", name, "key", key)

	return s.GetEnvironments(GetEnvironmentsRequest{Token: req.Token})
}

// UpdateEnvironment 编辑环境（名称 / key / 排序）；key 变更会同步迁移磁盘脚本目录
func (s *OpsService) UpdateEnvironment(req UpdateEnvironmentRequest) EnvironmentsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return EnvironmentsResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return EnvironmentsResponse{Success: false, Message: "环境名称不能为空"}
	}
	key := sanitizeEnvKey(req.Key)
	if key == "" {
		return EnvironmentsResponse{Success: false, Message: "环境 key 不能为空（仅限小写字母、数字、_、-）"}
	}

	// 原记录
	var oldName, oldKey string
	err := db.QueryRow("SELECT name, env_key FROM environments WHERE id = ?", req.ID).Scan(&oldName, &oldKey)
	if err == sql.ErrNoRows {
		return EnvironmentsResponse{Success: false, Message: "环境不存在"}
	}
	if err != nil {
		return EnvironmentsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 名称唯一（排除自身）
	var cnt int
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE name = ? AND id != ?", name, req.ID).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境名称已存在"}
	}
	// key 唯一（排除自身）
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE env_key = ? AND id != ?", key, req.ID).Scan(&cnt)
	if cnt > 0 {
		return EnvironmentsResponse{Success: false, Message: "环境 key 已存在"}
	}

	sortOrder := req.SortOrder
	if sortOrder <= 0 {
		sortOrder = 100
	}

	if _, err := db.Exec("UPDATE environments SET name = ?, env_key = ?, sort_order = ? WHERE id = ?",
		name, key, sortOrder, req.ID); err != nil {
		slog.Error("更新环境失败", "id", req.ID, "error", err)
		return EnvironmentsResponse{Success: false, Message: "更新失败: " + err.Error()}
	}

	// key 变更 → 迁移磁盘脚本目录 data/scripts/{oldKey} → data/scripts/{key}
	if oldKey != "" && oldKey != key {
		moveScriptsDir(oldKey, key)
	}

	slog.Info("环境更新成功", "id", req.ID, "name", name, "key", key)
	return s.GetEnvironments(GetEnvironmentsRequest{Token: req.Token})
}

// sanitizeEnvKey 清洗英文 key：仅保留小写字母、数字、下划线、连字符
func sanitizeEnvKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// moveScriptsDir 将脚本从 data/scripts/{oldKey}/ 迁移到 data/scripts/{newKey}/
// （合并内容并删除旧目录；任一为空或相等则跳过）
func moveScriptsDir(oldKey, newKey string) {
	if oldKey == "" || newKey == "" || oldKey == newKey {
		return
	}
	root := scriptsDir()
	oldDir := filepath.Join(root, oldKey)
	if _, err := os.Stat(oldDir); err != nil {
		return // 旧目录不存在（可能本就无脚本）
	}
	newDir := filepath.Join(root, newKey)
	_ = os.MkdirAll(newDir, 0755)
	if fes, err := os.ReadDir(oldDir); err == nil {
		for _, fe := range fes {
			if fe.IsDir() {
				continue
			}
			_ = os.Rename(filepath.Join(oldDir, fe.Name()), filepath.Join(newDir, fe.Name()))
		}
	}
	_ = os.Remove(oldDir)
	slog.Info("脚本目录已迁移", "from", oldKey, "to", newKey)
}

// DeleteEnvironment 删除环境（脚本迁移到目标环境，运维命令级联删除）
func (s *OpsService) DeleteEnvironment(req DeleteEnvironmentRequest) DeleteEnvResponse {
	if _, ok := validateSession(req.Token); !ok {
		return DeleteEnvResponse{Success: false, Message: "会话已过期"}
	}
	if req.TargetId == req.ID {
		return DeleteEnvResponse{Success: false, Message: "脚本迁移目标不能是自身"}
	}

	var envName, oldKey string
	err := db.QueryRow("SELECT name, env_key FROM environments WHERE id = ?", req.ID).Scan(&envName, &oldKey)
	if err == sql.ErrNoRows {
		return DeleteEnvResponse{Success: false, Message: "环境不存在"}
	}
	if err != nil {
		return DeleteEnvResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 至少保留一个环境
	var otherCount int
	db.QueryRow("SELECT COUNT(*) FROM environments WHERE id != ?", req.ID).Scan(&otherCount)
	if otherCount == 0 {
		return DeleteEnvResponse{Success: false, Message: "至少保留一个环境，无法删除"}
	}

	// 目标环境校验
	var targetName, newKey string
	if req.TargetId > 0 {
		terr := db.QueryRow("SELECT name, env_key FROM environments WHERE id = ?", req.TargetId).Scan(&targetName, &newKey)
		if terr == sql.ErrNoRows {
			return DeleteEnvResponse{Success: false, Message: "目标环境不存在"}
		}
		if terr != nil {
			return DeleteEnvResponse{Success: false, Message: "查询失败: " + terr.Error()}
		}
	}

	// 停止该环境下的命令进程（命令仍级联删除）
	rows, err := db.Query("SELECT id, type FROM commands WHERE environment_id = ?", req.ID)
	if err != nil {
		return DeleteEnvResponse{Success: false, Message: "查询命令失败: " + err.Error()}
	}
	var cmdIDs []int
	for rows.Next() {
		var cid int
		var ctype string
		rows.Scan(&cid, &ctype)
		cmdIDs = append(cmdIDs, cid)
	}
	rows.Close()

	s.mu.Lock()
	s.ensureMap()
	for _, cid := range cmdIDs {
		if rec, exists := s.cmdDaemons[cid]; exists {
			delete(s.cmdDaemons, cid)
			rec.Terminate()
		}
		if session, exists := s.interactiveSessions[cid]; exists {
			delete(s.interactiveSessions, cid)
			if session.transport != nil {
				session.transport.Kill()
				session.transport.Close()
			}
		}
	}
	s.mu.Unlock()

	var cmdCount int
	db.QueryRow("SELECT COUNT(*) FROM commands WHERE environment_id = ?", req.ID).Scan(&cmdCount)

	// 脚本迁移到目标环境（磁盘目录 + DB）
	if req.TargetId > 0 {
		moveScriptsDir(oldKey, newKey)
		db.Exec("UPDATE scripts SET environment_id = ? WHERE environment_id = ?", req.TargetId, req.ID)
	} else {
		// 兜底：挂首个其他环境（前端通常已传目标）
		var fallback int
		var fbKey string
		db.QueryRow("SELECT MIN(id), COALESCE((SELECT env_key FROM environments WHERE id = (SELECT MIN(id) FROM environments WHERE id != ?)), '')", req.ID).Scan(&fallback, &fbKey)
		if fallback > 0 {
			if fbKey == "" {
				fbKey = fmt.Sprintf("env_%d", fallback)
			}
			moveScriptsDir(oldKey, fbKey)
			db.Exec("UPDATE scripts SET environment_id = ? WHERE environment_id = ?", fallback, req.ID)
		}
	}

	if cmdCount > 0 {
		// 级联清理批量任务中引用的命令
		_, _ = db.Exec("DELETE FROM batch_task_items WHERE command_id IN (SELECT id FROM commands WHERE environment_id = ?)", req.ID)
		_, err = db.Exec("DELETE FROM commands WHERE environment_id = ?", req.ID)
		if err != nil {
			slog.Error("删除环境命令失败", "envId", req.ID, "error", err)
			return DeleteEnvResponse{Success: false, Message: "删除命令失败: " + err.Error()}
		}
		slog.Info("已删除环境下的命令", "env", envName, "count", cmdCount)
	}

	_, err = db.Exec("DELETE FROM environments WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("删除环境失败", "id", req.ID, "error", err)
		return DeleteEnvResponse{Success: false, Message: "删除失败: " + err.Error()}
	}

	slog.Info("环境删除成功", "id", req.ID, "name", envName, "targetKey", newKey, "deletedCommands", cmdCount)
	return DeleteEnvResponse{Success: true, Message: "环境「" + envName + "」已删除，脚本迁移至「" + targetName + "」", DeletedCount: cmdCount}
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
// 非本地执行模式（ssh/redis/mysql/tdengine）下脚本由远端环境解析，解释器强制回退 cmd。
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

	// 级联清理批量任务中引用的命令（任务保留，前端提示含已删除命令）
	_, _ = db.Exec("DELETE FROM batch_task_items WHERE command_id = ?", req.ID)

	_, err = db.Exec("DELETE FROM commands WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("删除命令失败", "id", req.ID, "error", err)
		return DeleteCommandResponse{Success: false, Message: "删除失败: " + err.Error()}
	}

	slog.Info("命令删除成功", "id", req.ID, "name", cmdName, "type", cmdType)
	return DeleteCommandResponse{Success: true, Message: "命令「" + cmdName + "」已删除"}
}
