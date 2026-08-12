package server

import (
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

// ==================== 隧道运行时控制 API ====================
// 单条启停、全部启停、按分组启停、顶部栏状态摘要。
// 并发启动统一走 runStartTunnels（避免 SSH 10s 握手超时串行叠加）。

// 请求/响应类型
type StartTunnelRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type StopTunnelRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type TunnelStatusResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Running bool   `json:"running"`
}

type TunnelStatusesResponse struct {
	Success bool     `json:"success"`
	Tunnels []Tunnel `json:"tunnels"`
	Message string   `json:"message"`
}

// 顶部栏隧道状态摘要
type TunnelSummaryRequest struct {
	Token string `json:"token"`
}

type TunnelSummaryResponse struct {
	Success      bool   `json:"success"`
	Total        int    `json:"total"`
	Running      int    `json:"running"`
	Reconnecting int    `json:"reconnecting"`
	Message      string `json:"message"`
}

// 按组启停请求
type TunnelGroupRequest struct {
	Token     string `json:"token"`
	GroupName string `json:"groupName"`
}

// StartTunnel 启动指定隧道
func (s *TunnelService) StartTunnel(req StartTunnelRequest) TunnelStatusResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelStatusResponse{Success: false, Message: "会话已过期"}
	}

	row := db.QueryRow("SELECT "+tunnelCols+" FROM tunnels WHERE id = ?", req.ID)
	t, err := scanTunnel(row)
	if err == sql.ErrNoRows {
		return TunnelStatusResponse{Success: false, Message: "隧道不存在"}
	}
	if err != nil {
		return TunnelStatusResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	if err := tunnelRT.StartTunnel(t); err != nil {
		slog.Error("启动隧道失败", "id", req.ID, "name", t.Name, "error", err)
		return TunnelStatusResponse{Success: false, Message: err.Error()}
	}

	return TunnelStatusResponse{Success: true, Message: "隧道「" + t.Name + "」已启动", Running: true}
}

// StopTunnel 停止指定隧道
func (s *TunnelService) StopTunnel(req StopTunnelRequest) TunnelStatusResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelStatusResponse{Success: false, Message: "会话已过期"}
	}

	var tunnelName string
	err := db.QueryRow("SELECT name FROM tunnels WHERE id = ?", req.ID).Scan(&tunnelName)
	if err == sql.ErrNoRows {
		return TunnelStatusResponse{Success: false, Message: "隧道不存在"}
	}
	if err != nil {
		return TunnelStatusResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	tunnelRT.StopTunnel(req.ID)
	return TunnelStatusResponse{Success: true, Message: "隧道「" + tunnelName + "」已停止", Running: false}
}

// runStartTunnels 并发启动满足条件的隧道（最慢不超过单条 10s 握手超时）
// 已在运行的隧道跳过不算失败；返回各隧道最新状态
func runStartTunnels(where string, args ...interface{}) TunnelStatusesResponse {
	query := "SELECT " + tunnelCols + " FROM tunnels"
	if where != "" {
		query += " " + where
	}
	query += " ORDER BY sort_order ASC, id ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return TunnelStatusesResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var tunnels []Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			continue
		}
		tunnels = append(tunnels, *t)
	}
	if len(tunnels) == 0 {
		return TunnelStatusesResponse{Success: true, Message: "暂无隧道可启动"}
	}

	started := 0
	already := 0
	failed := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range tunnels {
		wg.Add(1)
		go func(t *Tunnel) {
			defer wg.Done()
			err := tunnelRT.StartTunnel(t)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if strings.Contains(err.Error(), "已在运行") {
					already++
				} else {
					slog.Warn("批量启动隧道失败", "id", t.ID, "name", t.Name, "error", err)
					failed++
					t.Error = err.Error()
				}
			} else {
				started++
			}
			// 附带最新运行时状态
			if status, ok := tunnelRT.GetStatus(t.ID); ok {
				t.Running = status.Running
				t.Connections = status.Connections
				t.Reconnecting = status.Reconnecting
			}
		}(&tunnels[i])
	}
	wg.Wait()

	msg := "已启动 " + strconv.Itoa(started) + " 个隧道"
	if already > 0 {
		msg += "，" + strconv.Itoa(already) + " 个已在运行"
	}
	if failed > 0 {
		msg += "，" + strconv.Itoa(failed) + " 个失败"
	}

	return TunnelStatusesResponse{Success: true, Tunnels: tunnels, Message: msg}
}

// StartAllTunnels 启动所有隧道
func (s *TunnelService) StartAllTunnels(req StartTunnelRequest) TunnelStatusesResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelStatusesResponse{Success: false, Message: "会话已过期"}
	}
	return runStartTunnels("")
}

// StopAllTunnels 停止所有隧道
func (s *TunnelService) StopAllTunnels(req StopTunnelRequest) TunnelStatusesResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelStatusesResponse{Success: false, Message: "会话已过期"}
	}

	count := tunnelRT.StopAll()
	return TunnelStatusesResponse{
		Success: true,
		Message: "已停止 " + strconv.Itoa(count) + " 个隧道",
	}
}

// StartTunnelGroup 按分组启动隧道（组名为空=未分组）
func (s *TunnelService) StartTunnelGroup(req TunnelGroupRequest) TunnelStatusesResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelStatusesResponse{Success: false, Message: "会话已过期"}
	}
	return runStartTunnels("WHERE group_name = ?", req.GroupName)
}

// StopTunnelGroup 按分组停止隧道（组名为空=未分组）
func (s *TunnelService) StopTunnelGroup(req TunnelGroupRequest) TunnelStatusesResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelStatusesResponse{Success: false, Message: "会话已过期"}
	}

	rows, err := db.Query("SELECT id FROM tunnels WHERE group_name = ?", req.GroupName)
	if err != nil {
		return TunnelStatusesResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}

	count := 0
	for _, id := range ids {
		tunnelRT.StopTunnel(id)
		count++
	}
	msg := "已停止 " + strconv.Itoa(count) + " 个隧道"
	if req.GroupName != "" {
		msg += "（组：" + req.GroupName + "）"
	}
	return TunnelStatusesResponse{Success: true, Message: msg}
}

// GetTunnelSummary 获取隧道状态摘要（顶部栏指示器用）
// 轻量接口：只返回 total / running / reconnecting 计数，不返回完整列表
func (s *TunnelService) GetTunnelSummary(req TunnelSummaryRequest) TunnelSummaryResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelSummaryResponse{Success: false, Message: "会话已过期"}
	}

	rows, err := db.Query("SELECT id FROM tunnels")
	if err != nil {
		slog.Error("查询隧道总数失败", "error", err)
		return TunnelSummaryResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}

	total := len(ids)
	running := 0
	reconnecting := 0
	for _, id := range ids {
		if status, ok := tunnelRT.GetStatus(id); ok {
			if status.Reconnecting {
				reconnecting++
			}
			if status.Running {
				running++
			}
		}
	}

	return TunnelSummaryResponse{
		Success:      true,
		Total:        total,
		Running:      running,
		Reconnecting: reconnecting,
	}
}
