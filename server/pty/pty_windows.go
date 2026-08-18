//go:build windows

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"opsflash/server/cmd"
)

// ==================== Windows 伪终端（ConPTY）实现 ====================
// 基于 Windows Pseudo Console API（Windows 10 1809+）。
// 通过 CreatePseudoConsole + STARTUPINFOEX(PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE)
// 让子进程运行在一个真实的控制台中，从而支持 sudo / ssh 等需要 TTY 的密码交互。

const (
	procThreadAttributePseudoConsole uintptr = 0x00020016
)

var (
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procCreatePseudoConsole    = kernel32.NewProc("CreatePseudoConsole")
	procClosePseudoConsole     = kernel32.NewProc("ClosePseudoConsole")
	procResizePseudoConsole    = kernel32.NewProc("ResizePseudoConsole")
	procCreatePipe             = kernel32.NewProc("CreatePipe")
	procInitProcThreadAttrList = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttr   = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttr   = kernel32.NewProc("DeleteProcThreadAttributeList")
)

type coord struct {
	x int16
	y int16
}

// procThreadAttributeList 变长结构体的头部（其后为属性数组，由 Initialize 填充）
type procThreadAttributeList struct {
	dwFlags uint32
	size    uint32
}

// startupInfoEx 与 Windows STARTUPINFOEX 布局一致：STARTUPINFO + 属性列表指针
type startupInfoEx struct {
	startupInfo   windows.StartupInfo
	attributeList *procThreadAttributeList
}

// conptyTransport 伪终端传输层
type conptyTransport struct {
	hPC       syscall.Handle // 伪控制台句柄
	inFile    *os.File       // 我们写输入
	outFile   *os.File       // 我们读输出
	hProc     syscall.Handle // 子进程句柄
	closeOnce sync.Once
}

func (t *conptyTransport) Read(p []byte) (int, error)  { return t.outFile.Read(p) }
func (t *conptyTransport) Write(p []byte) (int, error) { return t.inFile.Write(p) }

// Resize 调整伪控制台尺寸（ResizePseudoConsole，COORD 按值传参）
func (t *conptyTransport) Resize(cols, rows int) error {
	if t.hPC == 0 {
		return fmt.Errorf("伪控制台已关闭")
	}
	size := coord{x: int16(cols), y: int16(rows)}
	// COORD 是 4 字节结构，x64 上按值传入单个寄存器，必须整体转成 int32 传值（与 CreatePseudoConsole 一致）
	r1, _, e1 := procResizePseudoConsole.Call(
		uintptr(*(*int32)(unsafe.Pointer(&size))),
		uintptr(t.hPC))
	if int32(r1) < 0 { // ResizePseudoConsole 返回 HRESULT，<0 表示失败
		return fmt.Errorf("ResizePseudoConsole 失败: HRESULT=0x%x, %v", uint32(r1), e1)
	}
	return nil
}

// Kill 终止子进程及其进程树（taskkill /T /F 覆盖 cmd → 子进程链，如 pnpm → node）
func (t *conptyTransport) Kill() error {
	if t.hProc != 0 {
		if pid, err := windows.GetProcessId(windows.Handle(t.hProc)); err == nil && pid > 0 {
			// 先尝试终止进程树（子进程可能继续占用端口/资源），失败时兜底 TerminateProcess
			// HideWindow：GUI 应用里 exec 启动控制台程序会弹出原生终端窗口，必须隐藏
			tk := exec.Command("taskkill", "/PID", strconv.FormatUint(uint64(pid), 10), "/T", "/F")
			tk.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			tk.Run()
		}
	}
	return windows.TerminateProcess(windows.Handle(t.hProc), 1)
}

