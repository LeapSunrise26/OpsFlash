package main

import (
	"embed"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"opsflash/server"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	AppName = "OpsFlash"
	Version = "0.1.0"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Register a custom event whose associated data type is string.
	// This is not required, but the binding generator will pick up registered events
	// and provide a strongly typed JS/TS API for them.
	application.RegisterEvent[string]("time")
}

// main function serves as the application's entry point. It initializes the application, creates a window,
// and starts a goroutine that emits a time-based event every second. It subsequently runs the application and
// logs any error that might occur.
func main() {
	// 初始化日志：同时输出到 data/ 目录和 stderr
	closeLogger := setupLogging()
	defer closeLogger()

	// 初始化 SQLite 数据库
	if err := server.InitDB(); err != nil {
		slog.Error("初始化数据库失败", "error", err)
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer server.CloseDB()

	// 自动启动 auto_start=1 的隧道
	server.AutoStartTunnels()

	// Create a new Wails application by providing the necessary options.
	// Variables 'Name' and 'Description' are for application metadata.
	// 'Assets' configures the asset server with the 'FS' variable pointing to the frontend files.
	// 'Bind' is a list of Go struct instances. The frontend has access to the methods of these instances.
	// 'Mac' options tailor the application when running an macOS.
	app := application.New(application.Options{
		Name:        AppName,
		Description: "OpsFlash 运维工具",
		Services: []application.Service{
			application.NewService(&server.GreetService{}),
			application.NewService(&server.AuthService{}),
			application.NewService(&server.UserService{}),
			application.NewService(&server.ConnService{}),
			application.NewService(&server.TunnelService{}),
			application.NewService(&server.ScriptsService{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background colour of the window.
	// 'URL' is the URL that will be loaded into the webview.
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: AppName,
		// Window sized to the golden ratio (1000 / 618 ≈ 1.618).
		Width:  1060,
		Height: 700,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	// 系统托盘：关闭窗口最小化到托盘；托盘菜单提供「显示主页面 / 退出」
	setupTray(app, window)

	// Create a goroutine that emits an event containing the current time every second.
	// The frontend can listen to this event and update the UI accordingly.
	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	// Run the application. This blocks until the application has been exited.
	err := app.Run()

	// 停止所有运行中的隧道
	server.ShutdownTunnels()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}

// setupLogging 初始化日志系统：每日一个日志文件，同时输出到 stderr（方便 dev 模式）
// 返回一个关闭函数，调用方应 defer 调用以刷新日志。
func setupLogging() func() {
	logDir := filepath.Join(".", "data")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "创建日志目录失败: %v\n", err)
		return func() {}
	}

	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, fmt.Sprintf("app-%s.log", dateStr))

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件失败: %v\n", err)
		return func() {}
	}

	// 同时写入文件和 stderr，dev 模式下终端也能看到
	multiWriter := io.MultiWriter(f, os.Stderr)
	handler := slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	slog.Info("日志系统初始化完成", "file", logPath)

	return func() {
		slog.Info("应用退出，关闭日志")
		f.Close()
	}
}

// quitting 标记应用是否正在真正退出（区分"关闭到托盘"与"退出应用"）
var quitting atomic.Bool

// quitApp 真正退出应用（托盘菜单 / 主菜单 / Ctrl+Q 统一入口）
func quitApp(app *application.App) {
	quitting.Store(true)
	app.Quit()
}

// setupTray 创建系统托盘：
//   - 点击托盘图标切换窗口显示/隐藏（保留隐藏前位置与最大化状态，不重定位）
//   - 托盘菜单：显示主页面 / 退出
//   - 点击窗口关闭按钮 → 拦截并隐藏到托盘（不退出）
func setupTray(app *application.App, window application.Window) {
	tray := app.SystemTray.New()

	// 托盘图标：使用 embed 的 logo.png（Vite 会将 public/ 拷贝到 dist/）
	if icon, err := assets.ReadFile("frontend/dist/logo.png"); err == nil {
		tray.SetIcon(icon)
	} else {
		slog.Warn("读取托盘图标失败，使用默认图标", "error", err)
	}
	tray.SetTooltip("OpsFlash 运维工具（点击显示/隐藏主窗口）")

	// 点击托盘图标切换显隐。
	// 注意：不使用 AttachWindow 的默认 ToggleWindow——它会在显示前 PositionWindow
	// 把窗口重新定位到托盘附近（右下角），导致恢复位置丢失、最大化状态偏移。
	tray.OnClick(func() {
		if window.IsVisible() {
			window.Hide()
		} else {
			window.Show()
			window.Focus()
		}
	})

	menu := app.NewMenu()
	menu.Add("显示主页面").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		quitApp(app)
	})
	tray.SetMenu(menu)

	// 关闭窗口 → 隐藏到托盘；真正退出（quitApp）时不拦截
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		e.Cancel()
		window.Hide()
	})
}
