package main

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// wiring holds everything main() used to keep in a dozen mutually-capturing
// closures: the three windows, the two menu items whose label/check state
// changes at runtime, and the two hotkeys currently registered.
//
// 拆出来只为一件事：原来 main() 有 470 行、闭包之间互相引用，想改"快速粘贴窗
// 怎么定位"得先读完整个启动流程。现在每个动作是一个方法，参数写在签名上。
//
// 注意：这些方法会在 app 尚未创建时被调用（比如服务在 application.New 之前
// 就可能回调 ShowWindow），所以每个方法都要自己判空。
type wiring struct {
	app     *application.App
	svc     *clip.ClipService
	dataDir string
	exePath string

	win     *application.WebviewWindow
	picker  *application.WebviewWindow
	capture *application.WebviewWindow

	pauseItem     *application.MenuItem
	autoStartItem *application.MenuItem

	hotkey        string
	captureHotkey string
}

func (w *wiring) showWindow() {
	if w.win == nil {
		return
	}
	w.win.Show()
	w.win.Focus()
}

func (w *wiring) hideWindow() {
	if w.win != nil {
		w.win.Hide()
	}
}

// showPickerAt 在 (x,y) 处弹出快速粘贴窗：锚点由服务按光标/插入符算好，
// 这里只负责夹到工作区内、抢前台、聚焦。
func (w *wiring) showPickerAt(x, y int) bool {
	if w.picker == nil {
		return false
	}
	px, py := clip.ClampToWorkArea(x, y, clip.PickerWidth, clip.PickerHeight)
	// Window calls and the foreground hand-off must happen on the main thread:
	// that is the thread that owns the popup window.
	application.InvokeSync(func() {
		w.picker.SetPosition(px, py)
		w.picker.Show()
		if hwnd := uintptr(w.picker.NativeWindow()); hwnd != 0 {
			clip.ActivateWindow(hwnd)
		}
		w.picker.Focus()
		clip.Logf(w.dataDir, "picker window: pos=(%d,%d) hwnd=%#x fg=%#x",
			px, py, uintptr(w.picker.NativeWindow()), clip.ForegroundWindowHandle())
	})
	if w.app != nil {
		w.app.Event.Emit("picker:opened", "")
	}
	return true
}

func (w *wiring) hidePicker() {
	if w.picker != nil {
		w.picker.Hide()
	}
}

// showCaptureAt 在光标处弹出「记一笔」。尺寸用户可拖，所以夹边距时取窗口的
// **当前**尺寸，不能用常量——否则拖大过的窗口会被按 620×220 夹，右下角跑到屏幕外。
func (w *wiring) showCaptureAt(x, y int) bool {
	if w.capture == nil {
		return false
	}
	cw, ch := w.capture.Size()
	if cw <= 0 || ch <= 0 {
		cw, ch = clip.CaptureWidth, clip.CaptureHeight
	}
	cx, cy := clip.ClampToWorkArea(x, y, cw, ch)
	application.InvokeSync(func() {
		w.capture.SetPosition(cx, cy)
		w.capture.Show()
		if hwnd := uintptr(w.capture.NativeWindow()); hwnd != 0 {
			clip.ActivateWindow(hwnd)
		}
		w.capture.Focus()
		clip.Logf(w.dataDir, "capture window: pos=(%d,%d) size=(%dx%d) hwnd=%#x",
			cx, cy, cw, ch, uintptr(w.capture.NativeWindow()))
	})
	if w.app != nil {
		w.app.Event.Emit("capture:opened", "")
	}
	return true
}

func (w *wiring) hideCapture() {
	if w.capture != nil {
		w.capture.Hide()
	}
}

// applyChrome 把 DWM 的系统边框/标题区刷成当前配色。
//
// picker 有意不设：它是透明窗口上的圆角卡片，DWM 的 1px 边框会沿着方形窗口
// 轮廓画一圈，正好落在本来要透明的四个角上。
func (w *wiring) applyChrome(dark bool, borderHex string) {
	application.InvokeSync(func() {
		if w.win != nil {
			clip.SetWindowChrome(uintptr(w.win.NativeWindow()), dark, borderHex)
		}
		if w.capture != nil {
			clip.SetWindowChrome(uintptr(w.capture.NativeWindow()), dark, borderHex)
		}
	})
}