// Wait 等待子进程退出，返回进程退出错误（非零退出码时返回 error）
func (t *conptyTransport) Wait() error {
	if _, err := windows.WaitForSingleObject(windows.Handle(t.hProc), windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(windows.Handle(t.hProc), &code); err != nil {
		return err
	}
	syscall.CloseHandle(t.hProc)
	t.hProc = 0
	if code != 0 {
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}

// Close 关闭伪控制台与管道（幂等，可被退出协程与停止操作重复调用）
func (t *conptyTransport) Close() error {
	t.closeOnce.Do(func() {
		if t.inFile != nil {
			t.inFile.Close()
		}
		if t.outFile != nil {
			t.outFile.Close()
		}
		closePseudoConsole(t.hPC)
	})
	return nil
}

// ptyCmdline 按解释器构造伪控制台中的子进程命令行
func ptyCmdline(interpreter, cmdText string) string {
	switch interpreter {
	case cmd.InterpreterPowerShell:
		// 前缀 $ProgressPreference：抑制模块加载进度流（CLIXML 噪音），与 cmd 包包装保持一致
		cmdText = `$ProgressPreference='SilentlyContinue'; ` + cmdText
		return `powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ` + cmd.PowershellEncodedCommand(cmdText)
	case cmd.InterpreterBash:
		escaped := strings.ReplaceAll(cmdText, `"`, `\"`)
		return `"` + cmd.BashPath() + `" -c "` + escaped + `"`
	default:
		// /Q 关闭命令回显（ConPTY 下 cmd /c 会回显输入的命令行，与前端已展示的命令行重复）；
		// /s /c 保留内部引号（支持 ssh host "cmd ; cmd" 这类带引号命令）
		return `cmd /Q /s /c "chcp 65001 >nul && ` + cmdText + `"`
	}
}

// StartPTY 启动一个运行在伪控制台中的交互式命令
// 按解释器构造命令行：
//   - cmd（默认）：cmd /Q /s /c "chcp 65001 >nul && <cmd>" 包装
//     · /Q 关闭命令回显（ConPTY 下 cmd /c 会回显输入的命令行，与前端已展示的命令行重复）
//     · chcp 65001 将控制台代码页设为 UTF-8（避免中文乱码）
//     · /s /c 保留内部引号（支持 ssh host "cmd ; cmd" 这类带引号命令）
//   - powershell：powershell -EncodedCommand（UTF-16LE Base64，无转义问题）
//   - bash：WSL bash -c "script"（System32/bash.exe，PATH 解析）
func StartPTY(cmdText string, interpreter string) (Transport, error) {
	return newConPTY(ptyCmdline(interpreter, cmdText))
}

// StartShell 启动交互式 shell 会话（不执行具体命令，等待逐行输入）。
// 用于"会话式逐行执行"：同一个 shell 进程内逐行喂入命令，变量跨行共享、每行自动回显。
func StartShell(interpreter string) (Transport, error) {
	return newConPTY(shellCmdline(interpreter))
}

// shellCmdline 按解释器构造交互式 shell 的命令行
// 注意：CreateProcess 对无扩展名的可执行名解析不可靠（实测 "powershell" 会误启 cmd），
// 必须给完整路径。
func shellCmdline(interpreter string) string {
	switch interpreter {
	case cmd.InterpreterPowerShell:
		// 禁用 PSReadLine：交互式 PowerShell 用 ANSI 光标重绘提示符，
		// 会污染"会话式逐行执行"的哨兵/提示符字符串清理（xterm 下输出错位）。
		// -NoExit 保持 shell 打开，Remove-Module 后为无 ANSI 的简单提示符。
		return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe -NoLogo -NoProfile -NoExit -Command "Remove-Module PSReadLine -ErrorAction SilentlyContinue"`
	case cmd.InterpreterBash:
		return `"` + cmd.BashPath() + `" -i`
	default: // cmd
		return `cmd`
	}
}

// newConPTY 建立 ConPTY 伪控制台并启动指定命令行的子进程
func newConPTY(cmdline string) (Transport, error) {
	// 创建输入管道（我们写 inWrite，伪控制台读 inRead）
	var inRead, inWrite syscall.Handle
	if err := createPipe(&inRead, &inWrite); err != nil {
		return nil, fmt.Errorf("创建输入管道失败: %w", err)
	}
	// 创建输出管道（伪控制台写 outWrite，我们读 outRead）
	var outRead, outWrite syscall.Handle
	if err := createPipe(&outRead, &outWrite); err != nil {
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		return nil, fmt.Errorf("创建输出管道失败: %w", err)
	}

	// 创建伪控制台（120 列 x 30 行）
	// 注意：COORD 是 4 字节结构，x64 上按值传入单个寄存器，必须整体转成 int32 传值
	size := coord{x: 120, y: 30}
	var hPC syscall.Handle
	r1, _, e1 := procCreatePseudoConsole.Call(
		uintptr(*(*int32)(unsafe.Pointer(&size))),
		uintptr(inRead),
		uintptr(outWrite),
		0,
		uintptr(unsafe.Pointer(&hPC)))
	if int32(r1) < 0 { // CreatePseudoConsole 返回 HRESULT，<0 表示失败
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		syscall.CloseHandle(outWrite)
		return nil, fmt.Errorf("CreatePseudoConsole 失败: HRESULT=0x%x, %v", uint32(r1), e1)
	}

	// 初始化线程属性列表（先取大小）
	// 注意：lpSize 是 SIZE_T（64 位下 8 字节），必须用 uintptr，否则栈内存越界
	var attrSize uintptr
	procInitProcThreadAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&attrSize)))
	if attrSize == 0 {
		closePseudoConsole(hPC)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("InitializeProcThreadAttributeList 获取大小失败")
	}
	attrBuf := make([]byte, int(attrSize))
	attrList := (*procThreadAttributeList)(unsafe.Pointer(&attrBuf[0]))
	r2, _, e2 := procInitProcThreadAttrList.Call(
		uintptr(unsafe.Pointer(attrList)), 1, 0, uintptr(unsafe.Pointer(&attrSize)))
	if r2 == 0 {
		closePseudoConsole(hPC)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("InitializeProcThreadAttributeList 失败: %v", e2)
	}
	defer procDeleteProcThreadAttr.Call(uintptr(unsafe.Pointer(attrList)))

	// 绑定伪控制台句柄到属性列表
	r3, _, e3 := procUpdateProcThreadAttr.Call(
		uintptr(unsafe.Pointer(attrList)),
		0,
		procThreadAttributePseudoConsole,
		uintptr(hPC),
		unsafe.Sizeof(hPC),
		0, 0)
	if r3 == 0 {
		closePseudoConsole(hPC)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("UpdateProcThreadAttribute 失败: %v", e3)
	}

	// 启动子进程（cmd 运行在伪控制台中）
	// 关键：cb 必须等于 sizeof(STARTUPINFOEX)（含属性列表指针），
	// 否则 Windows 不会读取 lpAttributeList，伪控制台无法挂载到子进程
	cmdPtr, _ := windows.UTF16PtrFromString(cmdline)
	var si startupInfoEx
	si.startupInfo.Cb = uint32(unsafe.Sizeof(si))
	si.startupInfo.Flags |= windows.STARTF_USESTDHANDLES
	si.attributeList = attrList
	var pi windows.ProcessInformation
	err := windows.CreateProcess(
		nil,      // lpApplicationName
		cmdPtr,   // lpCommandLine（可写）
		nil, nil, // 进程/线程安全属性
		false, // bInheritHandles = FALSE（伪控制台经属性列表传递）
		windows.EXTENDED_STARTUPINFO_PRESENT,
		nil, nil, // 环境变量 / 工作目录
		&si.startupInfo,
		&pi)
	if err != nil {
		closePseudoConsole(hPC)
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		syscall.CloseHandle(outWrite)
		return nil, fmt.Errorf("CreateProcessW 失败: %v", err)
	}
	syscall.CloseHandle(syscall.Handle(pi.Thread))
	// CreateProcess 完成后释放传给伪控制台的句柄（降低设备对象引用计数）
	syscall.CloseHandle(inRead)
	syscall.CloseHandle(outWrite)

	inFile := os.NewFile(uintptr(inWrite), "conpty-in")
	outFile := os.NewFile(uintptr(outRead), "conpty-out")

	return &conptyTransport{
		hPC:     hPC,
		inFile:  inFile,
		outFile: outFile,
		hProc:   syscall.Handle(pi.Process),
	}, nil
}

// createPipe 创建匿名管道（官方示例：lpPipeAttributes=NULL，即非继承句柄）
func createPipe(readHandle, writeHandle *syscall.Handle) error {
	r, _, e := procCreatePipe.Call(
		uintptr(unsafe.Pointer(readHandle)),
		uintptr(unsafe.Pointer(writeHandle)),
		0, // lpPipeAttributes = NULL（非继承）
		0)
	if r == 0 {
		return e
	}
	return nil
}

func closePseudoConsole(hPC syscall.Handle) {
	if hPC != 0 {
		procClosePseudoConsole.Call(uintptr(hPC))
	}
}

// PreparePTYInput Windows 控制台以 \r（回车）作为输入行结束符
func PreparePTYInput(s string) string {
	return strings.ReplaceAll(s, "\n", "\r")
}
