package exec

import (
	"context"
	"errors"

	"opsflash/server/daemon"
	"opsflash/server/pty"
)

// ==================== 执行器（Executor）抽象 ====================
// 屏蔽「命令在哪里执行」的差异：terminal（本机）/ ssh（远程主机）/ db（数据库，后续迭代）。
// 执行方式（type: non-interactive / interactive / daemon）的分发逻辑在 server 包保持不变，
// 只在此处根据 mode 选择合适的执行器。

// Executor 统一执行入口
type Executor interface {
	// Run 非交互式执行：同步返回 stdout+stderr 合并输出
	Run(ctx context.Context, cmdText string) (string, error)

	// StartInteractive 交互式启动：返回符合 pty.Transport 接口的传输层
	StartInteractive(cmdText string) (pty.Transport, error)

	// StartDaemon 守护进程启动（terminal: 本机 Job Object/进程组；ssh: 远程 nohup+kill；db: 不支持）
	StartDaemon(cmdText string) (daemon.Record, error)
}

// ErrDaemonUnsupported 数据库等模式暂不支持守护进程
var ErrDaemonUnsupported = errors.New("该执行模式暂不支持守护进程类型，请改用交互式模式或使用本地（terminal）模式")
