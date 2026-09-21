package server

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// DashboardService 首页仪表盘服务
type DashboardService struct{}

// DashboardStats 首页统计数据
type DashboardStats struct {
	TodaySuccess       int               `json:"today_success"`
	TodayFailed        int               `json:"today_failed"`
	SuccessRate        float64           `json:"success_rate"`
	RunningTunnels     int               `json:"running_tunnels"`
	TopExecuted        []TopExecutedItem `json:"top_executed"`
	RecentExecutions   []ExecutionLog    `json:"recent_executions"`
	RecentFailed       []ExecutionLog    `json:"recent_failed"`
}

// TopExecutedItem 最常执行项
type TopExecutedItem struct {
	OperationType string `json:"operation_type"`
	TargetName    string `json:"target_name"`
	ExecuteCount  int    `json:"execute_count"`
}

// ExecutionLog 执行记录
type ExecutionLog struct {
	ID              int64   `json:"id"`
	OperationType   string  `json:"operation_type"`
	TargetID        int64   `json:"target_id"`
	TargetName      string  `json:"target_name"`
	Action          string  `json:"action"`
	EnvironmentID   int64   `json:"environment_id"`
	EnvironmentName string  `json:"environment_name"`
	Mode            string  `json:"mode"`
	ConnectionID    int64   `json:"connection_id"`
	ConnectionName  string  `json:"connection_name"`
	Status          string  `json:"status"`
	ExitCode        int     `json:"exit_code"`
	Output          string  `json:"output"`
	ErrorMessage    string  `json:"error_message"`
	StartedAt       string  `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
	DurationMs      int64   `json:"duration_ms"`
	Username        string  `json:"username"`
	CreatedAt       string  `json:"created_at"`
}

// EnvironmentStat 环境统计
type EnvironmentStat struct {
	EnvironmentID   int64  `json:"environment_id"`
	EnvironmentName string `json:"environment_name"`
	CommandCount    int    `json:"command_count"`
	ScriptCount     int    `json:"script_count"`
}

// GetExecutionLogsRequest 获取执行记录请求
type GetExecutionLogsRequest struct {
	Token         string `json:"token"`
	Page          int    `json:"page"`
	PageSize      int    `json:"pageSize"`
	OperationType string `json:"operationType"`
	Status        string `json:"status"`
	EnvironmentID int64  `json:"environmentId"`
	Search        string `json:"search"`
}

// ExecutionLogsResponse 获取执行记录响应
type ExecutionLogsResponse struct {
	Success bool           `json:"success"`
	Total   int            `json:"total"`
	Items   []ExecutionLog `json:"items"`
}

// ClearExecutionLogsRequest 清空执行记录请求
type ClearExecutionLogsRequest struct {
	Token         string `json:"token"`
	OperationType string `json:"operationType"`
	Status        string `json:"status"`
	Search        string `json:"search"`
}

// ClearExecutionLogsResponse 清空执行记录响应
type ClearExecutionLogsResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Count   int64  `json:"count"`
}

// GetDashboardStats 获取首页统计数据
func (s *DashboardService) GetDashboardStats(token string) (*DashboardStats, error) {
	username, ok := validateSession(token)
	if !ok {
		return nil, fmt.Errorf("未授权访问")
	}
	_ = username

	stats := &DashboardStats{}

	today := time.Now().Format("2006-01-02")

	// 获取今日成功次数
	err := db.QueryRow(`
		SELECT COUNT(*) FROM execution_logs 
		WHERE DATE(started_at) = ? AND status = 'success'
	`, today).Scan(&stats.TodaySuccess)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("查询今日成功次数失败", "error", err)
		return nil, err
	}

	// 获取今日失败次数
	err = db.QueryRow(`
		SELECT COUNT(*) FROM execution_logs 
		WHERE DATE(started_at) = ? AND status = 'failed'
	`, today).Scan(&stats.TodayFailed)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("查询今日失败次数失败", "error", err)
		return nil, err
	}

	// 计算成功率（成功率 = 成功 / (成功 + 失败)）
	totalToday := stats.TodaySuccess + stats.TodayFailed
	if totalToday > 0 {
		stats.SuccessRate = float64(stats.TodaySuccess) / float64(totalToday) * 100
	}

	// 获取已启用隧道数（从运行时获取）
	stats.RunningTunnels = tunnelRT.GetRunningCount()
	slog.Debug("运行中隧道数", "count", stats.RunningTunnels)

	// 获取最近7天最常执行的top10
	topRows, err := db.Query(`
		SELECT operation_type, target_name, COUNT(*) as execute_count
		FROM execution_logs 
		WHERE started_at >= datetime('now', '-7 days')
		GROUP BY operation_type, target_name
		ORDER BY execute_count DESC
		LIMIT 10
	`)
	if err != nil {
		slog.Error("查询最常执行记录失败", "error", err)
		return nil, err
	}
	defer topRows.Close()

	for topRows.Next() {
		var item TopExecutedItem
		err := topRows.Scan(&item.OperationType, &item.TargetName, &item.ExecuteCount)
		if err != nil {
			slog.Error("扫描最常执行记录失败", "error", err)
			continue
		}
		stats.TopExecuted = append(stats.TopExecuted, item)
	}
	slog.Debug("最常执行记录", "count", len(stats.TopExecuted))

	// 获取最近10条执行记录
	rows, err := db.Query(`
		SELECT id, operation_type, target_id, target_name, action, 
			   environment_id, environment_name, mode, connection_id, connection_name,
			   status, exit_code, output, error_message, started_at, finished_at, 
			   duration_ms, username, created_at
		FROM execution_logs 
		ORDER BY started_at DESC 
		LIMIT 10
	`)
	if err != nil {
		slog.Error("查询最近执行记录失败", "error", err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var log ExecutionLog
		err := rows.Scan(
			&log.ID, &log.OperationType, &log.TargetID, &log.TargetName, &log.Action,
			&log.EnvironmentID, &log.EnvironmentName, &log.Mode, &log.ConnectionID, &log.ConnectionName,
			&log.Status, &log.ExitCode, &log.Output, &log.ErrorMessage, &log.StartedAt, &log.FinishedAt,
			&log.DurationMs, &log.Username, &log.CreatedAt,
		)
		if err != nil {
			slog.Error("扫描执行记录失败", "error", err)
			continue
		}
		stats.RecentExecutions = append(stats.RecentExecutions, log)
	}
	slog.Debug("最近执行记录", "count", len(stats.RecentExecutions))

	// 获取最近10条失败记录
	failRows, err := db.Query(`
		SELECT id, operation_type, target_id, target_name, action, 
			   environment_id, environment_name, mode, connection_id, connection_name,
			   status, exit_code, output, error_message, started_at, finished_at, 
			   duration_ms, username, created_at
		FROM execution_logs 
		WHERE status = 'failed'
		ORDER BY started_at DESC 
		LIMIT 10
	`)
	if err != nil {
		slog.Error("查询最近失败记录失败", "error", err)
		return nil, err
	}
	defer failRows.Close()

	for failRows.Next() {
		var log ExecutionLog
		err := failRows.Scan(
			&log.ID, &log.OperationType, &log.TargetID, &log.TargetName, &log.Action,
			&log.EnvironmentID, &log.EnvironmentName, &log.Mode, &log.ConnectionID, &log.ConnectionName,
			&log.Status, &log.ExitCode, &log.Output, &log.ErrorMessage, &log.StartedAt, &log.FinishedAt,
			&log.DurationMs, &log.Username, &log.CreatedAt,
		)
		if err != nil {
			slog.Error("扫描失败记录失败", "error", err)
			continue
		}
		stats.RecentFailed = append(stats.RecentFailed, log)
	}
	slog.Debug("最近失败记录", "count", len(stats.RecentFailed))

	return stats, nil
}

// CreateExecutionLog 创建执行记录
func CreateExecutionLog(log *ExecutionLog) (int64, error) {
	result, err := db.Exec(`
		INSERT INTO execution_logs (
			operation_type, target_id, target_name, action,
			environment_id, environment_name, mode, connection_id, connection_name,
			status, exit_code, output, error_message,
			started_at, finished_at, duration_ms, username
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		log.OperationType, log.TargetID, log.TargetName, log.Action,
		log.EnvironmentID, log.EnvironmentName, log.Mode, log.ConnectionID, log.ConnectionName,
		log.Status, log.ExitCode, log.Output, log.ErrorMessage,
		log.StartedAt, log.FinishedAt, log.DurationMs, log.Username,
	)
	if err != nil {
		slog.Error("创建执行记录失败", "error", err)
		return 0, err
	}

	id, _ := result.LastInsertId()
	slog.Info("执行记录创建成功", "id", id, "type", log.OperationType, "target", log.TargetName)
	return id, nil
}

