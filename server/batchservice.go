package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"opsflash/server/pty"
)

// ==================== 批量执行模块 ====================
// 批量任务（Batch Task）= 从现有命令中选择若干条按序组合，一次启动顺序执行全部命令。
// 错误策略（failure_policy）：
//   - stop_on_error：某条失败立即中止，后续命令标记 skipped
//   - continue_on_error：失败也继续执行剩余命令，直至全部完成
// V1 仅支持 non-interactive 类型命令（interactive/daemon 在创建/更新时校验拒绝）。
// 执行方式复用 Phase 1 的 executorFor 分派：任务内命令可混合 terminal / ssh。

// ==================== 类型定义 ====================

// BatchTask 批量任务
type BatchTask struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Remark        string `json:"remark"`
	FailurePolicy string `json:"failurePolicy"` // stop_on_error | continue_on_error
	SortOrder     int    `json:"sortOrder"`
	CommandCount  int    `json:"commandCount"`
	CreatedAt     string `json:"createdAt"`
}

// BatchTaskItem 批量任务项（命令或脚本步骤）
type BatchTaskItem struct {
	ID        int    `json:"id"`
	TaskId    int    `json:"taskId"`
	CommandId int    `json:"commandId"` // 命令步骤
	ScriptId  int    `json:"scriptId"`  // 脚本步骤（>0 时优先）
	Args      string `json:"args"`      // 脚本参数（支持 {{var}} 注入）
	Kind      string `json:"kind"`      // command | script（前端展示）
	Name      string `json:"name"`      // JOIN 带出（命令名或脚本名）
	Mode      string `json:"mode"`      // JOIN commands 带出（命令步骤）
	SortOrder int    `json:"sortOrder"`
}

// BatchItemInput 创建/更新任务时的步骤项
type BatchItemInput struct {
	CommandId int    `json:"commandId"`
	ScriptId  int    `json:"scriptId"`
	Args      string `json:"args"`
}

// 请求/响应类型
type GetBatchTasksRequest struct {
	Token string `json:"token"`
}

type GetBatchTaskRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type CreateBatchTaskRequest struct {
	Token         string           `json:"token"`
	Name          string           `json:"name"`
	Remark        string           `json:"remark"`
	FailurePolicy string           `json:"failurePolicy"`
	CommandIds    []int            `json:"commandIds"` // 兼容旧前端：纯命令步骤
	Items         []BatchItemInput `json:"items"`      // 新格式：命令/脚本混合步骤（优先）
}

type UpdateBatchTaskRequest struct {
	Token         string           `json:"token"`
	ID            int              `json:"id"`
	Name          string           `json:"name"`
	Remark        string           `json:"remark"`
	FailurePolicy string           `json:"failurePolicy"`
	CommandIds    []int            `json:"commandIds"`
	Items         []BatchItemInput `json:"items"`
}

type DeleteBatchTaskRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type StartBatchTaskRequest struct {
	Token      string `json:"token"`
	ID         int    `json:"id"`
	ParamsJson string `json:"paramsJson"` // 流程参数 {"version":"1.2.3"}，注入脚本步骤 args 的 {{key}}
}

type GetBatchTaskProgressRequest struct {
	Token string `json:"token"`
	RunId int    `json:"runId"`
}

type StopBatchTaskRequest struct {
	Token string `json:"token"`
	RunId int    `json:"runId"`
}

type BatchTasksResponse struct {
	Success bool        `json:"success"`
	Tasks   []BatchTask `json:"tasks"`
	Message string      `json:"message"`
}

type BatchTaskResponse struct {
	Success bool            `json:"success"`
	Task    *BatchTask      `json:"task"`
	Items   []BatchTaskItem `json:"items"`
	Message string          `json:"message"`
}

type DeleteBatchTaskResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type StartBatchTaskResponse struct {
	Success bool   `json:"success"`
	RunId   int    `json:"runId"`
	Message string `json:"message"`
}

type StopBatchTaskResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// BatchRunItemProgress 批量执行单条命令/脚本的实时进度
type BatchRunItemProgress struct {
	CommandId  int    `json:"commandId"`
	ScriptId   int    `json:"scriptId"`
	Kind       string `json:"kind"` // command | script
	Name       string `json:"name"`
	Status     string `json:"status"` // pending | running | success | failed | skipped
	Output     string `json:"output"`
	Error      string `json:"error"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

type BatchTaskProgressResponse struct {
	Success    bool                  `json:"success"`
	Done       bool                  `json:"done"`
	Stopped    bool                  `json:"stopped"`
	Items      []BatchRunItemProgress `json:"items"`
	StartedAt  string                `json:"startedAt"`
	FinishedAt string                `json:"finishedAt"`
	Message    string                `json:"message"`
}

// ==================== 执行引擎（内存会话） ====================

// batchRun 一次批量执行会话
type batchRun struct {
	runId      int
	taskId     int
	taskName   string
	policy     string
	params     map[string]string // 流程参数（脚本步骤 args 的 {{key}} 注入）
	items      []*batchRunItem
	mu         sync.Mutex
	done       bool
	stopped    bool
	startedAt  time.Time
	finishedAt time.Time
}

// batchRunItem 单条步骤的执行状态
type batchRunItem struct {
	commandId  int
	scriptId   int
	kind       string // command | script
	name       string
	args       string // 脚本参数（含 {{var}} 占位符）
	status     string // pending | running | success | failed | skipped
	output     string
	errMsg     string
	exitCode   int
	durationMs int64
	cancel     context.CancelFunc // 用于停止时中断当前 running 项
}

// BatchService 批量执行服务
type BatchService struct {
	mu        sync.Mutex
	batchRuns map[int]*batchRun // runId -> 执行会话（保留最近 maxBatchRuns 次）
	runSeq    int64             // runId 原子自增
}

const maxBatchRuns = 20

func (s *BatchService) ensureMap() {
	if s.batchRuns == nil {
		s.batchRuns = make(map[int]*batchRun)
	}
}

// nextRunId 分配自增 runId 并清理超出上限的旧会话
func (s *BatchService) nextRunId() int {
	id := int(atomic.AddInt64(&s.runSeq, 1))
	s.ensureMap()
	if len(s.batchRuns) >= maxBatchRuns {
		oldest := -1
		for rid := range s.batchRuns {
			if oldest < 0 || rid < oldest {
				oldest = rid
			}
		}
		if oldest >= 0 {
			delete(s.batchRuns, oldest)
		}
	}
	return id
}

// ==================== 任务管理 API ====================

// GetBatchTasks 获取任务列表（含命令数量）
func (s *BatchService) GetBatchTasks(req GetBatchTasksRequest) BatchTasksResponse {
	if _, ok := validateSession(req.Token); !ok {
		return BatchTasksResponse{Success: false, Message: "会话已过期"}
	}
	rows, err := db.Query(`SELECT t.id, t.name, t.remark, t.failure_policy, t.sort_order,
		(SELECT COUNT(*) FROM batch_task_items i WHERE i.task_id = t.id), t.created_at
		FROM batch_tasks t ORDER BY t.sort_order DESC, t.id DESC`)
	if err != nil {
		slog.Error("查询批量任务列表失败", "error", err)
		return BatchTasksResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var tasks []BatchTask
	for rows.Next() {
		var t BatchTask
		if err := rows.Scan(&t.ID, &t.Name, &t.Remark, &t.FailurePolicy, &t.SortOrder, &t.CommandCount, &t.CreatedAt); err != nil {
			slog.Error("扫描批量任务记录失败", "error", err)
			continue
		}
		if t.FailurePolicy == "" {
			t.FailurePolicy = "stop_on_error"
		}
		tasks = append(tasks, t)
	}
	return BatchTasksResponse{Success: true, Tasks: tasks}
}

// GetBatchTask 获取任务详情（含有序命令项）
func (s *BatchService) GetBatchTask(req GetBatchTaskRequest) BatchTaskResponse {
	if _, ok := validateSession(req.Token); !ok {
		return BatchTaskResponse{Success: false, Message: "会话已过期"}
	}
	task, items, err := loadBatchTask(req.ID)
	if err != nil {
		return BatchTaskResponse{Success: false, Message: err.Error()}
	}
	return BatchTaskResponse{Success: true, Task: task, Items: items}
}

// loadBatchTask 加载任务与有序命令项
func loadBatchTask(id int) (*BatchTask, []BatchTaskItem, error) {
	var t BatchTask
	err := db.QueryRow(`SELECT t.id, t.name, t.remark, t.failure_policy, t.sort_order,
		(SELECT COUNT(*) FROM batch_task_items i WHERE i.task_id = t.id), t.created_at
		FROM batch_tasks t WHERE t.id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Remark, &t.FailurePolicy, &t.SortOrder, &t.CommandCount, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil, errors.New("任务不存在")
	}
	if err != nil {
		return nil, nil, err
	}
	if t.FailurePolicy == "" {
		t.FailurePolicy = "stop_on_error"
	}

	rows, err := db.Query(`SELECT i.id, i.task_id, i.command_id, i.script_id, COALESCE(i.args, ''), i.sort_order,
		CASE WHEN i.script_id > 0 THEN
			(SELECT name FROM scripts sc WHERE sc.id = i.script_id)
		ELSE
			(SELECT name FROM commands c WHERE c.id = i.command_id)
		END AS item_name,
		CASE WHEN i.script_id > 0 THEN 'script' ELSE
			COALESCE((SELECT mode FROM commands c WHERE c.id = i.command_id), 'terminal')
		END AS item_mode
		FROM batch_task_items i
		WHERE i.task_id = ? ORDER BY i.sort_order ASC, i.id ASC`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var items []BatchTaskItem
	for rows.Next() {
		var it BatchTaskItem
		if err := rows.Scan(&it.ID, &it.TaskId, &it.CommandId, &it.ScriptId, &it.Args,
			&it.SortOrder, &it.Name, &it.Mode); err != nil {
			continue
		}
		if it.ScriptId > 0 {
			it.Kind = "script"
		} else {
			it.Kind = "command"
		}
		items = append(items, it)
	}
	return &t, items, nil
}

