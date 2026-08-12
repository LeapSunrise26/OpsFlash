package server

import (
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
)

// ==================== 隧道配置导入导出 ====================

// TunnelExport 导出的隧道配置项（按连接名引用，不含敏感信息）
type TunnelExport struct {
	Name           string `json:"name"`
	ConnectionName string `json:"connectionName"`
	Type           string `json:"type"`
	LocalHost      string `json:"localHost"`
	LocalPort      int    `json:"localPort"`
	RemoteHost     string `json:"remoteHost"`
	RemotePort     int    `json:"remotePort"`
	GroupName      string `json:"groupName"`
	AutoStart      bool   `json:"autoStart"`
	AutoReconnect  bool   `json:"autoReconnect"`
	Remark         string `json:"remark"`
}

// ExportTunnelsRequest 导出请求
type ExportTunnelsRequest struct {
	Token string `json:"token"`
}

// ExportTunnelsResponse 导出响应
type ExportTunnelsResponse struct {
	Success bool           `json:"success"`
	Tunnels []TunnelExport `json:"tunnels"`
	Message string         `json:"message"`
}

// ImportTunnelsRequest 导入请求
type ImportTunnelsRequest struct {
	Token   string         `json:"token"`
	Tunnels []TunnelExport `json:"tunnels"`
}

// ImportTunnelsResponse 导入响应
type ImportTunnelsResponse struct {
	Success bool                 `json:"success"`
	Created []Tunnel             `json:"created"`
	Failed  []BatchTunnelFailure `json:"failed"`
	Message string               `json:"message"`
}

// ExportTunnels 导出全部隧道配置（供备份/迁移）
func (s *TunnelService) ExportTunnels(req ExportTunnelsRequest) ExportTunnelsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ExportTunnelsResponse{Success: false, Message: "会话已过期"}
	}

	rows, err := db.Query(`SELECT t.` + strings.ReplaceAll(tunnelCols, ", ", ", t.") + `, c.name
		FROM tunnels t LEFT JOIN connections c ON t.connection_id = c.id
		ORDER BY t.sort_order ASC, t.id ASC`)
	if err != nil {
		return ExportTunnelsResponse{Success: false, Message: "查询失败: " + err.Error()}
	}
	defer rows.Close()

	var out []TunnelExport
	for rows.Next() {
		var t Tunnel
		var autoStart, autoReconnect int
		if err := rows.Scan(&t.ID, &t.Name, &t.ConnectionId, &t.Type,
			&t.LocalHost, &t.LocalPort, &t.RemoteHost, &t.RemotePort,
			&t.GroupName, &autoStart, &autoReconnect, &t.SortOrder, &t.Remark,
			&t.CreatedAt, &t.UpdatedAt, &t.ConnectionName); err != nil {
			continue
		}
		out = append(out, TunnelExport{
			Name:           t.Name,
			ConnectionName: t.ConnectionName,
			Type:           t.Type,
			LocalHost:      t.LocalHost,
			LocalPort:      t.LocalPort,
			RemoteHost:     t.RemoteHost,
			RemotePort:     t.RemotePort,
			GroupName:      t.GroupName,
			AutoStart:      autoStart == 1,
			AutoReconnect:  autoReconnect == 1,
			Remark:         t.Remark,
		})
	}

	return ExportTunnelsResponse{Success: true, Tunnels: out, Message: "已导出 " + strconv.Itoa(len(out)) + " 条隧道"}
}

// ImportTunnels 导入隧道配置（按连接名匹配 SSH 连接，逐条校验失败不中断）
func (s *TunnelService) ImportTunnels(req ImportTunnelsRequest) ImportTunnelsResponse {
	if _, ok := validateSession(req.Token); !ok {
		return ImportTunnelsResponse{Success: false, Message: "会话已过期"}
	}
	if len(req.Tunnels) == 0 {
		return ImportTunnelsResponse{Success: false, Message: "没有可导入的隧道配置"}
	}
	if len(req.Tunnels) > maxBatchTunnels*5 {
		return ImportTunnelsResponse{Success: false, Message: "单次最多导入 " + strconv.Itoa(maxBatchTunnels*5) + " 条隧道"}
	}

	var created []Tunnel
	var failed []BatchTunnelFailure

	for i := range req.Tunnels {
		item := req.Tunnels[i]
		name := strings.TrimSpace(item.Name)
		connName := strings.TrimSpace(item.ConnectionName)
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

		// 按连接名匹配 SSH 连接
		var connId int
		var connType string
		err := db.QueryRow("SELECT id, type FROM connections WHERE name = ?", connName).Scan(&connId, &connType)
		if err == sql.ErrNoRows {
			failed = append(failed, BatchTunnelFailure{Name: name, Message: "连接「" + connName + "」不存在，请先导入连接"})
			continue
		}
		if err != nil {
			failed = append(failed, BatchTunnelFailure{Name: name, Message: "查询连接失败: " + err.Error()})
			continue
		}
		if connType != "ssh" {
			failed = append(failed, BatchTunnelFailure{Name: name, Message: "连接「" + connName + "」不是 SSH 类型"})
			continue
		}

		if err := validateTunnel(name, connId, tunnelType, item.LocalPort, item.RemotePort); err != nil {
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
			name, connId, tunnelType, localHost, item.LocalPort,
			remoteHost, item.RemotePort, strings.TrimSpace(item.GroupName), autoStart, autoReconnect,
			strings.TrimSpace(item.Remark))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				failed = append(failed, BatchTunnelFailure{Name: name, Message: "隧道名称已存在"})
			} else {
				slog.Error("导入隧道失败", "name", name, "error", err)
				failed = append(failed, BatchTunnelFailure{Name: name, Message: "创建失败: " + err.Error()})
			}
			continue
		}

		id, _ := result.LastInsertId()
		created = append(created, Tunnel{
			ID:             int(id),
			Name:           name,
			ConnectionId:   connId,
			ConnectionName: connName,
			Type:           tunnelType,
			LocalHost:      localHost,
			LocalPort:      item.LocalPort,
			RemoteHost:     remoteHost,
			RemotePort:     item.RemotePort,
			GroupName:      strings.TrimSpace(item.GroupName),
			AutoStart:      item.AutoStart,
			AutoReconnect:  item.AutoReconnect,
			Remark:         strings.TrimSpace(item.Remark),
		})
	}

	slog.Info("导入隧道完成", "created", len(created), "failed", len(failed))
	msg := "成功导入 " + strconv.Itoa(len(created)) + " 条隧道"
	if len(failed) > 0 {
		msg += "，" + strconv.Itoa(len(failed)) + " 条失败"
	}
	return ImportTunnelsResponse{Success: true, Created: created, Failed: failed, Message: msg}
}
