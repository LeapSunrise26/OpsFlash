package daemon

// ==================== 守护进程运行记录接口 ====================
// 两种实现：
//   - LocalRecord（daemon_windows.go / daemon_unix.go）：本机 Job Object / 进程组管理
//   - sshDaemon（exec/exec_ssh.go）：SSH 远程 nohup 启动 + kill -0 查活 + kill 停止
// opsruntime 只依赖本接口，屏蔽「守护进程跑在哪」的差异。

// Record 守护进程运行记录接口
type Record interface {
	// PID 返回进程 PID（本地=本机 PID；SSH=远程 PID）
	PID() int
	// Running 进程是否仍在运行
	Running() bool
	// StderrText 返回启动后捕获的错误输出
	StderrText() string
	// Terminate 终止进程（本地=进程树；SSH=远程 kill）
	Terminate() error
	// Cleanup 进程退出后释放资源（本地=Job 句柄；SSH=关闭连接）
	Cleanup()
	// Wait 等待进程退出
	Wait() error
}
