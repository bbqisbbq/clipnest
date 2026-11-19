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
	application.RegisterEvent[string]("stealth:opened")
	application.RegisterEvent[string]("stealth:closed")
	application.RegisterEvent[string]("stealth:close-request")
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

	var (
		app                  *application.App
		win                  *application.WebviewWindow
		picker               *application.WebviewWindow
		capture              *application.WebviewWindow
		stealth              *application.WebviewWindow
		stealthHud           *application.WebviewWindow
		pauseItem            *application.MenuItem
		autoStartItem        *application.MenuItem
		currentHotkey        string
		currentCaptureHotkey string
		currentStealthHotkey string
		applyCaptureHotkey   func(string) error
		applyStealthHotkey   func(string) error
		svc                  *clip.ClipService
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

	// 快速输入：光标处弹一个小编辑框，Enter 存成备忘。
	showCaptureAt := func(x, y int) bool {
		if capture == nil {
			return false
		}
		cx, cy := clip.ClampToWorkArea(x, y, clip.CaptureWidth, clip.CaptureHeight)
		application.InvokeSync(func() {
			capture.SetPosition(cx, cy)
			capture.Show()
			if hwnd := uintptr(capture.NativeWindow()); hwnd != 0 {
				clip.ActivateWindow(hwnd)
			}
			capture.Focus()
			clip.Logf(dataDir, "capture window: pos=(%d,%d) hwnd=%#x", cx, cy, uintptr(capture.NativeWindow()))
		})
		if app != nil {
			app.Event.Emit("capture:opened", "")
		}
		return true
	}
	hideCapture := func() {
		if capture != nil {
			capture.Hide()
		}
	}

	// 隐写模式：一个全透明的输入窗（拿焦点、跑输入法、什么都看不见）+
	// 屏幕角落的小提示条（WS_EX_NOACTIVATE，永远不抢焦点）。
	showStealthAt := func(x, y int) bool {
		if stealth == nil {
			return false
		}
		sx, sy := clip.ClampToWorkArea(x, y, clip.StealthWidth, clip.StealthHeight)
		application.InvokeSync(func() {
			stealth.SetPosition(sx, sy)
			stealth.Show()
			if hwnd := uintptr(stealth.NativeWindow()); hwnd != 0 {
				clip.ActivateWindow(hwnd)
			}
			stealth.Focus()
			clip.Logf(dataDir, "stealth window: pos=(%d,%d) hwnd=%#x",
				sx, sy, uintptr(stealth.NativeWindow()))
		})
		return true
	}
	hideStealth := func() {
		if stealth != nil {
			stealth.Hide()
		}
	}
	showStealthHud := func() {
		if stealthHud == nil {
			return
		}
		hx, hy := clip.WorkAreaBottomRight(clip.StealthHudWidth, clip.StealthHudHeight)
		application.InvokeSync(func() {
			stealthHud.SetPosition(hx, hy)
			stealthHud.Show()
		})
	}
	hideStealthHud := func() {
		if stealthHud != nil {
			stealthHud.Hide()
		}
	}

	// 原生边框跟随主题：把 DWM 的系统边框/标题区刷成当前配色
	applyChrome := func(dark bool, borderHex string) {
		application.InvokeSync(func() {
			if win != nil {
				clip.SetWindowChrome(uintptr(win.NativeWindow()), dark, borderHex)
			}
			if picker != nil {
				clip.SetWindowChrome(uintptr(picker.NativeWindow()), dark, borderHex)
			}
			if capture != nil {
				clip.SetWindowChrome(uintptr(capture.NativeWindow()), dark, borderHex)
			}
		})
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

	applyCaptureHotkey = func(accel string) error {
		if accel == currentCaptureHotkey {
			return nil
		}
		if currentCaptureHotkey != "" && app.GlobalShortcut.IsRegistered(currentCaptureHotkey) {
			_ = app.GlobalShortcut.Unregister(currentCaptureHotkey)
		}
		currentCaptureHotkey = ""
		if accel == "" {
			return nil
		}
		if err := app.GlobalShortcut.Register(accel, func() {
			if err := svc.ShowCapture(); err != nil {
				clip.Logf(dataDir, "open capture window failed: %v", err)
			}
		}); err != nil {
			clip.Logf(dataDir, "capture hotkey register failed: %q: %v", accel, err)
			return err
		}
		currentCaptureHotkey = accel
		clip.Logf(dataDir, "capture hotkey registered: %s", accel)
		return nil
	}

	applyStealthHotkey = func(accel string) error {
		if accel == currentStealthHotkey {
			return nil
		}
		if currentStealthHotkey != "" && app.GlobalShortcut.IsRegistered(currentStealthHotkey) {
			_ = app.GlobalShortcut.Unregister(currentStealthHotkey)
		}
		currentStealthHotkey = ""
		if accel == "" {
			return nil
		}
		if err := app.GlobalShortcut.Register(accel, func() {
			if err := svc.ShowStealth(); err != nil {
				clip.Logf(dataDir, "open stealth window failed: %v", err)
			}
		}); err != nil {
			clip.Logf(dataDir, "stealth hotkey register failed: %q: %v", accel, err)
			return err
		}
		currentStealthHotkey = accel
		clip.Logf(dataDir, "stealth hotkey registered: %s", accel)
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
		ShowCaptureAt: showCaptureAt,
		HideCapture:   hideCapture,
		ShowStealthAt: showStealthAt,
		HideStealth:   hideStealth,
		ShowStealthHud: showStealthHud,
		HideStealthHud: hideStealthHud,
		ApplyChrome:   applyChrome,
		HidePicker:    hidePicker,
		OpenURL:       clip.OpenExternal,
		OnSettings: func(st clip.Settings) error {
			if pauseItem != nil {
				setPauseLabel(pauseItem, st.Capture)
			}
			if autoStartItem != nil {
				autoStartItem.SetChecked(st.AutoStart)
			}
			_ = applyCaptureHotkey(st.CaptureHotkey)
			_ = applyStealthHotkey(st.StealthHotkey)
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
		Hidden: forceHidden || (!preSettings.ShowOnLaunch && !forceShow),
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

	// Quick capture popup: a memo in one keystroke, right where you are typing.
	capture = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "capture",
		Title:           "记一笔",
		Width:           clip.CaptureWidth,
		Height:          clip.CaptureHeight,
		Frameless:       true,
		AlwaysOnTop:     true,
		Hidden:          true,
		DisableResize:   true,
		HideOnFocusLost: true,
		Windows: application.WindowsWindow{
			Theme: application.SystemDefault,
		},
		BackgroundColour: application.NewRGB(18, 21, 28),
		URL:              "/?mode=capture",
	})

	// 隐写模式（Ctrl+Alt+N）：光标处不出现任何输入框，焦点交给这个全透明的
	// 窗口；字只进内存缓冲，Ctrl+Enter 才落成备忘。它必须是「能拿焦点、能跑
	// 输入法」的真窗口，所以不能用 HideOnFocusLost——退出与否由前端决定
	// （失焦 = 存一次再退出，绝不丢字）。
	stealth = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:              "stealth",
		Title:             "ClipNest",
		Width:             clip.StealthWidth,
		Height:            clip.StealthHeight,
		Frameless:         true,
		AlwaysOnTop:       true,
		Hidden:            true,
		DisableResize:     true,
		IgnoreMouseEvents: true,
		BackgroundType:    application.BackgroundTypeTransparent,
		Windows: application.WindowsWindow{
			Theme:          application.SystemDefault,
			HiddenOnTaskbar: true,
		},
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/?mode=stealth",
	})

	// 角落提示条：显示「@目标@ · N 字」，永远不抢焦点。
	stealthHud = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:              "stealth-hud",
		Title:             "ClipNest",
		Width:             clip.StealthHudWidth,
		Height:            clip.StealthHudHeight,
		Frameless:         true,
		AlwaysOnTop:       true,
		Hidden:            true,
		DisableResize:     true,
		IgnoreMouseEvents: true,
		BackgroundType:    application.BackgroundTypeTransparent,
		Windows: application.WindowsWindow{
			Theme:          application.SystemDefault,
			HiddenOnTaskbar: true,
		},
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/?mode=stealthhud",
	})
	clip.MakeNonActivating(uintptr(stealthHud.NativeWindow()))

	applyHotkey(preSettings.Hotkey)
	clip.Logf(dataDir, "capture hotkey configured: %q", preSettings.CaptureHotkey)
	applyCaptureHotkey(preSettings.CaptureHotkey)
	clip.Logf(dataDir, "stealth hotkey configured: %q", preSettings.StealthHotkey)
	applyStealthHotkey(preSettings.StealthHotkey)

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
	// 记一笔：把剪贴板当前文本直接存成一条手写条目，不弹窗、不抢焦点
	menu.Add("记一笔（剪贴板内容）").OnClick(func(_ *application.Context) {
		go func() {
			if _, err := svc.QuickMemo(); err != nil {
				log.Printf("记一笔失败: %v", err)
			}
		}()
	})

	// 隐写输入：不弹输入框，字先进内存缓冲，Ctrl+Enter 才落成备忘
	menu.Add("隐写输入（无输入框）").OnClick(func(_ *application.Context) {
		go func() {
			if err := svc.ShowStealth(); err != nil {
				log.Printf("打开隐写输入失败: %v", err)
			}
		}()
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
		_, _ = svc.DeleteAll()
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
	svc.SetOwnWindowCheck(func() bool {
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
		if capture != nil && fg == uintptr(capture.NativeWindow()) {
			return true
		}
		if stealth != nil && fg == uintptr(stealth.NativeWindow()) {
			return true
		}
		if stealthHud != nil && fg == uintptr(stealthHud.NativeWindow()) {
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
