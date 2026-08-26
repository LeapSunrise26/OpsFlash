package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"opsflash/server/daemon"
	"opsflash/server/pty"
)

// ==================== sshExecutor：SSH 远程执行器 ====================
// 基于 golang.org/x/crypto/ssh（纯 Go，无 CGO）。
//   - 非交互：CombinedOutput（stdout+stderr 合并，60s 超时由调用方 ctx 控制）
//   - 交互式：远程请求 PTY（xterm），stdin/stdout 双向管道，实现 pty.Transport
//     （前端 300ms 轮询逻辑零改动）
//   - 守护进程：暂不支持（ErrDaemonUnsupported）

// SSHConfig SSH 连接配置（凭据已解密，由 server 包从 Connection 构造）
type SSHConfig struct {
	Host           string
	Port           int
	Username       string
	AuthMethod     string // password | key
	Password       string
	PrivateKey     string // PEM 内容
	PrivateKeyPath string
	Passphrase     string
}

type sshExecutor struct {
	cfg SSHConfig
}

// NewSSHExecutor 创建 SSH 执行器
func NewSSHExecutor(cfg SSHConfig) Executor {
	return &sshExecutor{cfg: cfg}
}

// loadSigner 按优先级加载私钥：PEM 内容 > 文件路径 > 默认 ~/.ssh
func (e *sshExecutor) loadSigner() ssh.Signer {
	var pemData []byte
	switch {
	case e.cfg.PrivateKey != "":
		pemData = []byte(e.cfg.PrivateKey)
	case e.cfg.PrivateKeyPath != "":
		data, err := os.ReadFile(e.cfg.PrivateKeyPath)
		if err != nil {
			slog.Warn("读取私钥文件失败", "path", e.cfg.PrivateKeyPath, "error", err)
			return nil
		}
		pemData = data
	default:
		if home, err := os.UserHomeDir(); err == nil {
			for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
				if data, err := os.ReadFile(filepath.Join(home, ".ssh", name)); err == nil {
					pemData = data
					break
				}
			}
		}
	}
	if len(pemData) == 0 {
		return nil
	}
	if e.cfg.Passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(pemData, []byte(e.cfg.Passphrase))
		if err != nil {
			slog.Warn("解析加密私钥失败", "error", err)
			return nil
		}
		return signer
	}
	signer, err := ssh.ParsePrivateKey(pemData)
	if err != nil {
		slog.Warn("解析私钥失败", "error", err)
		return nil
	}
	return signer
}

// authMethods 构建认证方式列表（密钥优先，密码兜底）
func (e *sshExecutor) authMethods() []ssh.AuthMethod {
	var methods []ssh.AuthMethod
	if e.cfg.AuthMethod == "key" {
		if signer := e.loadSigner(); signer != nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}
	if e.cfg.Password != "" {
		methods = append(methods, ssh.Password(e.cfg.Password))
	}
	return methods
}