// UpdateExecutionLog 更新执行记录
func UpdateExecutionLog(id int64, status string, exitCode int, output, errorMessage string, durationMs int64) error {
	_, err := db.Exec(`
		UPDATE execution_logs 
		SET status = ?, exit_code = ?, output = ?, error_message = ?, 
			finished_at = datetime('now'), duration_ms = ?
		WHERE id = ?
	`, status, exitCode, output, errorMessage, durationMs, id)
	if err != nil {
		slog.Error("更新执行记录失败", "error", err)
		return err
	}
	return nil
}

// GetExecutionLogs 获取执行记录列表
func (s *DashboardService) GetExecutionLogs(req GetExecutionLogsRequest) ExecutionLogsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ExecutionLogsResponse{Success: false}
	}

	// 构建查询条件
	where := "1=1"
	args := []interface{}{}

	if req.OperationType != "" {
		where += " AND operation_type = ?"
		args = append(args, req.OperationType)
	}
	if req.Status != "" {
		where += " AND status = ?"
		args = append(args, req.Status)
	}
	if req.EnvironmentID > 0 {
		where += " AND environment_id = ?"
		args = append(args, req.EnvironmentID)
	}
	if req.Search != "" {
		where += " AND (target_name LIKE ? OR output LIKE ?)"
		searchPattern := "%" + req.Search + "%"
		args = append(args, searchPattern, searchPattern)
	}

	// 获取总数
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM execution_logs WHERE %s", where)
	err := db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("查询执行记录总数失败", "error", err)
		return ExecutionLogsResponse{Success: false}
	}

	// 分页查询
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := fmt.Sprintf(`
		SELECT id, operation_type, target_id, target_name, action,
			   environment_id, environment_name, mode, connection_id, connection_name,
			   status, exit_code, output, error_message, started_at, finished_at,
			   duration_ms, username, created_at
		FROM execution_logs 
		WHERE %s
		ORDER BY started_at DESC 
		LIMIT ? OFFSET ?
	`, where)
	args = append(args, pageSize, offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("查询执行记录列表失败", "error", err)
		return ExecutionLogsResponse{Success: false}
	}
	defer rows.Close()

	var items []ExecutionLog
	for rows.Next() {
		var log ExecutionLog
		err := rows.Scan(
			&log.ID, &log.OperationType, &log.TargetID, &log.TargetName, &log.Action,
			&log.EnvironmentID, &log.EnvironmentName, &log.Mode, &log.ConnectionID, &log.ConnectionName,
			&log.Status, &log.ExitCode, &log.Output, &log.ErrorMessage, &log.StartedAt, &log.FinishedAt,
			&log.DurationMs, &log.Username, &log.CreatedAt,
		)
		if err != nil {
			slog.Error("扫描执行记录失败", "error", err)
			continue
		}
		items = append(items, log)
	}

	return ExecutionLogsResponse{Success: true, Total: total, Items: items}
}

