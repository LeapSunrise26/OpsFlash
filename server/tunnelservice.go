package server

import (
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"
)

// ==================== 隧道管理（数据层 + CRUD API）====================
// 隧道（Tunnel）= 基于 SSH 连接的端口转发配置。
// 通过 connection_id 关联 SSH 连接，敏感凭据由 connections 表间接管理（复用 loadConnectionByID 解密）。
// 运行时控制（启停/分组）见 tunnelcontrol.go；批量创建见 tunnelbatch.go；
// 日志见 tunnellogs.go；导入导出见 tunnelio.go。

// Tunnel 隧道信息
type Tunnel struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	ConnectionId   int    `json:"connectionId"`
	ConnectionName string `json:"connectionName"` // JOIN 带出
	Type           string `json:"type"`           // local | remote | dynamic
	LocalHost      string `json:"localHost"`
	LocalPort      int    `json:"localPort"`
	RemoteHost     string `json:"remoteHost"`
	RemotePort     int    `json:"remotePort"`
	GroupName      string `json:"groupName"` // 分组标签（空=未分组）
	AutoStart      bool   `json:"autoStart"`
	AutoReconnect  bool   `json:"autoReconnect"`
	SortOrder      int    `json:"sortOrder"`
	Remark         string `json:"remark"`
	// 运行时状态（GetTunnels 时附带）
	Running      bool   `json:"running"`
	Connections  int    `json:"connections"`
	Reconnecting bool   `json:"reconnecting"`
	Error        string `json:"error,omitempty"`
	BytesIn      int64  `json:"bytesIn"`
	BytesOut     int64  `json:"bytesOut"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// 请求/响应类型
type GetTunnelsRequest struct {
	Token        string `json:"token"`
	ConnectionId int    `json:"connectionId"` // 可选，筛选指定 SSH 连接的隧道
}

type CreateTunnelRequest struct {
	Token         string `json:"token"`
	Name          string `json:"name"`
	ConnectionId  int    `json:"connectionId"`
	Type          string `json:"type"`
	LocalHost     string `json:"localHost"`
	LocalPort     int    `json:"localPort"`
	RemoteHost    string `json:"remoteHost"`
	RemotePort    int    `json:"remotePort"`
	GroupName     string `json:"groupName"`
	AutoStart     bool   `json:"autoStart"`
	AutoReconnect bool   `json:"autoReconnect"`
	Remark        string `json:"remark"`
}

type UpdateTunnelRequest struct {
	Token         string `json:"token"`
	ID            int    `json:"id"`
	Name          string `json:"name"`
	ConnectionId  int    `json:"connectionId"`
	Type          string `json:"type"`
	LocalHost     string `json:"localHost"`
	LocalPort     int    `json:"localPort"`
	RemoteHost    string `json:"remoteHost"`
	RemotePort    int    `json:"remotePort"`
	GroupName     string `json:"groupName"`
	AutoStart     bool   `json:"autoStart"`
	AutoReconnect bool   `json:"autoReconnect"`
	Remark        string `json:"remark"`
}

type DeleteTunnelRequest struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
}

type TunnelsResponse struct {
	Success bool     `json:"success"`
	Tunnels []Tunnel `json:"tunnels"`
	Message string   `json:"message"`
}

type TunnelResponse struct {
	Success bool    `json:"success"`
	Tunnel  *Tunnel `json:"tunnel"`
	Message string  `json:"message"`
}

type DeleteTunnelResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Blocked bool   `json:"blocked"`
}

// TunnelService 隧道管理服务
type TunnelService struct{}

// 隧道类型合法性
var validTunnelTypes = map[string]bool{
	"local":   true,
	"remote":  true,
	"dynamic": true,
}

// scanTunnel 从查询结果扫描一行隧道记录（不含 connection_name）
func scanTunnel(row interface{ Scan(...interface{}) error }) (*Tunnel, error) {
	var t Tunnel
	var autoStart, autoReconnect int
	err := row.Scan(&t.ID, &t.Name, &t.ConnectionId, &t.Type,
		&t.LocalHost, &t.LocalPort, &t.RemoteHost, &t.RemotePort,
		&t.GroupName, &autoStart, &autoReconnect, &t.SortOrder, &t.Remark,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.AutoStart = autoStart == 1
	t.AutoReconnect = autoReconnect == 1
	return &t, nil
}

const tunnelCols = "id, name, connection_id, type, local_host, local_port, remote_host, remote_port, group_name, auto_start, auto_reconnect, sort_order, remark, created_at, updated_at"

// validateTunnel 校验隧道字段
func validateTunnel(name string, connId int, tunnelType string, localPort, remotePort int) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("隧道名称不能为空")
	}
	if connId <= 0 {
		return errors.New("必须选择 SSH 连接")
	}
	tunnelType = strings.TrimSpace(tunnelType)
	if tunnelType == "" {
		tunnelType = "local"
	}
	if !validTunnelTypes[tunnelType] {
		return errors.New("隧道类型无效，仅支持 local、remote、dynamic")
	}
	if localPort <= 0 || localPort > 65535 {
		return errors.New("本地端口无效（1-65535）")
	}
	// remote 类型需要远程监听端口；dynamic(SOCKS5) 无固定远程目标
	if tunnelType != "dynamic" {
		if remotePort <= 0 || remotePort > 65535 {
			return errors.New("远程端口无效（1-65535）")
		}
	}
	// 检查 connection_id 对应的连接是否存在且为 ssh 类型
	var connType string
	err := db.QueryRow("SELECT type FROM connections WHERE id = ?", connId).Scan(&connType)
	if err == sql.ErrNoRows {
		return errors.New("所选 SSH 连接不存在")
	}
	if err != nil {
		return errors.New("校验连接失败: " + err.Error())
	}
	if connType != "ssh" {
		return errors.New("隧道仅支持 SSH 类型连接")
	}
	return nil
}

// ==================== 隧道 CRUD API ====================

// GetTunnels 获取隧道列表（可按连接筛选，附带运行时状态）
func (s *TunnelService) GetTunnels(req GetTunnelsRequest) TunnelsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelsResponse{Success: false, Message: "会话已过期"}
	}

	query := `SELECT t.` + strings.ReplaceAll(tunnelCols, ", ", ", t.") + `, c.name
		FROM tunnels t
		LEFT JOIN connections c ON t.connection_id = c.id`
	var args []interface{}
	if req.ConnectionId > 0 {
		query += " WHERE t.connection_id = ?"
		args = append(args, req.ConnectionId)
	}
	query += " ORDER BY t.sort_order ASC, t.id ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("查询隧道列表失败", "error", err)
		return TunnelsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var tunnels []Tunnel
	for rows.Next() {
		var t Tunnel
		var autoStart, autoReconnect int
		err := rows.Scan(&t.ID, &t.Name, &t.ConnectionId, &t.Type,
			&t.LocalHost, &t.LocalPort, &t.RemoteHost, &t.RemotePort,
			&t.GroupName, &autoStart, &autoReconnect, &t.SortOrder, &t.Remark,
			&t.CreatedAt, &t.UpdatedAt, &t.ConnectionName)
		if err != nil {
			slog.Error("扫描隧道记录失败", "error", err)
			continue
		}
		t.AutoStart = autoStart == 1
		t.AutoReconnect = autoReconnect == 1

		// 附带运行时状态
		if status, ok := tunnelRT.GetStatus(t.ID); ok {
			t.Running = status.Running
			t.Connections = status.Connections
			t.Reconnecting = status.Reconnecting
			t.Error = status.Error
			t.BytesIn = status.BytesIn
			t.BytesOut = status.BytesOut
		}

		tunnels = append(tunnels, t)
	}

	slog.Debug("获取隧道列表", "count", len(tunnels))
	return TunnelsResponse{Success: true, Tunnels: tunnels}
}

// CreateTunnel 创建隧道
func (s *TunnelService) CreateTunnel(req CreateTunnelRequest) TunnelResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelResponse{Success: false, Message: "会话已过期"}
	}

	name := strings.TrimSpace(req.Name)
	tunnelType := strings.TrimSpace(req.Type)
	if tunnelType == "" {
		tunnelType = "local"
	}
	localHost := strings.TrimSpace(req.LocalHost)
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	remoteHost := strings.TrimSpace(req.RemoteHost)
	if remoteHost == "" {
		remoteHost = "127.0.0.1"
	}

	if err := validateTunnel(name, req.ConnectionId, tunnelType, req.LocalPort, req.RemotePort); err != nil {
		return TunnelResponse{Success: false, Message: err.Error()}
	}

	autoStart := 0
	if req.AutoStart {
		autoStart = 1
	}
	autoReconnect := 1
	if !req.AutoReconnect {
		autoReconnect = 0
	}

	result, err := db.Exec(`INSERT INTO tunnels
		(name, connection_id, type, local_host, local_port, remote_host, remote_port, group_name, auto_start, auto_reconnect, remark)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, req.ConnectionId, tunnelType, localHost, req.LocalPort,
		remoteHost, req.RemotePort, strings.TrimSpace(req.GroupName), autoStart, autoReconnect,
		strings.TrimSpace(req.Remark))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return TunnelResponse{Success: false, Message: "隧道名称已存在"}
		}
		slog.Error("创建隧道失败", "name", name, "error", err)
		return TunnelResponse{Success: false, Message: "创建失败: " + err.Error()}
	}

	id, _ := result.LastInsertId()
	slog.Info("隧道创建成功", "id", id, "name", name, "type", tunnelType,
		"local", localHost+":"+strconv.Itoa(req.LocalPort),
		"remote", remoteHost+":"+strconv.Itoa(req.RemotePort),
		"group", req.GroupName)

	// 获取连接名称用于返回
	var connName string
	db.QueryRow("SELECT name FROM connections WHERE id = ?", req.ConnectionId).Scan(&connName)

	return TunnelResponse{
		Success: true,
		Tunnel: &Tunnel{
			ID:             int(id),
			Name:           name,
			ConnectionId:   req.ConnectionId,
			ConnectionName: connName,
			Type:           tunnelType,
			LocalHost:      localHost,
			LocalPort:      req.LocalPort,
			RemoteHost:     remoteHost,
			RemotePort:     req.RemotePort,
			GroupName:      strings.TrimSpace(req.GroupName),
			AutoStart:      req.AutoStart,
			AutoReconnect:  req.AutoReconnect,
			Remark:         strings.TrimSpace(req.Remark),
		},
		Message: "隧道创建成功",
	}
}

