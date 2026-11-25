package main

import (
	"fmt"
	"log"
	"time"

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
	// shot 是全屏框选截图的遮罩窗：位置与尺寸由 clip.PlaceOverlayWindow 用物理像素
	// 直接摆（Wails 的 SetPosition/SetSize 走 DIP，缩放屏上会差一圈）。
	shot *application.WebviewWindow

	pauseItem     *application.MenuItem
	autoStartItem *application.MenuItem

	// idle 是 Gate 2.5 的实验开关：CLIPNEST_IDLE_DESTROY=1 时三个弹窗闲置即销毁、
	// 下次唤起重建。默认关（行为与以前一致）。见 idle_windows.go。
	idle *idleWindows

	hotkey        string
	captureHotkey string
	shotHotkey    string
	// shotHotkeyBlocked 记下"启动时就被别的程序占用、已经报过一次"的那个组合键。
	// 它不是"永远不报错"：用户主动在设置页选一个被占用的键，照样当场报错——只是别让
	// 一把抢不到的默认键把**所有**设置的保存都卡死（改个主题都弹"截图快捷键注册失败"）。
	shotHotkeyBlocked string
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
	p := w.ensurePicker()
	if p == nil {
		return false
	}
	w.idle.cancel("picker")
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
		w.retire("picker", w.picker, func() { w.picker = nil })
	}
}

// retire 在闲置一段时间后把弹窗销毁（CLIPNEST_IDLE_DESTROY=1 时才有动作）。
// 到点若窗口又可见了（用户连按两次热键）就不销毁。
func (w *wiring) retire(kind string, win *application.WebviewWindow, clear func()) {
	if win == nil {
		return
	}
	w.idle.schedule(kind,
		func() bool { return win.IsVisible() },
		func() {
			w.idle.markClosing(kind)
			win.Close() // 不带 Cancel 的 WindowClosing → Wails 把它从管理器摘掉并销毁
			clear()
			clip.Logf(w.dataDir, "idle: %s window destroyed", kind)
		})
}

// showCaptureAt 在光标处弹出「记一笔」。尺寸用户可拖，所以夹边距时取窗口的
// **当前**尺寸，不能用常量——否则拖大过的窗口会被按 620×220 夹，右下角跑到屏幕外。
func (w *wiring) showCaptureAt(x, y int) bool {
	c := w.ensureCapture()
	if c == nil {
		return false
	}
	w.idle.cancel("capture")
	cw, ch := c.Size()
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
		w.retire("capture", w.capture, func() { w.capture = nil })
	}
}

