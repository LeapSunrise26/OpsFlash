package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync/atomic"

	"golang.org/x/crypto/ssh"

	"opsflash/server/exec"
)

// ==================== DynamicTunnel: 动态端口转发 (ssh -D, SOCKS5) ====================
// 本地监听 localHost:localPort 作为 SOCKS5 代理 -> 通过 SSH 动态转发到任意目标

type DynamicTunnel struct {
	baseTunnel
}

func NewDynamicTunnel(cfg Config) *DynamicTunnel {
	return &DynamicTunnel{baseTunnel: baseTunnel{cfg: cfg}}
}

func (t *DynamicTunnel) Start() error {
	err := t.baseTunnel.start(func(client *ssh.Client) (net.Listener, error) {
		addr := net.JoinHostPort(t.cfg.LocalHost, strconv.Itoa(t.cfg.LocalPort))
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("本地监听失败 %s: %w", addr, err)
		}
		slog.Info("隧道已启动", "name", t.cfg.Name, "type", "dynamic(SOCKS5)", "listen", addr)
		t.logf("info", "SOCKS5 代理已启动：监听 %s", addr)
		return l, nil
	})
	if err != nil {
		return err
	}
	go t.acceptLoop()
	if t.cfg.AutoReconnect {
		go t.keepaliveLoop(t.reconnect)
	}
	return nil
}

func (t *DynamicTunnel) Stop() error {
	t.baseTunnel.stop()
	t.logf("info", "隧道已停止")
	slog.Info("隧道已停止", "name", t.cfg.Name)
	return nil
}

func (t *DynamicTunnel) Status() Status { return t.baseTunnel.status() }

func (t *DynamicTunnel) Logs() []LogEntry { return t.baseTunnel.Logs() }

func (t *DynamicTunnel) acceptLoop() {
	for {
		clientConn, err := t.listener.Accept()
		if err != nil {
			t.mu.Lock()
			if !t.running {
				t.mu.Unlock()
				return
			}
			t.mu.Unlock()
			slog.Warn("隧道监听异常", "name", t.cfg.Name, "error", err)
			return
		}
		t.active.Add(1)
		go t.handleSOCKS5(clientConn)
	}
}

func (t *DynamicTunnel) reconnect() error {
	client, err := exec.DialSSH(t.cfg.SSHConfig)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(t.cfg.LocalHost, strconv.Itoa(t.cfg.LocalPort))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		client.Close()
		return fmt.Errorf("本地监听失败 %s: %w", addr, err)
	}
	t.mu.Lock()
	// 重连期间隧道可能已被停止/删除，此时禁止复活
	if !t.running {
		t.mu.Unlock()
		client.Close()
		listener.Close()
		return fmt.Errorf("隧道已停止")
	}
	t.sshClient = client
	t.listener = listener
	t.mu.Unlock()
	go t.acceptLoop()
	return nil
}

// handleSOCKS5 处理 SOCKS5 代理请求
func (t *DynamicTunnel) handleSOCKS5(clientConn net.Conn) {
	defer t.active.Done()
	defer clientConn.Close()

	t.mu.Lock()
	t.connCount++
	sshClient := t.sshClient
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.connCount--
		t.mu.Unlock()
	}()

	if sshClient == nil {
		return
	}

	buf := make([]byte, 258)

	// 1. SOCKS5 握手：客户端发送 [VER, NMETHODS, METHODS...]
	n, err := clientConn.Read(buf)
	if err != nil || n < 2 || buf[0] != 0x05 {
		return
	}
	// 响应：选择「无认证」(0x00)
	if _, err = clientConn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// 2. 客户端发送请求: [VER, CMD, RSV, ATYP, ADDR..., PORT(2)]
	n, err = clientConn.Read(buf)
	if err != nil || n < 7 || buf[0] != 0x05 {
		return
	}
	if buf[1] != 0x01 { // 仅支持 CONNECT
		clientConn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	var targetAddr string
	var port uint16

	switch buf[3] { // ATYP
	case 0x01: // IPv4
		if n < 10 {
			return
		}
		targetAddr = net.IP(buf[4:8]).String()
		port = binary.BigEndian.Uint16(buf[8:10])
	case 0x03: // Domain
		if n < 5 {
			return
		}
		domainLen := int(buf[4])
		if n < 5+domainLen+2 {
			return
		}
		targetAddr = string(buf[5 : 5+domainLen])
		port = binary.BigEndian.Uint16(buf[5+domainLen : 7+domainLen])
	case 0x04: // IPv6
		if n < 22 {
			return
		}
		targetAddr = net.IP(buf[4:20]).String()
		port = binary.BigEndian.Uint16(buf[20:22])
	default:
		return
	}

	target := net.JoinHostPort(targetAddr, strconv.Itoa(int(port)))

	// 通过 SSH 拨号到目标
	remoteConn, err := sshClient.Dial("tcp", target)
	if err != nil {
		slog.Warn("SOCKS5 远程拨号失败", "name", t.cfg.Name, "target", target, "error", err)
		t.logf("warn", "SOCKS5 转发失败 %s：%v", target, err)
		clientConn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // HOST_UNREACHABLE
		return
	}
	defer remoteConn.Close()
	t.logf("info", "SOCKS5 连接建立：%s -> %s", clientConn.RemoteAddr(), target)

	// 响应成功
	if _, err = clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	// 双向转发
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(remoteConn, clientConn)
		atomic.AddInt64(&t.bytesIn, n)
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(clientConn, remoteConn)
		atomic.AddInt64(&t.bytesOut, n)
		done <- struct{}{}
	}()
	<-done
	t.logf("info", "SOCKS5 连接关闭：%s", target)
}
