package tunnel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"opsflash/server/exec"
)

// ==================== LocalTunnel: 本地端口转发 (ssh -L) ====================
// 本地监听 localHost:localPort -> 通过 SSH 隧道拨号到 remoteHost:remotePort

type LocalTunnel struct {
	baseTunnel
}

func NewLocalTunnel(cfg Config) *LocalTunnel {
	return &LocalTunnel{baseTunnel: baseTunnel{cfg: cfg}}
}

func (t *LocalTunnel) Start() error {
	err := t.baseTunnel.start(func(client *ssh.Client) (net.Listener, error) {
		addr := net.JoinHostPort(t.cfg.LocalHost, strconv.Itoa(t.cfg.LocalPort))
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("本地监听失败 %s: %w", addr, err)
		}
		t.logf("info", "隧道已启动：监听 %s，转发到 %s:%d", addr, t.cfg.RemoteHost, t.cfg.RemotePort)
		slog.Info("隧道已启动", "name", t.cfg.Name, "type", "local", "listen", addr,
			"remote", fmt.Sprintf("%s:%d", t.cfg.RemoteHost, t.cfg.RemotePort))
		return l, nil
	})
	if err != nil {
		return err
	}

	// 启动前探测远程目标（TCP 连通性，5s 超时）。
	// SSH 通但映射端口不通 → 直接启动失败，不启动后再提示
	if err := t.probeRemote(); err != nil {
		t.baseTunnel.stop()
		msg := "远程目标 " + net.JoinHostPort(t.cfg.RemoteHost, strconv.Itoa(t.cfg.RemotePort)) +
			" 不可达（" + probeErrorHint(err) + "），隧道未启动。远程地址是相对 SSH 服务器的，访问内网其他机器请填其内网 IP"
		t.logf("error", "启动失败：%s", msg)
		return fmt.Errorf("%s", msg)
	}

	go t.acceptLoop()
	if t.cfg.AutoReconnect {
		go t.keepaliveLoop(t.reconnect)
	}
	return nil
}

// probeRemote 通过 SSH 探测远程目标（TCP 连通性，5s 超时）
func (t *LocalTunnel) probeRemote() error {
	t.mu.Lock()
	client := t.sshClient
	t.mu.Unlock()
	if client == nil {
		return fmt.Errorf("SSH 连接未建立")
	}
	remoteAddr := net.JoinHostPort(t.cfg.RemoteHost, strconv.Itoa(t.cfg.RemotePort))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "tcp", remoteAddr)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// probeErrorHint 将探测错误转成人话提示（区分转发被禁/端口未监听/超时）
func (t *LocalTunnel) Stop() error {
	t.baseTunnel.stop()
	t.logf("info", "隧道已停止")
	slog.Info("隧道已停止", "name", t.cfg.Name)
	return nil
}

func (t *LocalTunnel) Status() Status { return t.baseTunnel.status() }

func (t *LocalTunnel) Logs() []LogEntry { return t.baseTunnel.Logs() }

func (t *LocalTunnel) acceptLoop() {
	for {
		localConn, err := t.listener.Accept()
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
		go t.forward(localConn)
	}
}

func (t *LocalTunnel) forward(localConn net.Conn) {
	defer t.active.Done()
	defer localConn.Close()

	t.mu.Lock()
	client := t.sshClient
	t.connCount++
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.connCount--
		t.mu.Unlock()
	}()

	if client == nil {
		t.logf("warn", "SSH 连接已断开，拒绝转发")
		return
	}

	remoteAddr := net.JoinHostPort(t.cfg.RemoteHost, strconv.Itoa(t.cfg.RemotePort))
	remoteConn, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		slog.Warn("隧道远程拨号失败", "name", t.cfg.Name, "remote", remoteAddr, "error", err)
		t.logf("warn", "转发失败 %s：%v", remoteAddr, err)
		return
	}
	defer remoteConn.Close()
	t.logf("info", "连接建立：%s -> %s", localConn.RemoteAddr(), remoteAddr)

	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(remoteConn, localConn)
		atomic.AddInt64(&t.bytesIn, n)
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(localConn, remoteConn)
		atomic.AddInt64(&t.bytesOut, n)
		done <- struct{}{}
	}()
	<-done
	t.logf("info", "连接关闭：%s", remoteAddr)
}

func (t *LocalTunnel) reconnect() error {
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

