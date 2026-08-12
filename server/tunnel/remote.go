package tunnel

import (
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

// ==================== RemoteTunnel: 远程端口转发 (ssh -R) ====================
// SSH 服务器在远程监听 remoteHost:remotePort -> 转发到本地 localHost:localPort

type RemoteTunnel struct {
	baseTunnel
}

func NewRemoteTunnel(cfg Config) *RemoteTunnel {
	return &RemoteTunnel{baseTunnel: baseTunnel{cfg: cfg}}
}

func (t *RemoteTunnel) Start() error {
	err := t.baseTunnel.start(func(client *ssh.Client) (net.Listener, error) {
		remoteAddr := net.JoinHostPort(t.cfg.RemoteHost, strconv.Itoa(t.cfg.RemotePort))
		l, err := client.Listen("tcp", remoteAddr)
		if err != nil {
			return nil, fmt.Errorf("远程监听失败 %s: %w", remoteAddr, err)
		}
		t.logf("info", "隧道已启动：SSH 远程监听 %s，转发到本地 %s:%d", remoteAddr, t.cfg.LocalHost, t.cfg.LocalPort)
		slog.Info("隧道已启动", "name", t.cfg.Name, "type", "remote", "remote-listen", remoteAddr,
			"local", fmt.Sprintf("%s:%d", t.cfg.LocalHost, t.cfg.LocalPort))
		return l, nil
	})
	if err != nil {
		return err
	}

	// 启动前探测本地目标（TCP 连通性，5s 超时）。不可达 → 直接启动失败
	if err := t.probeLocal(); err != nil {
		t.baseTunnel.stop()
		msg := "本地目标 " + net.JoinHostPort(t.cfg.LocalHost, strconv.Itoa(t.cfg.LocalPort)) +
			" 不可达（" + probeErrorHint(err) + "），隧道未启动"
		t.logf("error", "启动失败：%s", msg)
		return fmt.Errorf("%s", msg)
	}

	go t.acceptLoop()
	if t.cfg.AutoReconnect {
		go t.keepaliveLoop(t.reconnect)
	}
	return nil
}

// probeLocal 探测本地目标（TCP 连通性，5s 超时）
func (t *RemoteTunnel) probeLocal() error {
	localAddr := net.JoinHostPort(t.cfg.LocalHost, strconv.Itoa(t.cfg.LocalPort))
	conn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func (t *RemoteTunnel) Stop() error {
	t.baseTunnel.stop()
	t.logf("info", "隧道已停止")
	slog.Info("隧道已停止", "name", t.cfg.Name)
	return nil
}

func (t *RemoteTunnel) Status() Status { return t.baseTunnel.status() }

func (t *RemoteTunnel) Logs() []LogEntry { return t.baseTunnel.Logs() }

func (t *RemoteTunnel) acceptLoop() {
	for {
		remoteConn, err := t.listener.Accept()
		if err != nil {
			t.mu.Lock()
			if !t.running {
				t.mu.Unlock()
				return
			}
			t.mu.Unlock()
			slog.Warn("隧道远程监听异常", "name", t.cfg.Name, "error", err)
			return
		}
		t.active.Add(1)
		go t.forward(remoteConn)
	}
}

func (t *RemoteTunnel) forward(remoteConn net.Conn) {
	defer t.active.Done()
	defer remoteConn.Close()

	t.mu.Lock()
	t.connCount++
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.connCount--
		t.mu.Unlock()
	}()

	localAddr := net.JoinHostPort(t.cfg.LocalHost, strconv.Itoa(t.cfg.LocalPort))
	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		slog.Warn("隧道本地拨号失败", "name", t.cfg.Name, "local", localAddr, "error", err)
		t.logf("warn", "本地转发失败 %s：%v", localAddr, err)
		return
	}
	defer localConn.Close()
	t.logf("info", "连接建立：%s（远程监听）-> %s", remoteConn.RemoteAddr(), localAddr)

	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(localConn, remoteConn)
		atomic.AddInt64(&t.bytesIn, n)
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(remoteConn, localConn)
		atomic.AddInt64(&t.bytesOut, n)
		done <- struct{}{}
	}()
	<-done
	t.logf("info", "连接关闭：%s", localAddr)
}

func (t *RemoteTunnel) reconnect() error {
	client, err := exec.DialSSH(t.cfg.SSHConfig)
	if err != nil {
		return err
	}
	remoteAddr := net.JoinHostPort(t.cfg.RemoteHost, strconv.Itoa(t.cfg.RemotePort))
	listener, err := client.Listen("tcp", remoteAddr)
	if err != nil {
		client.Close()
		return fmt.Errorf("远程监听失败 %s: %w", remoteAddr, err)
	}
	t.mu.Lock()
	// 重连期间隧道可能已被停止/删除，此时禁止复活
	if !t.running {
		t.mu.Unlock()
		client.Close() // 同时关闭远程 listener
		return fmt.Errorf("隧道已停止")
	}
	t.sshClient = client
	t.listener = listener
	t.mu.Unlock()
	go t.acceptLoop()
	return nil
}