// showShotWindow 把遮罩窗精确铺在"按下快捷键时鼠标所在那块显示器"上。
//
// 桌面截图的两条硬要求：① 必须在**已经在内存里的那一帧**上铺开（截图先拍好了，
// 见 service_shot.go）；② 位置尺寸按物理像素给（monitor rect 就是物理像素），
// 所以走 Win32 SetWindowPos，不用 Wails 的 DIP 接口。
func (w *wiring) showShotWindow() bool {
	s := w.ensureShot()
	if s == nil {
		return false
	}
	w.idle.cancel("shot")
	left, top, sw, sh := w.svc.ShotBounds()
	if sw <= 0 || sh <= 0 {
		return false
	}
	// Show 必须走主线程；隐藏窗的 WebView2 controller 是第一次 Show 时才嵌进来的，
	// 所以窗口句柄要等一会儿才有（见下面的轮询）。
	application.InvokeSync(func() { w.shot.Show() })

	go func() {
		var hwnd uintptr
		// 第一次唤起实测要 ~100ms 才拿到句柄；之后都是立即有。
		for i := 0; i < 60; i++ {
			if h := uintptr(w.shot.NativeWindow()); h != 0 {
				hwnd = h
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if hwnd == 0 {
			clip.Logf(w.dataDir, "shot window: 句柄迟迟没建出来，遮罩可能没铺满")
			return
		}
		// 用物理像素把遮罩精确铺在显示器上（Wails 的 SetSize/SetPosition 走 DIP，
		// 缩放屏上会差一圈）。然后抢前台、把焦点交给 WebView 子窗口，Esc 才能生效。
		clip.PlaceOverlayWindow(hwnd, left, top, sw, sh)
		clip.ActivateWindow(hwnd)
		clip.FocusOverlayWindow(hwnd)
		clip.Logf(w.dataDir, "shot window: pos=(%d,%d) size=(%dx%d) hwnd=%#x", left, top, sw, sh, hwnd)
		// 铺好之后再让前端换帧：这时窗口已经是最终尺寸，用户看到的第一眼就是对的。
		if w.app != nil {
			w.app.Event.Emit("shot:opened", "")
		}
	}()
	return true
}

func (w *wiring) hideShotWindow() {
	if w.shot == nil {
		return
	}
	application.InvokeSync(func() {
		clip.HideOverlayWindow(uintptr(w.shot.NativeWindow()))
	})
	w.retire("shot", w.shot, func() { w.shot = nil })
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
	if clip.HotkeyTaken(accel) {
		return fmt.Errorf("快捷键 %s 已被其它程序占用", accel)
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
	if clip.HotkeyTaken(accel) {
		return fmt.Errorf("快捷键 %s 已被其它程序占用", accel)
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

// applyShotHotkey 同上，作用于框选截图。
func (w *wiring) applyShotHotkey(accel string) error {
	if accel == w.shotHotkey {
		return nil
	}
	if w.shotHotkey != "" && w.app.GlobalShortcut.IsRegistered(w.shotHotkey) {
		_ = w.app.GlobalShortcut.Unregister(w.shotHotkey)
	}
	w.shotHotkey = ""
	if accel == "" {
		return nil
	}
	// 先同步探一次：Wails 在 Run() 之前只排队，撞车要等启动后才会经由错误处理器报出来，
	// 那时用户早就以为"设置保存成功了"（见 internal/clip/hotkey_windows.go）。
	if clip.HotkeyTaken(accel) {
		if accel == w.shotHotkeyBlocked {
			clip.Logf(w.dataDir, "shot hotkey still owned by another app: %s（不阻塞其它设置保存）", accel)
			return nil
		}
		return fmt.Errorf("快捷键 %s 已被其它程序占用", accel)
	}
	if err := w.app.GlobalShortcut.Register(accel, func() {
		if err := w.svc.StartShot(); err != nil {
			clip.Logf(w.dataDir, "screenshot start failed: %v", err)
		}
	}); err != nil {
		clip.Logf(w.dataDir, "shot hotkey register failed: %q: %v", accel, err)
		return err
	}
	w.shotHotkey = accel
	w.shotHotkeyBlocked = ""
	clip.Logf(w.dataDir, "shot hotkey registered: %s", accel)
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
	for _, win := range []*application.WebviewWindow{w.win, w.picker, w.capture, w.shot} {
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


	// 三个弹窗：默认仍是"启动即建、只隐藏"（保持原行为）；设了
	// CLIPNEST_IDLE_DESTROY=1 时改为"用到才建（ensure*）、闲置就销毁（retire）"。
	if !w.idle.on() {
		w.ensurePicker()
		w.ensureCapture()
		w.ensureShot()
	}
}

// ensurePicker 建（或复用）快速粘贴窗：无边框、置顶、热键唤起、失焦自隐。
func (w *wiring) ensurePicker() *application.WebviewWindow {
	if w.picker != nil {
		return w.picker
	}
	p := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
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
	// 失焦自隐是 Wails 自己做的（HideOnFocusLost），会绕过 hidePicker：
	// 补一条 WindowHide 钩子，闲置销毁才不会漏掉这条最常见的路径。
	p.RegisterHook(events.Common.WindowHide, func(*application.WindowEvent) {
		w.retire("picker", p, func() {
			if w.picker == p {
				w.picker = nil
			}
		})
	})
	// 关闭请求（Alt+F4 之类）一律降级成"隐藏"：以前没有这条钩子，外部关掉会把窗口
	// 销毁而 w.picker 还留着指针；闲置销毁这条路又必须能真的关掉，所以用 allowClose 区分。
	p.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if w.idle.allowClose("picker") {
			if w.picker == p {
				w.picker = nil
			}
			return
		}
		e.Cancel()
		w.hidePicker()
	})
	w.picker = p
	return p
}

// ensureCapture 建（或复用）「记一笔」浮层。
//
// 无边框，但**要能拖、要能拉**（2.6.x 的「记一笔」是个固定大小的死框，被吐槽过）：
//   - 拖动 —— 前端给抬头加 `--wails-draggable: drag`，Wails 运行时据此把
//     按下手势交给系统标题栏拖动，不用自己算鼠标位移。
//   - 拉伸 —— 这里**不能**设 DisableResize：Wails v3 的运行时靠
//     setResizable(true) 才把窗口四周 5px 激活成拉伸热区
//     （Windows 上无边框窗没有原生边框，WM_NCCALCSIZE 把边吃掉了，
//     所以「OS 自己会给」这个假设不成立）。
//   - 尺寸 —— 记住上次拖到多大（本机 meta，不同步），位置仍在光标处弹出。
func (w *wiring) ensureCapture() *application.WebviewWindow {
	if w.capture != nil {
		return w.capture
	}
	captureW, captureH := w.svc.CaptureSize()
	c := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
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
		// 用 Translucent 而不是 Transparent：两者 exStyle 相同（都是 WS_EX_NOREDIRECTIONBITMAP），
		// 但实测 Transparent 那条路页面 alpha=0 的地方会露出 WebView2 的白底，
		// 而 Translucent 能让 alpha=0 的地方透出桌面（圆角切边靠的就是这个）。
		BackgroundType:   application.BackgroundTypeTranslucent,
		BackgroundColour: application.NewRGB(14, 16, 21),
		URL:              "/?mode=capture",
	})
	// 拉伸结束时把尺寸记下来。用窗口事件而不是前端 resize 回调：窗口被系统拖动
	// 拉伸时前端虽然也会发 resize，但只在用户松手后才值得写一次库。
	// 隐藏中的窗口不算：那时读到的是个还没排好版的小矩形，记下来会顶掉默认尺寸。
	c.RegisterHook(events.Windows.WindowEndResize, func(_ *application.WindowEvent) {
		if !c.IsVisible() {
			return
		}
		if cw, ch := c.Size(); cw > 0 && ch > 0 {
			w.svc.SaveCaptureSize(cw, ch)
		}
	})
	c.RegisterHook(events.Common.WindowHide, func(*application.WindowEvent) {
		w.retire("capture", c, func() {
			if w.capture == c {
				w.capture = nil
			}
		})
	})
	c.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if w.idle.allowClose("capture") {
			if w.capture == c {
				w.capture = nil
			}
			return
		}
		e.Cancel()
		w.hideCapture()
	})
	w.capture = c
	return c
}