// ClearExecutionLogs 清空执行记录（按筛选条件）
func (s *DashboardService) ClearExecutionLogs(req ClearExecutionLogsRequest) ClearExecutionLogsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ClearExecutionLogsResponse{Success: false, Message: "未授权访问"}
	}

	// 构建查询条件
	where := "1=1"
	args := []interface{}{}

	if req.OperationType != "" {
		where += " AND operation_type = ?"
		args = append(args, req.OperationType)
	}
	if req.Status != "" {
		where += " AND status = ?"
		args = append(args, req.Status)
	}
	if req.Search != "" {
		where += " AND (target_name LIKE ? OR output LIKE ?)"
		searchPattern := "%" + req.Search + "%"
		args = append(args, searchPattern, searchPattern)
	}

	// 先获取符合条件的记录数量
	var count int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM execution_logs WHERE %s", where)
	err := db.QueryRow(countQuery, args...).Scan(&count)
	if err != nil {
		slog.Error("查询执行记录数量失败", "error", err)
		return ClearExecutionLogsResponse{Success: false, Message: "查询失败"}
	}

	if count == 0 {
		return ClearExecutionLogsResponse{Success: true, Message: "没有需要清空的记录", Count: 0}
	}

	// 删除符合条件的记录
	deleteQuery := fmt.Sprintf("DELETE FROM execution_logs WHERE %s", where)
	result, err := db.Exec(deleteQuery, args...)
	if err != nil {
		slog.Error("清空执行记录失败", "error", err)
		return ClearExecutionLogsResponse{Success: false, Message: "清空失败: " + err.Error()}
	}

	deleted, _ := result.RowsAffected()
	slog.Info("执行记录已清空", "count", deleted)

	return ClearExecutionLogsResponse{
		Success: true,
		Message: fmt.Sprintf("已清空 %d 条执行记录", deleted),
		Count:   deleted,
	}
}

// GetVersion 返回应用版本号
func (d *DashboardService) GetVersion() string {
	return AppVersion
}
