// ClipNest — a quiet clipboard manager for Windows.
//
// 宿主已从 Wails v3 换成 MyGo（分支 mygo-shell，见 docs/design/MYGO-MIGRATION.md）。
// 这个文件仍然只管"装配顺序"：
//
//	main.go    启动顺序：路径、设置预读、服务、App、窗口、托盘、监听
//	wire.go    窗口与热键的开关（wiring 结构体）
//	tray.go    托盘图标与菜单
//	events.go  Go → 页面的事件（名字与载荷与 Wails 版完全一致）
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/egoist/mygo"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	// A console-subsystem build (debug builds, `go run`) would otherwise open a
	// black window at logon, and closing that window kills the app. Detaching
	// makes every build behave like a GUI app.
	clip.PrepareWindowlessStart()

	// 只跑一个实例：第二次启动让已经在跑的那个把窗口亮出来。
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

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
		Emit:           emit,
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
			// 而不是显示"已保存"然后 Ctrl+Shift+V 永远没反应。
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

	// 前端产物：MyGo 用 fs.FS 提供页面，自定义协议处理 /shot/current。
	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("前端产物缺失: %v", err)
	}
	mygo.SetFrontend(dist)
	// shot 遮罩窗要拿"按下快捷键时拍到的那一帧"：`<img src="/shot/current">`。
	// MyGo 的自定义协议就是个 http.Handler，包装逻辑与 Wails 版一模一样。
	mygo.Protocol.Handle("mygo", shotAwareAssets(mygo.FileServer(dist), svc))

	mygo.App.SetName("ClipNest")
	// 托盘应用：关掉窗口不等于退出。
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnSecondInstance(func([]string, string) { w.showWindow() })
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { svc.Shutdown() })

	// 前端用 call("ClipService.GetEntries", …) 调过来，名字要和 Wails 版一致。
	mygo.BindAs("ClipService", clipAPI{svc})

	mygo.App.WhenReady(func() {
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
			// 默认键被别的程序占了：记下来，别让这一次抢不到把后面每次保存设置都拦下来。
			w.shotHotkeyBlocked = preSettings.ShotHotkey
		}

		w.buildTray(preSettings)

		// Start listening once the app is wired up.
		if err := svc.Start(); err != nil {
			clip.Logf(dataDir, "listener start failed: %v", err)
			log.Printf("剪贴板监听启动失败: %v", err)
		}

		// Keep the logon entry in sync with the stored setting (repairs a stale
		// path after the app is moved).
		svc.ReconcileAutoStart()

		// The inline expander must not act inside ClipNest's own windows.
		svc.SetOwnWindowCheck(w.ownWindow)
	})

	if err := mygo.App.Run(); err != nil {
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
// 为什么不走绑定方法：一张 4K 截图 base64 之后有好几 MB，塞进 JSON 传一次
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

// 让 log 的默认输出走文件（GUI 构建没有控制台）。
var _ = slog.LevelWarn