// dial 建立 SSH 连接（握手超时 10s）
// known_hosts：默认 InsecureIgnoreHostKey（宽松模式，风险已在设计文档明示）
func (e *sshExecutor) dial() (*ssh.Client, error) {
	if e.cfg.Host == "" {
		return nil, errors.New("主机地址为空")
	}
	if e.cfg.Port <= 0 {
		e.cfg.Port = 22
	}
	methods := e.authMethods()
	if len(methods) == 0 {
		return nil, errors.New("未配置有效的认证方式（密码或密钥）")
	}

	addr := net.JoinHostPort(e.cfg.Host, strconv.Itoa(e.cfg.Port))
	config := &ssh.ClientConfig{
		User:            e.cfg.Username,
		Auth:            methods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	return ssh.Dial("tcp", addr, config)
}

// Run 非交互式执行：远程执行命令并返回合并输出
func (e *sshExecutor) Run(ctx context.Context, cmdText string) (string, error) {
	client, err := e.dial()
	if err != nil {
		return "", fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer session.Close()

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := session.CombinedOutput(cmdText)
		ch <- result{out, err}
	}()

	select {
	case r := <-ch:
		return DecodeOutput(r.out), r.err
	case <-ctx.Done():
		session.Close() // 中断远程命令
		<-ch
		return "", ctx.Err()
	}
}

// StartInteractive 交互式执行：远程分配 PTY，返回 sshTransport（实现 pty.Transport）
func (e *sshExecutor) StartInteractive(cmdText string) (pty.Transport, error) {
	client, err := e.dial()
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("创建 SSH 会话失败: %w", err)
	}

	// 请求远程 PTY（120x30），sudo / 远程交互程序依赖 TTY
	if err := session.RequestPty("xterm", 120, 30, ssh.TerminalModes{}); err != nil {
		session.Close()
		client.Close()
		return nil, fmt.Errorf("请求远程 PTY 失败: %w", err)
	}

	// stdout 与 stderr 合并到同一输出管道（与本地 PTY 行为一致）
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	session.Stdin = stdinR
	session.Stdout = stdoutW
	session.Stderr = stdoutW

	if err := session.Start(cmdText); err != nil {
		session.Close()
		client.Close()
		return nil, fmt.Errorf("启动远程命令失败: %w", err)
	}

	slog.Info("SSH 交互式会话已建立", "host", e.cfg.Host, "port", e.cfg.Port, "user", e.cfg.Username)
	return &sshTransport{
		client:  client,
		session: session,
		stdin:   stdinW,
		stdout:  stdoutR,
	}, nil
}

// StartDaemon SSH 远程守护进程暂不支持
func (e *sshExecutor) StartDaemon(cmdText string) (*daemon.Record, error) {
	return nil, ErrDaemonUnsupported
}

// ==================== sshTransport：SSH 交互传输层 ====================
// 实现 pty.Transport 接口（Read/Write/Close/Kill/Wait），
// 使前端 300ms 轮询、输出归一化等现有流程完全复用。

type sshTransport struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	mu      sync.Mutex
	closed  bool
}

func (t *sshTransport) Read(p []byte) (int, error) {
	return t.stdout.Read(p)
}

func (t *sshTransport) Write(p []byte) (int, error) {
	return t.stdin.Write(p)
}

// Resize 同步远程终端尺寸（SSH WindowChange）
func (t *sshTransport) Resize(cols, rows int) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed || t.session == nil {
		return fmt.Errorf("SSH 会话已关闭")
	}
	return t.session.WindowChange(rows, cols)
}

// Close 关闭会话与连接（幂等）
func (t *sshTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	t.stdin.Close()
	_ = t.session.Close()
	return t.client.Close()
}

// Kill 发送 SIGKILL 并关闭连接
func (t *sshTransport) Kill() error {
	if t.session != nil {
		_ = t.session.Signal(ssh.SIGKILL)
	}
	return t.Close()
}

// Wait 等待远程命令退出
func (t *sshTransport) Wait() error {
	return t.session.Wait()
}

// DialSSH 导出的 SSH 拨号函数，供 tunnel 等包复用
func DialSSH(cfg SSHConfig) (*ssh.Client, error) {
	ex := &sshExecutor{cfg: cfg}
	return ex.dial()
}

// ==================== 连接测试 ====================

// TestSSH 测试 SSH 连接：握手 + 执行 echo，返回往返延迟（毫秒）
func TestSSH(host string, port int, username, authMethod, password, privateKey, privateKeyPath, passphrase string) (int64, error) {
	ex := &sshExecutor{
		cfg: SSHConfig{
			Host: host, Port: port, Username: username, AuthMethod: authMethod,
			Password: password, PrivateKey: privateKey, PrivateKeyPath: privateKeyPath, Passphrase: passphrase,
		},
	}
	start := time.Now()
	client, err := ex.dial()
	if err != nil {
		return 0, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return 0, err
	}
	defer session.Close()
	if _, err := session.CombinedOutput("echo ok"); err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}