// UpdateTunnel 修改隧道
func (s *TunnelService) UpdateTunnel(req UpdateTunnelRequest) TunnelResponse {
	if _, ok := validateSession(req.Token); !ok {
		return TunnelResponse{Success: false, Message: "会话已过期"}
	}

	// 检查隧道是否存在
	var exists int
	err := db.QueryRow("SELECT COUNT(*) FROM tunnels WHERE id = ?", req.ID).Scan(&exists)
	if err != nil {
		return TunnelResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	if exists == 0 {
		return TunnelResponse{Success: false, Message: "隧道不存在"}
	}

	name := strings.TrimSpace(req.Name)
	tunnelType := strings.TrimSpace(req.Type)
	if tunnelType == "" {
		tunnelType = "local"
	}
	localHost := strings.TrimSpace(req.LocalHost)
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	remoteHost := strings.TrimSpace(req.RemoteHost)
	if remoteHost == "" {
		remoteHost = "127.0.0.1"
	}

	if err := validateTunnel(name, req.ConnectionId, tunnelType, req.LocalPort, req.RemotePort); err != nil {
		return TunnelResponse{Success: false, Message: err.Error()}
	}

	autoStart := 0
	if req.AutoStart {
		autoStart = 1
	}
	autoReconnect := 1
	if !req.AutoReconnect {
		autoReconnect = 0
	}

	// 如果隧道正在运行，先停止
	tunnelRT.StopTunnel(req.ID)

	_, err = db.Exec(`UPDATE tunnels SET
		name = ?, connection_id = ?, type = ?, local_host = ?, local_port = ?,
		remote_host = ?, remote_port = ?, group_name = ?, auto_start = ?, auto_reconnect = ?,
		remark = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		name, req.ConnectionId, tunnelType, localHost, req.LocalPort,
		remoteHost, req.RemotePort, strings.TrimSpace(req.GroupName), autoStart, autoReconnect,
		strings.TrimSpace(req.Remark), req.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return TunnelResponse{Success: false, Message: "隧道名称已存在"}
		}
		slog.Error("修改隧道失败", "id", req.ID, "name", name, "error", err)
		return TunnelResponse{Success: false, Message: "修改失败: " + err.Error()}
	}

	var connName string
	db.QueryRow("SELECT name FROM connections WHERE id = ?", req.ConnectionId).Scan(&connName)

	slog.Info("隧道修改成功", "id", req.ID, "name", name)
	return TunnelResponse{
		Success: true,
		Tunnel: &Tunnel{
			ID:             req.ID,
			Name:           name,
			ConnectionId:   req.ConnectionId,
			ConnectionName: connName,
			Type:           tunnelType,
			LocalHost:      localHost,
			LocalPort:      req.LocalPort,
			RemoteHost:     remoteHost,
			RemotePort:     req.RemotePort,
			GroupName:      strings.TrimSpace(req.GroupName),
			AutoStart:      req.AutoStart,
			AutoReconnect:  req.AutoReconnect,
			Remark:         strings.TrimSpace(req.Remark),
		},
		Message: "隧道修改成功",
	}
}

// DeleteTunnel 删除隧道（运行中先停止）
func (s *TunnelService) DeleteTunnel(req DeleteTunnelRequest) DeleteTunnelResponse {
	if _, ok := validateSession(req.Token); !ok {
		return DeleteTunnelResponse{Success: false, Message: "会话已过期"}
	}

	var tunnelName string
	err := db.QueryRow("SELECT name FROM tunnels WHERE id = ?", req.ID).Scan(&tunnelName)
	if err == sql.ErrNoRows {
		return DeleteTunnelResponse{Success: false, Message: "隧道不存在"}
	}
	if err != nil {
		return DeleteTunnelResponse{Success: false, Message: "查询失败: " + err.Error()}
	}

	// 先停止运行中的隧道
	tunnelRT.StopTunnel(req.ID)

	_, err = db.Exec("DELETE FROM tunnels WHERE id = ?", req.ID)
	if err != nil {
		slog.Error("删除隧道失败", "id", req.ID, "error", err)
		return DeleteTunnelResponse{Success: false, Message: "删除失败: " + err.Error()}
	}

	slog.Info("隧道删除成功", "id", req.ID, "name", tunnelName)
	return DeleteTunnelResponse{Success: true, Message: "隧道「" + tunnelName + "」已删除"}
}
