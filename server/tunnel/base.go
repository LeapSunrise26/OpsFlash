package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"opsflash/server/exec"
)

// ==================== baseTunnel: 公共逻辑（SSH 连接管理 + 断线重连 + keepalive + 状态追踪） ====================

type baseTunnel struct {
	cfg Config

	sshClient *ssh.Client
	listener  net.Listener // 由 start 的 createListener 回调设置

	mu           sync.Mutex
	running      bool
	stopCh       chan struct{}
	active       sync.WaitGroup // 跟踪活跃的转发连接
	connCount    int            // 当前活跃连接数（用于状态展示）
	startedAt    time.Time
	lastErr      string
	reconnecting bool
	reconnCh     chan struct{} // 重连信号通道

	// 日志缓冲（环形，日志面板展示）
	logMu sync.Mutex
	logs  []LogEntry

	// 流量统计（累计字节）
	bytesIn  int64 // 客户端 → 远程
	bytesOut int64 // 远程 → 客户端
}

// start 公共启动：SSH 拨号 + createListener 回调创建监听器
func (b *baseTunnel) start(createListener func(client *ssh.Client) (net.Listener, error)) error {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return fmt.Errorf("隧道「%s」已在运行", b.cfg.Name)
	}
	b.mu.Unlock()

	client, err := exec.DialSSH(b.cfg.SSHConfig)
	if err != nil {
		return fmt.Errorf("SSH 连接失败: %w", err)
	}

	listener, err := createListener(client)
	if err != nil {
		client.Close()
		return err
	}

	b.mu.Lock()
	b.sshClient = client
	b.listener = listener
	b.running = true
	b.stopCh = make(chan struct{})
	b.reconnCh = make(chan struct{}, 1)
	b.startedAt = time.Now()
	b.lastErr = ""
	b.mu.Unlock()

	return nil
}

// stop 公共停止（幂等，等待活跃连接最多 3s）
func (b *baseTunnel) stop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	b.running = false
	close(b.stopCh)
	listener := b.listener
	client := b.sshClient
	b.listener = nil
	b.sshClient = nil
	b.mu.Unlock()

	if listener != nil {
		listener.Close()
	}
	if client != nil {
		client.Close()
	}

	done := make(chan struct{})
	go func() {
		b.active.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

// status 公共状态
func (b *baseTunnel) status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{
		Running:      b.running,
		StartedAt:    b.startedAt.Format(time.RFC3339),
		Error:        b.lastErr,
		Connections:  b.connCount,
		Reconnecting: b.reconnecting,
		BytesIn:      atomic.LoadInt64(&b.bytesIn),
		BytesOut:     atomic.LoadInt64(&b.bytesOut),
	}
}

// logf 追加一条隧道日志（环形缓冲）
func (b *baseTunnel) logf(level, format string, args ...interface{}) {
	entry := LogEntry{
		Time:    time.Now().Format("15:04:05"),
		Level:   level,
		Message: fmt.Sprintf(format, args...),
	}
	b.logMu.Lock()
	defer b.logMu.Unlock()
	b.logs = append(b.logs, entry)
	if len(b.logs) > maxTunnelLogs {
		b.logs = b.logs[len(b.logs)-maxTunnelLogs:]
	}
}

// Logs 返回隧道日志快照（最新在前）
func (b *baseTunnel) Logs() []LogEntry {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	out := make([]LogEntry, len(b.logs))
	copy(out, b.logs)
	// 反转：最新在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// keepaliveLoop 心跳保活 + 断线重连（指数退避）
// reconnect 回调由子类提供，负责重新建立 SSH 连接 + 监听器 + accept 循环
func (b *baseTunnel) keepaliveLoop(reconnect func() error) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.mu.Lock()
			client := b.sshClient
			running := b.running
			b.mu.Unlock()
			if !running || client == nil {
				continue
			}
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				slog.Warn("隧道 SSH 连接异常，触发重连", "name", b.cfg.Name, "error", err)
				b.logf("warn", "SSH 心跳失败：%v，触发重连", err)
				b.tryReconnect(reconnect)
			}
		case <-b.reconnCh:
			b.tryReconnect(reconnect)
		}
	}
}

// tryReconnect 指数退避重连（1s -> 2s -> 4s -> 8s -> max 30s）
func (b *baseTunnel) tryReconnect(reconnect func() error) {
	b.mu.Lock()
	if !b.running || b.reconnecting {
		b.mu.Unlock()
		return
	}
	b.reconnecting = true
	b.lastErr = "重连中..."
	oldClient := b.sshClient
	oldListener := b.listener
	b.sshClient = nil
	b.listener = nil
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.reconnecting = false
		b.mu.Unlock()
	}()

	if oldClient != nil {
		oldClient.Close()
	}
	if oldListener != nil {
		oldListener.Close()
	}

	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		b.mu.Lock()
		running := b.running
		b.mu.Unlock()
		if !running {
			return
		}

		slog.Info("隧道重连中", "name", b.cfg.Name, "backoff", backoff)
		b.logf("warn", "重连尝试中（退避 %v）", backoff)

		if err := reconnect(); err != nil {
			slog.Warn("隧道重连失败", "name", b.cfg.Name, "error", err, "backoff", backoff)
			b.mu.Lock()
			b.lastErr = fmt.Sprintf("重连失败: %v (下次重试: %v)", err, backoff)
			b.mu.Unlock()
			b.logf("error", "重连失败：%v", err)

			select {
			case <-b.stopCh:
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		b.mu.Lock()
		b.lastErr = ""
		b.startedAt = time.Now()
		b.mu.Unlock()

		slog.Info("隧道重连成功", "name", b.cfg.Name)
		b.logf("info", "重连成功")
		return
	}
}

func probeErrorHint(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "forwarding") || strings.Contains(lower, "not enabled"):
		return "SSH 服务器禁止端口转发（AllowTcpForwarding=no）"
	case strings.Contains(lower, "refused"):
		return "远程目标端口未监听（connection refused）"
	case strings.Contains(lower, "timeout") || err == context.DeadlineExceeded:
		return "远程目标无响应（超时 5s）"
	default:
		return msg
	}
}

