package server

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"opsflash/server/exec"
)

// ==================== 执行器（Executor）工厂 ====================
// 屏蔽「命令在哪里执行」的差异：terminal（本机）/ ssh（远程主机）/ db（数据库）。
// 执行方式（type: non-interactive / interactive / daemon）的分发逻辑在 opsruntime.go 保持不变，
// 只在此处根据 mode 选择合适的执行器（具体实现见 server/exec 子包）。

// executorFor 根据执行位置（mode）与连接信息创建执行器
// interpreter（cmd/powershell/bash）仅对本地 terminal 模式生效，其他模式忽略
func executorFor(mode string, conn *Connection, interpreter string) (exec.Executor, error) {
	switch mode {
	case "", "terminal":
		return exec.NewTerminalExecutor(interpreter), nil
	case "ssh":
		if conn == nil {
			return nil, errors.New("SSH 命令缺少连接配置，请先在「连接管理」中创建 SSH 连接")
		}
		return exec.NewSSHExecutor(exec.SSHConfig{
			Host:           conn.Host,
			Port:           conn.Port,
			Username:       conn.Username,
			AuthMethod:     conn.AuthMethod,
			Password:       conn.Password,
			PrivateKey:     conn.PrivateKey,
			PrivateKeyPath: conn.PrivateKeyPath,
			Passphrase:     conn.Passphrase,
		}), nil
	case "redis":
		if conn == nil {
			return nil, errors.New("Redis 命令缺少连接配置，请先在「连接管理」中创建 Redis 连接")
		}
		if err := applyTunnelForDB(conn); err != nil {
			return nil, err
		}
		dbIdx := 0
		if conn.Database != "" {
			dbIdx, _ = strconv.Atoi(conn.Database)
		}
		return exec.NewRedisExecutor(exec.RedisConfig{
			Host:     conn.Host,
			Port:     conn.Port,
			Password: conn.Password,
			DB:       dbIdx,
		}), nil
	case "mysql":
		if conn == nil {
			return nil, errors.New("MySQL 命令缺少连接配置，请先在「连接管理」中创建 MySQL 连接")
		}
		if err := applyTunnelForDB(conn); err != nil {
			return nil, err
		}
		return exec.NewMySQLExecutor(exec.MySQLConfig{
			Host:     conn.Host,
			Port:     conn.Port,
			Username: conn.Username,
			Password: conn.Password,
			Database: conn.Database,
		}), nil
	case "tdengine":
		if conn == nil {
			return nil, errors.New("TDengine 命令缺少连接配置，请先在「连接管理」中创建 TDengine 连接")
		}
		if err := applyTunnelForDB(conn); err != nil {
			return nil, err
		}
		return exec.NewTDengineExecutor(exec.TDengineConfig{
			Host:     conn.Host,
			Port:     conn.Port,
			Username: conn.Username,
			Password: conn.Password,
			Database: conn.Database,
		}), nil
	default:
		return nil, fmt.Errorf("执行模式 %q 尚未支持", mode)
	}
}

// applyTunnelForDB 若数据库连接关联了隧道（tunnel_id > 0），自动确保隧道运行
// 并将 conn.Host/conn.Port 替换为隧道的本地监听地址，实现「数据库命令自动走隧道」。
// 仅对 redis/mysql/tdengine 模式有效；SSH 模式不经过隧道。
func applyTunnelForDB(conn *Connection) error {
	if conn.TunnelId <= 0 {
		return nil
	}

	// 查询隧道配置
	row := db.QueryRow("SELECT "+tunnelCols+" FROM tunnels WHERE id = ?", conn.TunnelId)
	t, err := scanTunnel(row)
	if err == sql.ErrNoRows {
		return fmt.Errorf("关联的隧道(id=%d)不存在", conn.TunnelId)
	}
	if err != nil {
		return fmt.Errorf("查询隧道失败: %w", err)
	}

	// 仅 local 类型隧道适用于 DB 路由（remote/dynamic 不指向固定远程目标）
	if t.Type != "local" {
		return fmt.Errorf("数据库连接仅支持 local 类型隧道，当前隧道「%s」为 %s 类型", t.Name, t.Type)
	}

	// 检查隧道是否已在运行
	if status, ok := tunnelRT.GetStatus(t.ID); ok && status.Running {
		// 隧道已运行，替换地址
		originalHost := conn.Host
		originalPort := conn.Port
		conn.Host = t.LocalHost
		conn.Port = t.LocalPort
		slog.Debug("数据库连接走隧道", "tunnel", t.Name,
			"original", fmt.Sprintf("%s:%d", originalHost, originalPort),
			"via", fmt.Sprintf("%s:%d", conn.Host, conn.Port))
		return nil
	}

	// 隧道未运行，自动启动
	slog.Info("数据库连接关联的隧道未运行，自动启动", "tunnel", t.Name, "id", t.ID)
	if err := tunnelRT.StartTunnel(t); err != nil {
		return fmt.Errorf("启动关联隧道「%s」失败: %w", t.Name, err)
	}

	// 替换地址
	originalHost := conn.Host
	originalPort := conn.Port
	conn.Host = t.LocalHost
	conn.Port = t.LocalPort
	slog.Info("数据库连接已走隧道", "tunnel", t.Name,
		"original", fmt.Sprintf("%s:%d", originalHost, originalPort),
		"via", fmt.Sprintf("%s:%d", conn.Host, conn.Port))
	return nil
}

// validCommandModes 合法的执行位置
var validCommandModes = map[string]bool{
	"terminal": true,
	"ssh":      true,
	"redis":    true,
	"mysql":    true,
	"tdengine": true,
}