// ensureShot 建（或复用）框选截图的遮罩窗。
//
// 建的时候给个小尺寸并隐藏：真正的铺满由 ShowShotWindow 用 SetWindowPos 按显示器
// 物理像素摆（见 internal/clip/shot_windows.go）。不要开 HideOnFocusLost ——
// 截图的整个过程里它就该一直是前台窗；也不要用透明背景：遮罩自己会画半透明黑，
// 窗口透明只会让 WebView2 后面露出桌面，边缘还会带一圈脏像素。
func (w *wiring) ensureShot() *application.WebviewWindow {
	if w.shot != nil {
		return w.shot
	}
	s := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "shot",
		Title:         "ClipNest 截图",
		Width:         320,
		Height:        240,
		Frameless:     true,
		AlwaysOnTop:   true,
		Hidden:        true,
		DisableResize: true,
		Windows: application.WindowsWindow{
			Theme: application.SystemDefault,
		},
		BackgroundColour: application.NewRGB(8, 9, 12),
		URL:              "/?mode=shot",
	})
	// Esc / 点完立刻切走：遮罩窗不该被当成"用户手上的工作窗口"。
	s.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if w.idle.allowClose("shot") {
			return // 这是我们自己的闲置销毁，放行
		}
		e.Cancel()
		w.hideShotWindow()
	})
	w.shot = s
	return s
}