// validateFailurePolicy 校验错误策略
func validateFailurePolicy(policy string) (string, error) {
	policy = strings.TrimSpace(policy)
	if policy == "" {
		return "stop_on_error", nil
	}
	if policy != "stop_on_error" && policy != "continue_on_error" {
		return "", errors.New("错误策略无效，仅支持 stop_on_error（遇错停止）或 continue_on_error（忽略错误）")
	}
	return policy, nil
}

// validateBatchItems 校验步骤列表（命令/脚本混合），返回规范化步骤
// 优先使用 Items（新格式）；为空时回退 CommandIds（旧前端纯命令步骤）
func validateBatchItems(items []BatchItemInput, legacyIds []int) ([]BatchItemInput, error) {
	var steps []BatchItemInput
	if len(items) > 0 {
		steps = items
	} else {
		for _, cid := range legacyIds {
			steps = append(steps, BatchItemInput{CommandId: cid})
		}
	}
	if len(steps) == 0 {
		return nil, errors.New("请至少添加一个步骤（命令或脚本）")
	}

	seen := make(map[string]bool)
	valid := make([]BatchItemInput, 0, len(steps))
	for _, st := range steps {
		if st.CommandId > 0 {
			// 命令步骤：存在 + 非交互式
			var cmdName, cmdType string
			err := db.QueryRow("SELECT name, type FROM commands WHERE id = ?", st.CommandId).Scan(&cmdName, &cmdType)
			if err == sql.ErrNoRows {
				return nil, errors.New("命令 #" + strconv.Itoa(st.CommandId) + " 不存在")
			}
			if err != nil {
				return nil, err
			}
			if cmdType != "non-interactive" {
				return nil, errors.New("命令「" + cmdName + "」不是非交互式类型，批量执行仅支持非交互式命令")
			}
			key := "c" + strconv.Itoa(st.CommandId)
			if seen[key] {
				continue
			}
			seen[key] = true
			valid = append(valid, BatchItemInput{CommandId: st.CommandId})
		} else if st.ScriptId > 0 {
			// 脚本步骤：存在
			var scName string
			err := db.QueryRow("SELECT name FROM scripts WHERE id = ?", st.ScriptId).Scan(&scName)
			if err == sql.ErrNoRows {
				return nil, errors.New("脚本 #" + strconv.Itoa(st.ScriptId) + " 不存在")
			}
			if err != nil {
				return nil, err
			}
			key := "s" + strconv.Itoa(st.ScriptId)
			if seen[key] {
				continue
			}
			seen[key] = true
			valid = append(valid, BatchItemInput{ScriptId: st.ScriptId, Args: st.Args})
		} else {
			return nil, errors.New("步骤无效：命令或脚本 ID 必须至少一个")
		}
	}
	if len(valid) == 0 {
		return nil, errors.New("请至少添加一个有效步骤")
	}
	return valid, nil
}

