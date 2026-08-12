package server

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"opsflash/server/exec"
	"opsflash/server/tunnel"
)

// ==================== 隧道运行时管理 ====================
// 管理 tunnels 的生命周期：启动/停止/状态/断线重连/SSH 连接池复用。
// 类比 OpsService 管理 daemon/interactive/stream 会话的模式。

// tunnelRT 全局隧道运行时实例（包级变量，与 db 同级）
var tunnelRT = &TunnelRuntime{
	tunnels: make(map[int]tunnel.Tunnel),
}

// TunnelRuntime 隧道运行时管理器
type TunnelRuntime struct {
	mu      sync.RWMutex
	tunnels map[int]tunnel.Tunnel // tunnelID → Tunnel 实例
}

// StartTunnel 从 DB 配置启动隧道
func (tr *TunnelRuntime) StartTunnel(t *Tunnel) error {
	tr.mu.Lock()
	if _, exists := tr.tunnels[t.ID]; exists {
		tr.mu.Unlock()
		return fmt.Errorf("隧道「%s」已在运行", t.Name)
	}
	tr.mu.Unlock()

	// 加载关联的 SSH 连接（解密凭据）
	conn, err := loadConnectionByID(t.ConnectionId)
	if err != nil {
		return fmt.Errorf("加载 SSH 连接失败: %w", err)
	}
	if conn == nil {
		return fmt.Errorf("SSH 连接不存在")
	}
	if conn.Type != "ssh" {
		return fmt.Errorf("连接「%s」不是 SSH 类型", conn.Name)
	}

	// 构造 tunnel.Config
	cfg := tunnel.Config{
		ID:   t.ID,
		Name: t.Name,
		SSHConfig: exec.SSHConfig{
			Host:           conn.Host,
			Port:           conn.Port,
			Username:       conn.Username,
			AuthMethod:     conn.AuthMethod,
			Password:       conn.Password,
			PrivateKey:     conn.PrivateKey,
			PrivateKeyPath: conn.PrivateKeyPath,
			Passphrase:     conn.Passphrase,
		},
		Type:          t.Type,
		LocalHost:     t.LocalHost,
		LocalPort:     t.LocalPort,
		RemoteHost:    t.RemoteHost,
		RemotePort:    t.RemotePort,
		AutoReconnect: t.AutoReconnect,
	}

	// 根据 type 创建隧道实例（local/remote/dynamic）
	tnl, err := tunnel.NewTunnel(cfg)
	if err != nil {
		return err
	}

	if err := tnl.Start(); err != nil {
		return err
	}

	// 启动期间隧道可能已被并发删除（DB 记录不存在），此时立即停止，防止「复活」
	var exists int
	if err := db.QueryRow("SELECT COUNT(*) FROM tunnels WHERE id = ?", t.ID).Scan(&exists); err == nil && exists == 0 {
		tnl.Stop()
		return fmt.Errorf("隧道「%s」已被删除", t.Name)
	}

	// 二次检查：并发启动同一隧道时，后完成的实例直接释放，避免监听器泄漏
	tr.mu.Lock()
	if _, exists := tr.tunnels[t.ID]; exists {
		tr.mu.Unlock()
		tnl.Stop()
		return fmt.Errorf("隧道「%s」已在运行", t.Name)
	}
	tr.tunnels[t.ID] = tnl
	tr.mu.Unlock()

	return nil
}

// StopTunnel 停止指定隧道
func (tr *TunnelRuntime) StopTunnel(id int) {
	tr.mu.Lock()
	tnl, exists := tr.tunnels[id]
	if exists {
		delete(tr.tunnels, id)
	}
	tr.mu.Unlock()

	if exists {
		tnl.Stop()
	}
}

// GetStatus 获取隧道状态
func (tr *TunnelRuntime) GetStatus(id int) (tunnel.Status, bool) {
	tr.mu.RLock()
	tnl, exists := tr.tunnels[id]
	tr.mu.RUnlock()

	if !exists {
		return tunnel.Status{}, false
	}
	return tnl.Status(), true
}

// GetLogs 获取隧道日志（未运行/不存在时返回空）
func (tr *TunnelRuntime) GetLogs(id int) []tunnel.LogEntry {
	tr.mu.RLock()
	tnl, exists := tr.tunnels[id]
	tr.mu.RUnlock()

	if !exists {
		return nil
	}
	return tnl.Logs()
}

// StartAll 启动所有 auto_start=1 的隧道（应用启动时调用）
// 并发启动，避免 SSH 10s 握手 + 5s 探测超时串行叠加阻塞应用启动
func (tr *TunnelRuntime) StartAll() {
	rows, err := db.Query("SELECT " + tunnelCols + " FROM tunnels WHERE auto_start = 1 ORDER BY sort_order ASC, id ASC")
	if err != nil {
		slog.Error("查询自动启动隧道失败", "error", err)
		return
	}
	defer rows.Close()

	var tunnels []*Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			slog.Error("扫描隧道记录失败", "error", err)
			continue
		}
		tunnels = append(tunnels, t)
	}
	if len(tunnels) == 0 {
		return
	}

	started := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range tunnels {
		wg.Add(1)
		go func(t *Tunnel) {
			defer wg.Done()
			if err := tr.StartTunnel(t); err != nil {
				slog.Warn("自动启动隧道失败", "id", t.ID, "name", t.Name, "error", err)
			} else {
				mu.Lock()
				started++
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	if started > 0 {
		slog.Info("自动启动隧道完成", "started", started)
	}
}

// StopAll 停止所有隧道，返回停止数量
func (tr *TunnelRuntime) StopAll() int {
	tr.mu.Lock()
	ids := make([]int, 0, len(tr.tunnels))
	tnls := make([]tunnel.Tunnel, 0, len(tr.tunnels))
	for id, tnl := range tr.tunnels {
		ids = append(ids, id)
		tnls = append(tnls, tnl)
	}
	for _, id := range ids {
		delete(tr.tunnels, id)
	}
	tr.mu.Unlock()

	for _, tnl := range tnls {
		tnl.Stop()
	}
	return len(tnls)
}

// Shutdown 优雅关闭（应用退出时调用）
func (tr *TunnelRuntime) Shutdown() {
	count := tr.StopAll()
	if count > 0 {
		slog.Info("隧道运行时已关闭", "stopped", count)
	}
}

// ==================== 包级导出函数（供 main.go 调用） ====================

// AutoStartTunnels 自动启动 auto_start=1 的隧道。
// 异步执行：隧道启动可能因 SSH 不可达阻塞数秒，不能阻塞窗口创建；
// 稍作延迟等待应用窗口就绪，隧道状态由前端轮询刷新。
func AutoStartTunnels() {
	go func() {
		time.Sleep(1000 * time.Millisecond)
		tunnelRT.StartAll()
	}()
}

// ShutdownTunnels 关闭所有隧道（应用退出时调用）
func ShutdownTunnels() {
	tunnelRT.Shutdown()
}