// applyCaptureBackdrop 打开/关闭「记一笔」窗口背后的 DWM Acrylic 层，返回是否真的开着。
// 前端拿到 false 就把卡片画回不透明——半透明在没有 backdrop 的窗口里会被合到黑底上。
func (w *wiring) applyCaptureBackdrop(acrylic bool) bool {
	if w.capture == nil {
		return false
	}
	on := false
	application.InvokeSync(func() {
		on = clip.SetWindowBackdrop(uintptr(w.capture.NativeWindow()), acrylic)
	})
	return on
}

// applyHotkey (re)registers the quick-paste hotkey. 空串表示"不注册"；
// 同一个加速键重复调用是幂等的。
func (w *wiring) applyHotkey(accel string) error {
	if accel == w.hotkey {
		return nil
	}
	if w.hotkey != "" && w.app.GlobalShortcut.IsRegistered(w.hotkey) {
		_ = w.app.GlobalShortcut.Unregister(w.hotkey)
	}
	w.hotkey = ""
	if accel == "" {
		return nil
	}
	if err := w.app.GlobalShortcut.Register(accel, func() {
		if err := w.svc.ShowPicker(); err != nil {
			log.Printf("打开快速粘贴窗失败: %v", err)
		}
	}); err != nil {
		return err
	}
	w.hotkey = accel
	return nil
}

// applyCaptureHotkey 同上，作用于「记一笔」。
func (w *wiring) applyCaptureHotkey(accel string) error {
	if accel == w.captureHotkey {
		return nil
	}
	if w.captureHotkey != "" && w.app.GlobalShortcut.IsRegistered(w.captureHotkey) {
		_ = w.app.GlobalShortcut.Unregister(w.captureHotkey)
	}
	w.captureHotkey = ""
	if accel == "" {
		return nil
	}
	if err := w.app.GlobalShortcut.Register(accel, func() {
		if err := w.svc.ShowCapture(); err != nil {
			clip.Logf(w.dataDir, "open capture window failed: %v", err)
		}
	}); err != nil {
		clip.Logf(w.dataDir, "capture hotkey register failed: %q: %v", accel, err)
		return err
	}
	w.captureHotkey = accel
	clip.Logf(w.dataDir, "capture hotkey registered: %s", accel)
	return nil
}

func (w *wiring) quit() {
	if w.svc != nil {
		w.svc.Shutdown()
	}
	if w.app != nil {
		w.app.Quit()
	}
}

// ownWindow 报告前台窗口是不是 ClipNest 自己的（展开器不能在自己窗口里动作）。
func (w *wiring) ownWindow() bool {
	fg := clip.ForegroundWindowHandle()
	if fg == 0 {
		return false
	}
	for _, win := range []*application.WebviewWindow{w.win, w.picker, w.capture} {
		if win != nil && fg == uintptr(win.NativeWindow()) {
			return true
		}
	}
	return false
}