// CreateBatchTask 创建批量任务
func (s *BatchService) CreateBatchTask(req CreateBatchTaskRequest) BatchTaskResponse {
	if _, ok := validateSession(req.Token); !ok {
		return BatchTaskResponse{Success: false, Message: "会话已过期"}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return BatchTaskResponse{Success: false, Message: "任务名称不能为空"}
	}
	policy, err := validateFailurePolicy(req.FailurePolicy)
	if err != nil {
		return BatchTaskResponse{Success: false, Message: err.Error()}
	}
	steps, err := validateBatchItems(req.Items, req.CommandIds)
	if err != nil {
		return BatchTaskResponse{Success: false, Message: err.Error()}
	}

	result, err := db.Exec("INSERT INTO batch_tasks (name, remark, failure_policy) VALUES (?, ?, ?)",
		name, strings.TrimSpace(req.Remark), policy)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return BatchTaskResponse{Success: false, Message: "任务名称已存在"}
		}
		slog.Error("创建批量任务失败", "name", name, "error", err)
		return BatchTaskResponse{Success: false, Message: "创建失败: " + err.Error()}
	}
	taskID, _ := result.LastInsertId()
	if err := insertBatchItems(int(taskID), steps); err != nil {
		slog.Error("创建批量任务项失败", "taskId", taskID, "error", err)
		return BatchTaskResponse{Success: false, Message: "创建失败: " + err.Error()}
	}

	slog.Info("批量任务创建成功", "id", taskID, "name", name, "policy", policy, "steps", len(steps))
	task, items, _ := loadBatchTask(int(taskID))
	return BatchTaskResponse{Success: true, Task: task, Items: items, Message: "任务创建成功"}
}

// UpdateBatchTask 更新批量任务（重建命令项；有活跃执行时拒绝）
func (s *BatchService) UpdateBatchTask(req UpdateBatchTaskRequest) BatchTaskResponse {
	if _, ok := validateSession(req.Token); !ok {
		return BatchTaskResponse{Success: false, Message: "会话已过期"}
	}

	// 检查是否有活跃执行
	s.mu.Lock()
	s.ensureMap()
	for _, run := range s.batchRuns {
		if run.taskId == req.ID {
			run.mu.Lock()
			active := !run.done && !run.stopped
			run.mu.Unlock()
			if active {
				s.mu.Unlock()
				return BatchTaskResponse{Success: false, Message: "任务正在执行中，请先停止再修改"}
			}
		}
	}
	s.mu.Unlock()

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return BatchTaskResponse{Success: false, Message: "任务名称不能为空"}
	}
	policy, err := validateFailurePolicy(req.FailurePolicy)
	if err != nil {
		return BatchTaskResponse{Success: false, Message: err.Error()}
	}
	steps, err := validateBatchItems(req.Items, req.CommandIds)
	if err != nil {
		return BatchTaskResponse{Success: false, Message: err.Error()}
	}

	var exists int
	err = db.QueryRow("SELECT COUNT(*) FROM batch_tasks WHERE id = ?", req.ID).Scan(&exists)
	if err != nil || exists == 0 {
		return BatchTaskResponse{Success: false, Message: "任务不存在"}
	}

	_, err = db.Exec("UPDATE batch_tasks SET name = ?, remark = ?, failure_policy = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		name, strings.TrimSpace(req.Remark), policy, req.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return BatchTaskResponse{Success: false, Message: "任务名称已存在"}
		}
		slog.Error("更新批量任务失败", "id", req.ID, "error", err)
		return BatchTaskResponse{Success: false, Message: "更新失败: " + err.Error()}
	}
	// 重建步骤项
	if _, err := db.Exec("DELETE FROM batch_task_items WHERE task_id = ?", req.ID); err != nil {
		slog.Error("清理批量任务项失败", "taskId", req.ID, "error", err)
		return BatchTaskResponse{Success: false, Message: "更新失败: " + err.Error()}
	}
	if err := insertBatchItems(req.ID, steps); err != nil {
		slog.Error("重建批量任务项失败", "taskId", req.ID, "error", err)
		return BatchTaskResponse{Success: false, Message: "更新失败: " + err.Error()}
	}

	slog.Info("批量任务更新成功", "id", req.ID, "name", name, "policy", policy, "steps", len(steps))
	task, items, _ := loadBatchTask(req.ID)
	return BatchTaskResponse{Success: true, Task: task, Items: items, Message: "任务更新成功"}
}

// insertBatchItems 按顺序写入任务步骤项（sort_order 递增）
func insertBatchItems(taskID int, steps []BatchItemInput) error {
	for idx, st := range steps {
		if _, err := db.Exec("INSERT INTO batch_task_items (task_id, command_id, script_id, args, sort_order) VALUES (?, ?, ?, ?, ?)",
			taskID, st.CommandId, st.ScriptId, st.Args, (idx+1)*10); err != nil {
			return err
		}
	}
	return nil
}

