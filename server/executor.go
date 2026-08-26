package server

import (
	"errors"
	"fmt"

	"opsflash/server/exec"
)

// ==================== 执行器（Executor）工厂 ====================
// 屏蔽「命令在哪里执行」的差异：terminal（本机）/ ssh（远程主机）。
// 执行方式（type: non-interactive / interactive / daemon）的分发逻辑在 opsruntime.go 保持不变，
// 只在此处根据 mode 选择合适的执行器（具体实现见 server/exec 子包）。

// executorFor 根据执行位置（mode）与连接信息创建执行器
// interpreter（cmd/powershell/bash）仅对本地 terminal 模式生效，其他模式忽略
func executorFor(mode string, conn *Connection, interpreter string) (exec.Executor, error) {
	switch mode {
	case "", "terminal":
		return exec.NewTerminalExecutor(interpreter), nil
	case "ssh":
		if conn == nil {
			return nil, errors.New("SSH 命令缺少连接配置，请先在「连接管理」中创建 SSH 连接")
		}
		return exec.NewSSHExecutor(exec.SSHConfig{
			Host:           conn.Host,
			Port:           conn.Port,
			Username:       conn.Username,
			AuthMethod:     conn.AuthMethod,
			Password:       conn.Password,
			PrivateKey:     conn.PrivateKey,
			PrivateKeyPath: conn.PrivateKeyPath,
			Passphrase:     conn.Passphrase,
		}), nil
	default:
		return nil, fmt.Errorf("执行模式 %q 尚未支持", mode)
	}
}

// validCommandModes 合法的执行位置（v0.3.0：本地脚本 terminal + ssh，不含 redis/mysql/tdengine）
var validCommandModes = map[string]bool{
	"terminal": true,
	"ssh":      true,
}
