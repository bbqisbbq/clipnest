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
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

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
	// 截图遮罩窗被唤起（前端据此重置选框、换一帧背景，见 frontend/src/Shot.tsx）。
	application.RegisterEvent[string]("shot:opened")
	// 托盘 → 前端的导航请求，以及"发现新版本"。
	// 这两个以前只 emit、没注册：前端收不到（导航是 2.8.1 才修好），
	// Wails 还会为每次检查更新记一条 "event not registered" 警告。
	application.RegisterEvent[string]("ui:navigate")
	application.RegisterEvent[clip.UpdateInfo]("update:available")
	// OCR 资源下载进度（设置页那行进度条靠它）。
	// 不注册的话前端也收不到事件，Wails 还会为每次 emit 记一条 "event not registered" 警告。
	application.RegisterEvent[clip.OCRProgress]("ocr:progress")
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

	w := &wiring{dataDir: dataDir, exePath: exePath, idle: newIdleWindows()}

	svc, err := clip.NewClipService(dataDir, clip.Hooks{
		Emit: func(name string, data any) {
			if w.app != nil {
				w.app.Event.Emit(name, data)
			}
		},
		ShowWindow:     w.showWindow,
		HideWindow:     w.hideWindow,
		Quit:           w.quit,
		ExePath:        exePath,
		PlatformWorks:  true,
		ShowPickerAt:   w.showPickerAt,
		ShowCaptureAt:  w.showCaptureAt,
		HideCapture:    w.hideCapture,
		ShowShotWindow: w.showShotWindow,
		HideShotWindow: w.hideShotWindow,
		ApplyChrome:    w.applyChrome,
		ApplyBackdrop:  w.applyCaptureBackdrop,
		HidePicker:     w.hidePicker,
		OpenURL:        clip.OpenExternal,
		OnSettings: func(st clip.Settings) error {
			if w.pauseItem != nil {
				setPauseLabel(w.pauseItem, st.Capture)
			}
			if w.autoStartItem != nil {
				w.autoStartItem.SetChecked(st.AutoStart)
			}
			// 热键注册失败必须让调用方知道：撞了别的软件的热键时，设置页要能报错，
			// 而不是显示"已保存"然后 Ctrl+Shift+N 永远没反应。
			if err := w.applyCaptureHotkey(st.CaptureHotkey); err != nil {
				return fmt.Errorf("「记一笔」快捷键注册失败: %w", err)
			}
			if err := w.applyShotHotkey(st.ShotHotkey); err != nil {
				return fmt.Errorf("截图快捷键注册失败: %w", err)
			}
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
			Handler: shotAwareAssets(application.AssetFileServerFS(assets), svc),
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
	if err := w.applyCaptureHotkey(preSettings.CaptureHotkey); err != nil {
		clip.Logf(dataDir, "capture hotkey registration failed: %v", err)
	}
	// 截图热键：老 profile 里没有这个字段（空串），Start 里的 EnsureShotDefaults
	// 会补上默认值并回调 OnSettings 重新注册这一次。
	clip.Logf(dataDir, "shot hotkey configured: %q", preSettings.ShotHotkey)
	if err := w.applyShotHotkey(preSettings.ShotHotkey); err != nil {
		clip.Logf(dataDir, "shot hotkey registration failed: %v", err)
		// 默认键被别的程序占了：记下来，别让这一次抢不到把后面每次保存设置都拦下来
		// （用户改个主题却被弹"截图快捷键注册失败"）。用户换一个键就会清掉这个标记。
		w.shotHotkeyBlocked = preSettings.ShotHotkey
	}

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

// shotAwareAssets 在静态资源前面插一条 /shot/current：截图遮罩窗用
// `<img src="/shot/current">` 拿"按下快捷键时拍到的那一帧"。
//
// 为什么不走 Wails 绑定方法：一张 4K 截图 base64 之后有好几 MB，塞进 JSON 传一次
// 要几十毫秒，而且这段字符串会一直留在 JS 堆里；图片本来就应该由 <img> 从 HTTP 拿。
func shotAwareAssets(base http.Handler, svc *clip.ClipService) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shot/current" {
			png := svc.CurrentShotPNG()
			if len(png) == 0 {
				http.NotFound(rw, r)
				return
			}
			rw.Header().Set("Content-Type", "image/png")
			rw.Header().Set("Content-Length", strconv.Itoa(len(png)))
			rw.Header().Set("Cache-Control", "no-store")
			_, _ = rw.Write(png)
			return
		}
		base.ServeHTTP(rw, r)
	})
}