// DeleteBatchTask 删除任务（若有活跃执行先停止）
func (s *BatchService) DeleteBatchTask(req DeleteBatchTaskRequest) DeleteBatchTaskResponse {
	if _, ok := validateSession(req.Token); !ok {
		return DeleteBatchTaskResponse{Success: false, Message: "会话已过期"}
	}
	var taskName string
	err := db.QueryRow("SELECT name FROM batch_tasks WHERE id = ?", req.ID).Scan(&taskName)
	if err == sql.ErrNoRows {
		return DeleteBatchTaskResponse{Success: false, Message: "任务不存在"}
	}
	if err != nil {
		return DeleteBatchTaskResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 停止该任务的活跃执行
	s.mu.Lock()
	s.ensureMap()
	for rid, run := range s.batchRuns {
		if run.taskId == req.ID {
			run.mu.Lock()
			if !run.done {
				run.stopped = true
				for _, item := range run.items {
					if item.status == "running" && item.cancel != nil {
						item.cancel()
					}
				}
			}
			run.mu.Unlock()
			delete(s.batchRuns, rid)
		}
	}
	s.mu.Unlock()

	if _, err := db.Exec("DELETE FROM batch_task_items WHERE task_id = ?", req.ID); err != nil {
		slog.Error("删除任务项失败", "taskId", req.ID, "error", err)
		return DeleteBatchTaskResponse{Success: false, Message: "删除失败: " + err.Error()}
	}
	if _, err := db.Exec("DELETE FROM batch_tasks WHERE id = ?", req.ID); err != nil {
		slog.Error("删除批量任务失败", "id", req.ID, "error", err)
		return DeleteBatchTaskResponse{Success: false, Message: "删除失败: " + err.Error()}
	}

	slog.Info("批量任务删除成功", "id", req.ID, "name", taskName)
	return DeleteBatchTaskResponse{Success: true, Message: "任务「" + taskName + "」已删除"}
}

// ==================== 执行控制 API ====================

// StartBatchTask 异步启动任务执行，返回 runId
func (s *BatchService) StartBatchTask(req StartBatchTaskRequest) StartBatchTaskResponse {
	if _, ok := validateSession(req.Token); !ok {
		return StartBatchTaskResponse{Success: false, Message: "会话已过期"}
	}

	task, items, err := loadBatchTask(req.ID)
	if err != nil {
		return StartBatchTaskResponse{Success: false, Message: err.Error()}
	}
	if len(items) == 0 {
		return StartBatchTaskResponse{Success: false, Message: "任务没有可执行的步骤，请先编辑补充"}
	}

	// 同任务并发执行限制
	s.mu.Lock()
	s.ensureMap()
	for _, run := range s.batchRuns {
		if run.taskId == req.ID {
			run.mu.Lock()
			active := !run.done && !run.stopped
			run.mu.Unlock()
			if active {
				s.mu.Unlock()
				return StartBatchTaskResponse{Success: false, Message: "任务已在执行中"}
			}
		}
	}
	runID := s.nextRunId()
	s.mu.Unlock()

	run := &batchRun{
		runId:     runID,
		taskId:    req.ID,
		taskName:  task.Name,
		policy:    task.FailurePolicy,
		params:    parseParamsJson(req.ParamsJson),
		startedAt: time.Now(),
	}
	for _, it := range items {
		kind := "command"
		if it.ScriptId > 0 {
			kind = "script"
		}
		run.items = append(run.items, &batchRunItem{
			commandId: it.CommandId,
			scriptId:  it.ScriptId,
			kind:      kind,
			name:      it.Name,
			args:      it.Args,
			status:    "pending",
		})
	}

	s.mu.Lock()
	s.batchRuns[runID] = run
	s.mu.Unlock()

	slog.Info("批量任务开始执行", "runId", runID, "taskId", req.ID, "name", task.Name, "policy", task.FailurePolicy, "steps", len(run.items))
	go s.runBatch(run)

	return StartBatchTaskResponse{Success: true, RunId: runID, Message: "任务已开始执行"}
}

// runBatch 顺序执行任务全部命令（goroutine 内运行）
func (s *BatchService) runBatch(run *batchRun) {
	defer func() {
		run.mu.Lock()
		run.done = true
		run.finishedAt = time.Now()
		run.mu.Unlock()
		slog.Info("批量任务执行结束", "runId", run.runId, "taskId", run.taskId, "name", run.taskName)
	}()

	for i, item := range run.items {
		run.mu.Lock()
		if run.stopped {
			run.mu.Unlock()
			markSkipped(run, i)
			break
		}
		item.status = "running"
		run.mu.Unlock()

		s.executeBatchItem(run, item)

		run.mu.Lock()
		failed := item.status == "failed"
		stopped := run.stopped
		run.mu.Unlock()
		if failed && !stopped && run.policy == "stop_on_error" {
			markSkipped(run, i+1)
			break
		}
	}
}

// markSkipped 将 index 起的剩余项标记为 skipped
func markSkipped(run *batchRun, from int) {
	run.mu.Lock()
	defer run.mu.Unlock()
	for j := from; j < len(run.items); j++ {
		if run.items[j].status == "pending" {
			run.items[j].status = "skipped"
		}
	}
}

// executeBatchItem 执行单条步骤（脚本 / 命令，按 kind 分派）
func (s *BatchService) executeBatchItem(run *batchRun, item *batchRunItem) {
	start := time.Now()

	// ==================== 脚本步骤 ====================
	if item.kind == "script" {
		s.executeScriptItem(run, item, start)
		return
	}

	// ==================== 命令步骤（现有逻辑） ====================
	var cmdName, cmdText, cmdType, mode, interpreter string
	var connID int
	err := db.QueryRow(`SELECT name, command, type, mode, COALESCE(connection_id, 0), COALESCE(interpreter, 'cmd') FROM commands WHERE id = ?`, item.commandId).
		Scan(&cmdName, &cmdText, &cmdType, &mode, &connID, &interpreter)
	if err != nil {
		finishBatchItem(item, start, "", err)
		return
	}
	if cmdType != "non-interactive" {
		finishBatchItem(item, start, "", errors.New("批量执行仅支持非交互式命令"))
		return
	}

	conn, err := loadConnectionByID(connID)
	if err != nil {
		finishBatchItem(item, start, "", err)
		return
	}
	ex, err := executorFor(mode, conn, interpreter)
	if err != nil {
		finishBatchItem(item, start, "", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	run.mu.Lock()
	item.cancel = cancel
	run.mu.Unlock()

	var output string
	var runErr error
	// 本地多行命令：与单条运行一致的会话式执行（PTY shell 逐行喂入，共享变量 + 每行回显）。
	// 会话无每行退出码（靠输出判断），读取中断/超时视为失败；超时通过 kill 通道终止会话。
	if mode == "terminal" && strings.Contains(cmdText, "\n") {
		lines := splitExecLines(cmdText)
		if len(lines) > 1 {
			kill := make(chan struct{})
			type batchSessionResult struct {
				out string
				err error
			}
			resCh := make(chan batchSessionResult, 1)
			go func() {
				out, err := runSessionBatchSync(interpreter, lines, kill)
				resCh <- batchSessionResult{out, err}
			}()
			select {
			case res := <-resCh:
				output, runErr = res.out, res.err
			case <-ctx.Done():
				close(kill) // 终止会话（阻塞中的 Read 被 Kill 唤醒）
				output, runErr = "", errors.New("执行超时（60 秒）")
			}
		} else {
			output, runErr = ex.Run(ctx, lines[0])
		}
	} else if mode == "ssh" {
		// SSH 批量流式：远程请求 PTY（xterm）整段执行，实时输出累积到 item.output
		// （前端 500ms 轮询 GetBatchTaskProgress 可见增长）——替代 CombinedOutput 的"运行完才显示"。
		transport, err := ex.StartInteractive(cmdText)
		if err != nil {
			finishBatchItem(item, start, "", err)
			return
		}
		// ctx2：停止（StopBatchTask 调 item.cancel）时终止远程命令；ctx：60s 超时兜底
		ctx2, cancel2 := context.WithCancel(context.Background())
		run.mu.Lock()
		item.cancel = cancel2
		run.mu.Unlock()
		defer func() {
			run.mu.Lock()
			item.cancel = nil
			run.mu.Unlock()
			cancel2()
			cancel() // 释放 60s 超时 timer（ssh 分支提前 return，跳过外层 cancel）
		}()
		go func() {
			<-ctx2.Done()
			transport.Kill()
			transport.Close()
		}()

		// 读取 goroutine：跨帧剥启动噪声/OSC，实时累加 item.output（上限防膨胀）
		decoder := &outputDecoder{}
		buf := make([]byte, 8192)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			stripper := &noiseStripper{}
			osc := &oscStripper{}
			for {
				n, rerr := transport.Read(buf)
				if n > 0 {
					data := osc.Process(sanitizeStartupNoise(stripper.Process(decoder.Decode(buf[:n]))))
					if data != "" {
						run.mu.Lock()
						if len(item.output) < 20000 {
							item.output += data
						}
						run.mu.Unlock()
					}
				}
				if rerr != nil {
					return
				}
			}
		}()

		// 等待远程命令结束（60s 超时兜底）
		waitCh := make(chan error, 1)
		go func() { waitCh <- transport.Wait() }()
		select {
		case werr := <-waitCh:
			runErr = werr
		case <-ctx.Done():
			transport.Kill()
			transport.Close()
			<-waitCh
			runErr = errors.New("执行超时（60 秒）")
		}
		// 排空尾部输出（远程 PTY 管道不保证 EOF）
		select {
		case <-readDone:
		case <-time.After(300 * time.Millisecond):
			transport.Close()
			<-readDone
		}
		transport.Close()

		// 状态落定（output 已实时累积，不覆盖）
		run.mu.Lock()
		item.durationMs = time.Since(start).Milliseconds()
		if runErr != nil {
			item.errMsg = runErr.Error()
			item.status = "failed"
		} else {
			item.status = "success"
		}
		run.mu.Unlock()
		return
	} else {
		output, runErr = ex.Run(ctx, cmdText)
	}

	run.mu.Lock()
	item.cancel = nil
	run.mu.Unlock()
	cancel()

	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			runErr = errors.New("执行超时（60 秒）")
		}
		finishBatchItem(item, start, output, runErr)
		return
	}
	finishBatchItem(item, start, output, nil)
}

