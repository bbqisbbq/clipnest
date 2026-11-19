// ClipNest — a quiet clipboard manager for Windows, built with Wails v3.
//
// It lives in the system tray, records what you copy while it is running, and
// gives you a searchable window to manage that history. Closing the window
// keeps the app alive; only "退出" in the tray menu really quits.
//
// 这个文件只管"装配顺序"，不装细节：
//   - main.go   启动顺序：路径、设置预读、服务、App、窗口、托盘、监听
//   - wire.go   窗口与热键的开关（wiring 结构体，替代原来十几个互相捕获的闭包）
//   - tray.go   托盘图标与菜单
package main

import (
	"embed"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func init() {
	// Typed events: the binding generator turns these into TS event payloads.
	application.RegisterEvent[clip.Entry]("clip:new")
	application.RegisterEvent[clip.Entry]("clip:bumped")
	application.RegisterEvent[clip.Entry]("clip:updated")
	application.RegisterEvent[string]("clip:deleted")
	application.RegisterEvent[int]("clip:cleared")
	application.RegisterEvent[clip.Stats]("clip:stats")
	application.RegisterEvent[clip.Settings]("settings:changed")
	application.RegisterEvent[clip.SyncStatus]("sync:status")
	application.RegisterEvent[string]("picker:opened")
	application.RegisterEvent[string]("picker:paste-failed")
	application.RegisterEvent[string]("capture:opened")
}

func main() {
	// A console-subsystem build (debug builds, `go run`) would otherwise open a
	// black window at logon, and closing that window kills the app. Detaching
	// makes every build behave like a GUI app.
	clip.PrepareWindowlessStart()

	dataDir := defaultDataDir()
	exePath, _ := os.Executable()
	clip.TrimLog(dataDir, 2<<20)
	clip.Logf(dataDir, "=== ClipNest %s starting (exe=%s) ===", clip.Version, exePath)

	// Service first: we need the persisted settings to decide whether the
	// window starts visible.
	preSettings := clip.DefaultSettings()
	if st, err := clip.PeekSettings(dataDir); err == nil {
		preSettings = st
	}
	forceShow, forceHidden := parseArgs(os.Args[1:])

	w := &wiring{dataDir: dataDir, exePath: exePath}

	svc, err := clip.NewClipService(dataDir, clip.Hooks{
		Emit: func(name string, data any) {
			if w.app != nil {
				w.app.Event.Emit(name, data)
			}
		},
		ShowWindow:    w.showWindow,
		HideWindow:    w.hideWindow,
		Quit:          w.quit,
		ExePath:       exePath,
		PlatformWorks: true,
		ShowPickerAt:  w.showPickerAt,
		ShowCaptureAt: w.showCaptureAt,
		HideCapture:   w.hideCapture,
		ApplyChrome:   w.applyChrome,
		HidePicker:    w.hidePicker,
		OpenURL:       clip.OpenExternal,
		OnSettings: func(st clip.Settings) error {
			if w.pauseItem != nil {
				setPauseLabel(w.pauseItem, st.Capture)
			}
			if w.autoStartItem != nil {
				w.autoStartItem.SetChecked(st.AutoStart)
			}
			_ = w.applyCaptureHotkey(st.CaptureHotkey)
			return w.applyHotkey(st.Hotkey)
		},
	})
	if err != nil {
		log.Fatalf("打开数据目录失败 (%s): %v", dataDir, err)
	}
	w.svc = svc

	w.app = application.New(application.Options{
		Name:        "ClipNest",
		Description: "轻量剪贴板管理器：常驻托盘，静默记录文本、图片、链接与文件引用。",
		Icon:        appIcon,
		LogLevel:    slog.LevelWarn,
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		ErrorHandler: func(err error) {
			clip.Logf(dataDir, "app error: %v", err)
		},
		WarningHandler: func(msg string) {
			clip.Logf(dataDir, "app warning: %s", msg)
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.bbqisbbq.clipnest",
			OnSecondInstanceLaunch: func(_ application.SecondInstanceData) {
				w.showWindow()
			},
		},
		Mac: application.MacOptions{
			// Tray apps must survive their last window closing on macOS too.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	w.createWindows(preSettings, forceHidden || (!preSettings.ShowOnLaunch && !forceShow))

	w.applyHotkey(preSettings.Hotkey)
	clip.Logf(dataDir, "capture hotkey configured: %q", preSettings.CaptureHotkey)
	w.applyCaptureHotkey(preSettings.CaptureHotkey)

	w.buildTray(preSettings)

	// Start listening once the app is wired up.
	if err := svc.Start(); err != nil {
		// Capture failures must be visible in the log file: a GUI build has no
		// console, and "nothing happens" is the worst possible bug report.
		clip.Logf(dataDir, "listener start failed: %v", err)
		log.Printf("剪贴板监听启动失败: %v", err)
	}

	// Keep the logon entry in sync with the stored setting (repairs a stale path
	// after the app is moved).
	svc.ReconcileAutoStart()

	// The inline expander must not act inside ClipNest's own windows.
	svc.SetOwnWindowCheck(w.ownWindow)

	w.app.OnShutdown(svc.Shutdown)

	if err := w.app.Run(); err != nil {
		log.Fatal(err)
	}
}

// parseArgs reads the two launch switches: `--show` forces the window open
// (handy from a shortcut or while testing), `--background` is what the logon
// entry uses so autostart never pops a window in the user's face.
func parseArgs(args []string) (forceShow, forceHidden bool) {
	for _, arg := range args {
		switch arg {
		case "--show", "-show":
			forceShow = true
		case "--background", "-background":
			forceHidden = true
		}
	}
	return forceShow, forceHidden
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "ClipNest")
	}
	return filepath.Join(os.Getenv("APPDATA"), "ClipNest")
}
