package exec

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// ==================== SSH 拨号与连接测试 ====================
// 第一阶段（连接管理 + 隧道）所需的最小 SSH 能力：
//   - DialSSH：建立 SSH 连接（供 tunnel 包端口转发复用）
//   - TestSSH：连接测试（握手 + echo，返回往返延迟毫秒）

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

// DialSSH 导出的 SSH 拨号函数，供 tunnel 等包复用
func DialSSH(cfg SSHConfig) (*ssh.Client, error) {
	ex := &sshExecutor{cfg: cfg}
	return ex.dial()
}

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