// finishBatchItem 记录单条命令执行结果
func finishBatchItem(item *batchRunItem, start time.Time, output string, runErr error) {
	item.durationMs = time.Since(start).Milliseconds()
	output = strings.TrimSpace(output)
	if len(output) > 5000 {
		output = output[:5000] + "\n... (输出已截断)"
	}
	item.output = output
	if runErr != nil {
		item.errMsg = runErr.Error()
		item.status = "failed"
	} else {
		item.status = "success"
	}
}

// executeScriptItem 执行脚本步骤（流程参数注入 + ConPTY 流式 + 退出码判定）
func (s *BatchService) executeScriptItem(run *batchRun, item *batchRunItem, start time.Time) {
	// 加载脚本
	sc, err := loadScriptByID(item.scriptId)
	if err != nil {
		finishBatchItem(item, start, "", errors.New("脚本不存在（可能已被删除）"))
		return
	}
	// 参数注入：{{var}} → 流程参数
	args := item.args
	for k, v := range run.params {
		args = strings.ReplaceAll(args, "{{"+k+"}}", v)
	}

	// 准备执行（sh CRLF 转副本）
	execPath, cleanup, err := prepareScriptForExec(sc)
	if err != nil {
		finishBatchItem(item, start, "", err)
		return
	}
	if cleanup != "" {
		defer os.Remove(cleanup)
	}

	wrapped := scriptWrapCommand(execPath, sc.Type, args)
	interpreter := scriptExtToInterpreter[sc.Type]

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	run.mu.Lock()
	item.cancel = cancel
	run.mu.Unlock()

	transport, err := pty.StartPTY(wrapped, interpreter)
	if err != nil {
		run.mu.Lock()
		item.cancel = nil
		run.mu.Unlock()
		cancel()
		finishBatchItem(item, start, "", errors.New("脚本启动失败: "+err.Error()))
		return
	}

	// 读取 goroutine：实时累积 item.output（与 SSH 批量分支一致，上限 20000）
	decoder := &outputDecoder{}
	buf := make([]byte, 8192)
	readDone := make(chan struct{})
	stripper := &noiseStripper{}
	osc := &oscStripper{}
	go func() {
		defer close(readDone)
		for {
			n, rerr := transport.Read(buf)
			if n > 0 {
				data := osc.Process(sanitizeStartupNoise(stripper.Process(decoder.Decode(buf[:n]))))
				if data != "" {
					run.mu.Lock()
					if len(item.output) < 20000 {
						item.output += data
					}
					run.mu.Unlock()
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// 停止（StopBatchTask 调 item.cancel）时终止脚本进程
	go func() {
		<-ctx.Done()
		run.mu.Lock()
		stopped := run.stopped
		run.mu.Unlock()
		if stopped {
			_ = transport.Kill()
			_ = transport.Close()
		}
	}()

	// 等待结束（60s 超时兜底 + 静默排空尾部输出）
	waitCh := make(chan error, 1)
	go func() { waitCh <- transport.Wait() }()
	var runErr error
	select {
	case werr := <-waitCh:
		runErr = werr
	case <-ctx.Done():
		if run.stopped {
			runErr = errors.New("执行已停止")
		} else {
			transport.Kill()
			transport.Close()
			<-waitCh
			runErr = errors.New("执行超时（60 秒）")
		}
	}
	select {
	case <-readDone:
	case <-time.After(300 * time.Millisecond):
		transport.Close()
		<-readDone
	}
	transport.Close()

	run.mu.Lock()
	item.cancel = nil
	item.durationMs = time.Since(start).Milliseconds()
	if runErr != nil {
		item.errMsg = runErr.Error()
		item.exitCode = exitCodeFromErr(runErr)
		item.status = "failed"
	} else {
		item.status = "success"
	}
	run.mu.Unlock()
	cancel()

	slog.Info("批量脚本步骤执行结束", "runId", run.runId, "name", item.name,
		"exitCode", item.exitCode, "durationMs", item.durationMs, "error", item.errMsg)
}

// GetBatchTaskProgress 获取任务执行进度（前端轮询调用）
func (s *BatchService) GetBatchTaskProgress(req GetBatchTaskProgressRequest) BatchTaskProgressResponse {
	if _, ok := validateSession(req.Token); !ok {
		return BatchTaskProgressResponse{Success: false, Message: "会话已过期"}
	}
	s.mu.Lock()
	run, exists := s.batchRuns[req.RunId]
	s.mu.Unlock()
	if !exists {
		return BatchTaskProgressResponse{Success: false, Message: "执行会话不存在或已清理"}
	}

	run.mu.Lock()
	defer run.mu.Unlock()

	items := make([]BatchRunItemProgress, 0, len(run.items))
	for _, it := range run.items {
		items = append(items, BatchRunItemProgress{
			CommandId:  it.commandId,
			ScriptId:   it.scriptId,
			Kind:       it.kind,
			Name:       it.name,
			Status:     it.status,
			Output:     it.output,
			Error:      it.errMsg,
			ExitCode:   it.exitCode,
			DurationMs: it.durationMs,
		})
	}
	startedAt, finishedAt := "", ""
	if !run.startedAt.IsZero() {
		startedAt = run.startedAt.Format("2006-01-02 15:04:05")
	}
	if !run.finishedAt.IsZero() {
		finishedAt = run.finishedAt.Format("2006-01-02 15:04:05")
	}
	return BatchTaskProgressResponse{
		Success:    true,
		Done:       run.done,
		Stopped:    run.stopped,
		Items:      items,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
}

// StopBatchTask 停止执行（中断当前命令，剩余标记 skipped）
func (s *BatchService) StopBatchTask(req StopBatchTaskRequest) StopBatchTaskResponse {
	if _, ok := validateSession(req.Token); !ok {
		return StopBatchTaskResponse{Success: false, Message: "会话已过期"}
	}
	s.mu.Lock()
	run, exists := s.batchRuns[req.RunId]
	s.mu.Unlock()
	if !exists {
		return StopBatchTaskResponse{Success: false, Message: "执行会话不存在或已清理"}
	}

	run.mu.Lock()
	if run.done {
		run.mu.Unlock()
		return StopBatchTaskResponse{Success: false, Message: "任务已执行结束"}
	}
	run.stopped = true
	for _, item := range run.items {
		if item.status == "running" && item.cancel != nil {
			item.cancel()
		}
	}
	run.mu.Unlock()

	slog.Info("批量任务已停止", "runId", req.RunId, "taskId", run.taskId, "name", run.taskName)
	return StopBatchTaskResponse{Success: true, Message: "已停止执行，剩余命令将跳过"}
}

// parseParamsJson 解析流程参数 JSON（如 {"version":"1.2.3"}），解析失败返回空 map
func parseParamsJson(raw string) map[string]string {
	result := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return result
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		slog.Warn("解析流程参数失败", "error", err)
		return result
	}
	for k, v := range m {
		switch val := v.(type) {
		case string:
			result[k] = val
		default:
			// 非字符串值转为 JSON 文本（数字/布尔等）
			if b, err := json.Marshal(val); err == nil {
				result[k] = string(b)
			}
		}
	}
	return result
}
