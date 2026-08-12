package server

import (
	"opsflash/server/tunnel"
)

// ==================== 隧道日志 ====================

// GetTunnelLogsRequest 获取隧道日志请求
type GetTunnelLogsRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

// TunnelLogsResponse 隧道日志响应
type TunnelLogsResponse struct {
	Success bool              `json:"success"`
	Logs    []tunnel.LogEntry `json:"logs"`
	Message string            `json:"message"`
}

// GetTunnelLogs 获取隧道运行日志（启动/停止/连接/重连/错误事件，最新在前）
func (s *TunnelService) GetTunnelLogs(req GetTunnelLogsRequest) TunnelLogsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelLogsResponse{Success: false, Message: "会话已过期"}
	}
	logs := tunnelRT.GetLogs(req.ID)
	if logs == nil {
		logs = []tunnel.LogEntry{}
	}
	return TunnelLogsResponse{Success: true, Logs: logs}
}
