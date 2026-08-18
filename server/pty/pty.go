package pty

// Transport 交互式终端传输层（跨平台统一接口）
// 实现方：Windows ConPTY（conptyTransport）、Unix creack/pty（unixPTYTransport）、
// SSH 远程 PTY（exec 包 sshTransport）。
// 提供 Read/Write 双向字节流、Resize 调整终端尺寸、Kill 终止进程、Wait 等待退出、Close 释放资源。
type Transport interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	// Resize 调整伪终端尺寸（列/行），供前端 xterm 自适应同步
	Resize(cols, rows int) error
	Close() error
	Kill() error
	Wait() error
}
