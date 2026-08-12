package server

import (
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
)

// ==================== 批量创建隧道（单个 SSH 连接一键建 N 条端口映射） ====================

// CreateTunnelItem 批量创建中的单条隧道配置
type CreateTunnelItem struct {
	Name          string `json:"name"`
	Type          string `json:"type"` // local | remote | dynamic
	LocalHost     string `json:"localHost"`
	LocalPort     int    `json:"localPort"`
	RemoteHost    string `json:"remoteHost"`
	RemotePort    int    `json:"remotePort"`
	GroupName     string `json:"groupName"`
	AutoStart     bool   `json:"autoStart"`
	AutoReconnect bool   `json:"autoReconnect"`
	Remark        string `json:"remark"`
}

// CreateTunnelsRequest 批量创建请求
type CreateTunnelsRequest struct {
	Token        string             `json:"token"`
	ConnectionId int                `json:"connectionId"`
	Tunnels      []CreateTunnelItem `json:"tunnels"`
	StartAll     bool               `json:"startAll"` // 创建后是否立即全部启动
}

// BatchTunnelFailure 批量创建中的失败项
type BatchTunnelFailure struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// CreateTunnelsResponse 批量创建响应
type CreateTunnelsResponse struct {
	Success bool                 `json:"success"`
	Created []Tunnel             `json:"created"`
	Failed  []BatchTunnelFailure `json:"failed"`
	Message string               `json:"message"`
}

// maxBatchTunnels 单次批量创建上限
const maxBatchTunnels = 20

// CreateTunnels 批量创建隧道：同一 SSH 连接一次创建多条端口映射
// 逐条校验（复用 validateTunnel），失败项收集到 Failed 不中断；StartAll=true 时创建成功后自动启动。
func (s *TunnelService) CreateTunnels(req CreateTunnelsRequest) CreateTunnelsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return CreateTunnelsResponse{Success: false, Message: "会话已过期"}
	}

	if req.ConnectionId <= 0 {
		return CreateTunnelsResponse{Success: false, Message: "必须选择 SSH 连接"}
	}
	// 校验连接存在且为 ssh
	var connType string
	if err := db.QueryRow("SELECT type FROM connections WHERE id = ?", req.ConnectionId).Scan(&connType); err != nil {
		if err == sql.ErrNoRows {
			return CreateTunnelsResponse{Success: false, Message: "所选 SSH 连接不存在"}
		}
		return CreateTunnelsResponse{Success: false, Message: "校验连接失败: " + err.Error()}
	}
	if connType != "ssh" {
		return CreateTunnelsResponse{Success: false, Message: "批量创建隧道仅支持 SSH 类型连接"}
	}

	if len(req.Tunnels) == 0 {
		return CreateTunnelsResponse{Success: false, Message: "至少需要一条端口映射"}
	}
	if len(req.Tunnels) > maxBatchTunnels {
		return CreateTunnelsResponse{Success: false, Message: "单次最多创建 " + strconv.Itoa(maxBatchTunnels) + " 条隧道"}
	}

	var created []Tunnel
	var failed []BatchTunnelFailure

	for i := range req.Tunnels {
		item := req.Tunnels[i]
		name := strings.TrimSpace(item.Name)
		tunnelType := strings.TrimSpace(item.Type)
		if tunnelType == "" {
			tunnelType = "local"
		}
		localHost := strings.TrimSpace(item.LocalHost)
		if localHost == "" {
			localHost = "127.0.0.1"
		}
		remoteHost := strings.TrimSpace(item.RemoteHost)
		if remoteHost == "" {
			remoteHost = "127.0.0.1"
		}

		if err := validateTunnel(name, req.ConnectionId, tunnelType, item.LocalPort, item.RemotePort); err != nil {
			failed = append(failed, BatchTunnelFailure{Name: name, Message: err.Error()})
			continue
		}

		autoStart := 0
		if item.AutoStart {
			autoStart = 1
		}
		autoReconnect := 1
		if !item.AutoReconnect {
			autoReconnect = 0
		}

		result, err := db.Exec(`INSERT INTO tunnels
			(name, connection_id, type, local_host, local_port, remote_host, remote_port, group_name, auto_start, auto_reconnect, remark)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			name, req.ConnectionId, tunnelType, localHost, item.LocalPort,
			remoteHost, item.RemotePort, strings.TrimSpace(item.GroupName), autoStart, autoReconnect,
			strings.TrimSpace(item.Remark))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				failed = append(failed, BatchTunnelFailure{Name: name, Message: "隧道名称已存在"})
			} else {
				slog.Error("批量创建隧道失败", "name", name, "error", err)
				failed = append(failed, BatchTunnelFailure{Name: name, Message: "创建失败: " + err.Error()})
			}
			continue
		}

		id, _ := result.LastInsertId()
		created = append(created, Tunnel{
			ID:            int(id),
			Name:          name,
			ConnectionId:  req.ConnectionId,
			Type:          tunnelType,
			LocalHost:     localHost,
			LocalPort:     item.LocalPort,
			RemoteHost:    remoteHost,
			RemotePort:    item.RemotePort,
			GroupName:     strings.TrimSpace(item.GroupName),
			AutoStart:     item.AutoStart,
			AutoReconnect: item.AutoReconnect,
			Remark:        strings.TrimSpace(item.Remark),
		})
	}

	// 创建成功后按需启动
	started := 0
	if req.StartAll {
		for i := range created {
			t := &created[i]
			if err := tunnelRT.StartTunnel(t); err != nil {
				slog.Warn("批量启动隧道失败", "id", t.ID, "name", t.Name, "error", err)
				failed = append(failed, BatchTunnelFailure{Name: t.Name, Message: "创建成功但启动失败: " + err.Error()})
			} else {
				started++
			}
		}
	}

	slog.Info("批量创建隧道完成", "connectionId", req.ConnectionId,
		"created", len(created), "failed", len(failed), "started", started)

	msg := "成功创建 " + strconv.Itoa(len(created)) + " 条隧道"
	if started > 0 {
		msg += "，已启动 " + strconv.Itoa(started) + " 条"
	}
	if len(failed) > 0 {
		msg += "，" + strconv.Itoa(len(failed)) + " 条失败"
	}
	return CreateTunnelsResponse{
		Success: true,
		Created: created,
		Failed:  failed,
		Message: msg,
	}
}
