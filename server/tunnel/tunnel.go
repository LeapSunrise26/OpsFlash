package tunnel

import (
	"fmt"

	"opsflash/server/exec"
)

// ==================== SSH 隧道（端口转发） ====================
// 基于 golang.org/x/crypto/ssh 的纯 Go 端口转发实现。
//   - LocalTunnel:   本地端口转发 (ssh -L)，最常用
//   - RemoteTunnel:  远程端口转发 (ssh -R)，内网穿透
//   - DynamicTunnel: 动态端口转发 (ssh -D, SOCKS5 代理)

// LogEntry 隧道日志条目（环形缓冲，供日志面板展示）
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"` // info | warn | error
	Message string `json:"message"`
}

// maxTunnelLogs 每隧道保留的最大日志条数
const maxTunnelLogs = 200

// Status 隧道运行状态
type Status struct {
	Running      bool   `json:"running"`
	StartedAt    string `json:"startedAt"`
	Error        string `json:"error,omitempty"`
	Connections  int    `json:"connections"`  // 当前活跃转发连接数
	Reconnecting bool   `json:"reconnecting"` // 是否正在重连
	BytesIn      int64  `json:"bytesIn"`      // 累计接收字节（客户端→远程）
	BytesOut     int64  `json:"bytesOut"`     // 累计发送字节（远程→客户端）
}

// Config 隧道配置（由 server 包从 DB 构造，凭据已解密）
type Config struct {
	ID            int
	Name          string
	SSHConfig     exec.SSHConfig
	Type          string // local | remote | dynamic
	LocalHost     string
	LocalPort     int
	RemoteHost    string
	RemotePort    int
	AutoReconnect bool
}

// Tunnel 隧道抽象接口
type Tunnel interface {
	Start() error
	Stop() error
	Status() Status
	Logs() []LogEntry
}

// NewTunnel 根据配置创建隧道实例
func NewTunnel(cfg Config) (Tunnel, error) {
	switch cfg.Type {
	case "local":
		return NewLocalTunnel(cfg), nil
	case "remote":
		return NewRemoteTunnel(cfg), nil
	case "dynamic":
		return NewDynamicTunnel(cfg), nil
	default:
		return nil, fmt.Errorf("不支持的隧道类型: %s", cfg.Type)
	}
}

