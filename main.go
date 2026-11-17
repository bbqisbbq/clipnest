// ClipNest — a quiet clipboard manager for Windows, built with Wails v3.
//
// It lives in the system tray, records what you copy while it is running, and
// gives you a searchable window to manage that history. Closing the window
// keeps the app alive; only "退出" in the tray menu really quits.
package main

import (
	"embed"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func init() {
	// Typed events: the binding generator turns these into TS event payloads.
	application.RegisterEvent[clip.Item]("clip:new")
	application.RegisterEvent[clip.Item]("clip:bumped")
	application.RegisterEvent[clip.Item]("clip:updated")
	application.RegisterEvent[string]("clip:deleted")
	application.RegisterEvent[int]("clip:cleared")
	application.RegisterEvent[clip.Stats]("clip:stats")
	application.RegisterEvent[clip.Settings]("settings:changed")
	application.RegisterEvent[clip.SyncStatus]("sync:status")
	application.RegisterEvent[string]("picker:opened")
	application.RegisterEvent[string]("picker:paste-failed")
}

func main() {
	dataDir := defaultDataDir()
	exePath, _ := os.Executable()
	clip.TrimLog(dataDir, 2<<20)
	clip.Logf(dataDir, "=== ClipNest %s starting (exe=%s) ===", clip.Version, exePath)

	var (
		app           *application.App
		win           *application.WebviewWindow
		picker        *application.WebviewWindow
		pauseItem     *application.MenuItem
		autoStartItem *application.MenuItem
		currentHotkey string
		svc           *clip.ClipService
	)

	showWindow := func() {
		if win == nil {
			return
		}
		win.Show()
		win.Focus()
	}
	hideWindow := func() {
		if win != nil {
			win.Hide()
		}
	}

	// The picker: a frameless always-on-top popup that appears at the caret.
	showPickerAt := func(x, y int) bool {
		if picker == nil {
			return false
		}
		px, py := clip.ClampToWorkArea(x, y, clip.PickerWidth, clip.PickerHeight)
		// Window calls and the foreground hand-off must happen on the main
		// thread: that is the thread that owns the popup window.
		application.InvokeSync(func() {
			picker.SetPosition(px, py)
			picker.Show()
			if hwnd := uintptr(picker.NativeWindow()); hwnd != 0 {
				clip.ActivateWindow(hwnd)
			}
			picker.Focus()
			clip.Logf(dataDir, "picker window: pos=(%d,%d) hwnd=%#x fg=%#x",
				px, py, uintptr(picker.NativeWindow()), clip.ForegroundWindowHandle())
		})
		if app != nil {
			app.Event.Emit("picker:opened", "")
		}
		return true
	}
	hidePicker := func() {
		if picker != nil {
			picker.Hide()
		}
	}

	var applyHotkey func(string) error
	applyHotkey = func(accel string) error {
		if accel == currentHotkey {
			return nil
		}
		if currentHotkey != "" && app.GlobalShortcut.IsRegistered(currentHotkey) {
			_ = app.GlobalShortcut.Unregister(currentHotkey)
		}
		currentHotkey = ""
		if accel == "" {
			return nil
		}
		if err := app.GlobalShortcut.Register(accel, func() {
			if err := svc.ShowPicker(); err != nil {
				log.Printf("打开快速粘贴窗失败: %v", err)
			}
		}); err != nil {
			return err
		}
		currentHotkey = accel
		return nil
	}

	quit := func() {
		if svc != nil {
			svc.Shutdown()
		}
		if app != nil {
			app.Quit()
		}
	}

	// Service first: we need the persisted settings to decide whether the
	// window starts visible.
	preSettings := clip.DefaultSettings()
	if st, err := clip.PeekSettings(dataDir); err == nil {
		preSettings = st
	}
	// `clipnest.exe --show` forces the window open (handy when launching from a
	// shortcut or while testing); `--background` is what the logon entry uses so
	// autostart never pops a window in the user's face.
	forceShow := false
	forceHidden := false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--show", "-show":
			forceShow = true
		case "--background", "-background":
			forceHidden = true
		}
	}

	svc, err := clip.NewClipService(dataDir, clip.Hooks{
		Emit: func(name string, data any) {
			if app != nil {
				app.Event.Emit(name, data)
			}
		},
		ShowWindow:    showWindow,
		HideWindow:    hideWindow,
		Quit:          quit,
		ExePath:       exePath,
		PlatformWorks: true,
		ShowPickerAt:  showPickerAt,
		HidePicker:    hidePicker,
		OpenURL:       clip.OpenExternal,
		OnSettings: func(st clip.Settings) error {
			if pauseItem != nil {
				setPauseLabel(pauseItem, st.Capture)
			}
			if autoStartItem != nil {
				autoStartItem.SetChecked(st.AutoStart)
			}
			return applyHotkey(st.Hotkey)
		},
	})
	if err != nil {
		log.Fatalf("打开数据目录失败 (%s): %v", dataDir, err)
	}

	app = application.New(application.Options{
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
				showWindow()
			},
		},
		Mac: application.MacOptions{
			// Tray apps must survive their last window closing on macOS too.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "ClipNest 剪贴板",
		Width:     1120,
		Height:    740,
		MinWidth:  900,
		MinHeight: 560,
		// --background (logon entry) always wins; --show forces a visible window
		// even when the setting says "stay in the tray".
		Hidden:    forceHidden || (!preSettings.ShowOnLaunch && !forceShow),
		// Frameless: the UI draws its own caption bar so the chrome matches the
		// content. Resizing still works (the Wails runtime handles edge drags).
		Frameless: true,
		Windows: application.WindowsWindow{
			Theme: application.SystemDefault,
		},
		BackgroundColour: application.NewRGB(10, 11, 15),
		URL:              "/",
	})

	// The whole point of a tray app: closing the window must not quit.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})

	// Quick paste popup. Frameless, always on top, hidden until the hotkey
	// asks for it; it hides itself as soon as it loses focus.
	picker = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "picker",
		Title:           "ClipNest",
		Width:           clip.PickerWidth,
		Height:          clip.PickerHeight,
		Frameless:       true,
		AlwaysOnTop:     true,
		Hidden:          true,
		DisableResize:   true,
		HideOnFocusLost: true,
		Windows: application.WindowsWindow{
			Theme: application.SystemDefault,
		},
		BackgroundColour: application.NewRGB(18, 21, 28),
		URL:              "/?mode=picker",
	})

	applyHotkey(preSettings.Hotkey)

	// Tray icon + menu.
	tray := app.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("ClipNest 剪贴板管理器")
	tray.OnClick(showWindow)

	menu := application.NewMenu()
	menu.Add("显示管理窗口").OnClick(func(_ *application.Context) { showWindow() })
	menu.Add("快速粘贴（光标处）").OnClick(func(_ *application.Context) {
		if err := svc.ShowPicker(); err != nil {
			log.Printf("打开快速粘贴窗失败: %v", err)
		}
	})
	pauseItem = menu.Add(pauseLabel(preSettings.Capture))
	pauseItem.OnClick(func(_ *application.Context) {
		st := svc.GetSettings()
		updated, err := svc.SetCapture(!st.Capture)
		if err == nil {
			setPauseLabel(pauseItem, updated.Capture)
		}
	})
	autoStartItem = menu.AddCheckbox("开机自动启动", svc.IsAutoStartEnabled())
	autoStartItem.OnClick(func(_ *application.Context) {
		enabled := !svc.IsAutoStartEnabled()
		if _, err := svc.SetAutoStart(enabled); err != nil {
			log.Printf("切换开机启动失败: %v", err)
		}
		autoStartItem.SetChecked(svc.IsAutoStartEnabled())
	})
	menu.AddSeparator()
	checkUpdateItem := menu.Add("检查更新…")
	checkUpdateItem.OnClick(func(_ *application.Context) {
		go func() {
			info := svc.CheckUpdate()
			showWindow()
			if info.Error != "" {
				log.Printf("检查更新失败: %s", info.Error)
			}
		}()
	})
	menu.Add("清空未收藏的历史").OnClick(func(_ *application.Context) {
		_, _ = svc.DeleteWhere(clip.Filter{})
	})
	sub := menu.AddSubmenu("更多")
	sub.Add("打开数据目录").OnClick(func(_ *application.Context) {
		_ = svc.OpenDataDir()
	})
	sub.Add("打开设置").OnClick(func(_ *application.Context) {
		showWindow()
		app.Event.Emit("ui:navigate", "settings")
	})
	menu.AddSeparator()
	menu.Add("退出 ClipNest").OnClick(func(_ *application.Context) { quit() })
	tray.SetMenu(menu)

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
	clip.SetExpanderForegroundGuard(func() bool {
		fg := clip.ForegroundWindowHandle()
		if fg == 0 {
			return false
		}
		if win != nil && fg == uintptr(win.NativeWindow()) {
			return true
		}
		if picker != nil && fg == uintptr(picker.NativeWindow()) {
			return true
		}
		return false
	})

	app.OnShutdown(svc.Shutdown)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func pauseLabel(capturing bool) string {
	if capturing {
		return "暂停收集"
	}
	return "继续收集"
}

func setPauseLabel(item *application.MenuItem, capturing bool) {
	if item == nil {
		return
	}
	item.SetLabel(pauseLabel(capturing))
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "ClipNest")
	}
	return filepath.Join(os.Getenv("APPDATA"), "ClipNest")
}