// createWindows builds the three windows. Hidden 决定主窗是否在启动时就显示。
func (w *wiring) createWindows(preSettings clip.Settings, hidden bool) {
	w.win = w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "ClipNest 剪贴板",
		Width:     1120,
		Height:    740,
		MinWidth:  900,
		MinHeight: 560,
		// --background (logon entry) always wins; --show forces a visible window
		// even when the setting says "stay in the tray".
		Hidden: hidden,
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
	w.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		w.win.Hide()
	})

	// 收起窗口时给前端递个信号：草稿的写库防抖在后端（internal/clip/draft.go），
	// 前端收到就立刻 Flush 一次。原生 Hide 不一定会让页面收到 visibilitychange，
	// 所以这条不能省——否则"敲完立刻点 ×"可能丢最后几个字。
	w.win.RegisterHook(events.Common.WindowHide, func(_ *application.WindowEvent) {
		if w.app != nil {
			w.app.Event.Emit("app:hidden", "")
		}
	})

	// Quick paste popup. Frameless, always on top, hidden until the hotkey
	// asks for it; it hides itself as soon as it loses focus.
	w.picker = w.app.Window.NewWithOptions(application.WebviewWindowOptions{
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
		// 弹窗是圆角卡片：窗口本身必须透明，否则四角会露出窗口底色（那四个"小黑点"）。
		// BackgroundTypeTransparent 在 Windows 上走 DirectComposition（WS_EX_NOREDIRECTIONBITMAP），
		// 页面背后是真透明，不是黑色。
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGB(18, 21, 28),
		URL:              "/?mode=picker",
	})

	// Quick capture popup: a memo in one keystroke, right where you are typing.
	//
	// 无边框，但**要能拖、要能拉**（2.6.x 的「记一笔」是个固定大小的死框，被吐槽过）：
	//   · 拖动 —— 前端给抬头加 `--wails-draggable: drag`，Wails 运行时据此把
	//     按下手势交给系统标题栏拖动，不用自己算鼠标位移。
	//   · 拉伸 —— 这里**不能**设 DisableResize：Wails v3 的运行时靠
	//     setResizable(true) 才把窗口四周 5px 激活成拉伸热区
	//     （Windows 上无边框窗没有原生边框，WM_NCCALCSIZE 把边吃掉了，
	//     所以「OS 自己会给」这个假设不成立）。
	//   · 尺寸 —— 记住上次拖到多大（本机 meta，不同步），位置仍在光标处弹出。
	captureW, captureH := w.svc.CaptureSize()
	w.capture = w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "capture",
		Title:           "记一笔",
		Width:           captureW,
		Height:          captureH,
		MinWidth:        clip.CaptureMinWidth,
		MinHeight:       clip.CaptureMinHeight,
		Frameless:       true,
		AlwaysOnTop:     true,
		Hidden:          true,
		HideOnFocusLost: true,
		Windows: application.WindowsWindow{
			Theme: application.SystemDefault,
			// backdrop 在运行时由 SetCaptureBackdrop 开关（见 internal/clip/backdrop_windows.go），
			// 建窗时先给 None：0% 透明度下不付合成开销，圆角也是干净的切边。
			BackdropType: application.None,
		},
		// 和 picker 一样：窗口本身必须透明，否则那两个 10px 圆角外面会垫一块矩形底色
		// （看起来就是浮在桌面上的一块方砖）。卡片自己是不是半透明由前端按
		// Settings.CaptureTransparency 决定，这里只把"四角真透明"这件底层的事做掉。
		// 用 Translucent 而不是 Transparent：两者在 Windows 上的 exStyle 完全相同
		// （都是 WS_EX_NOREDIRECTIONBITMAP），差别只在 Wails 建窗时会不会顺手开 DWM
		// backdrop；我们改成运行时自己开关，所以这里选哪个都行——选 Translucent 是为了
		// 让"这窗打算用 backdrop"在代码里读得出来。
		BackgroundType:   application.BackgroundTypeTranslucent,
		BackgroundColour: application.NewRGB(14, 16, 21),
		URL:              "/?mode=capture",
	})
	// 拉伸结束时把尺寸记下来。用窗口事件而不是前端 resize 回调：窗口被系统拖动
	// 拉伸时前端虽然也会发 resize，但只在用户松手后才值得写一次库。
	// 隐藏中的窗口不算：那时读到的是个还没排好版的小矩形，记下来会顶掉默认尺寸。
	w.capture.RegisterHook(events.Windows.WindowEndResize, func(_ *application.WindowEvent) {
		if !w.capture.IsVisible() {
			return
		}
		if cw, ch := w.capture.Size(); cw > 0 && ch > 0 {
			w.svc.SaveCaptureSize(cw, ch)
		}
	})
}
