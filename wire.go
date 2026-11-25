package main

import (
	"fmt"
	"log"
	"time"

	"github.com/egoist/mygo"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// wiring holds the four windows, the two menu items whose label/check state
// changes at runtime, and the three hotkeys currently registered.
//
// 每个方法都要自己判空：服务可能在 App/窗口就绪之前就回调它们。
type wiring struct {
	svc     *clip.ClipService
	dataDir string
	exePath string

	win     *mygo.Window
	picker  *mygo.Window
	capture *mygo.Window
	// shot 是全屏框选截图的遮罩窗：位置与尺寸由 clip.PlaceOverlayWindow 用物理像素
	// 直接摆（窗口的 SetBounds 走 DIP，缩放屏上会差一圈）。
	shot *mygo.Window

	tray *mygo.Tray
	// pauseItem / autoStartItem 会在运行时被改标签或勾选状态。
	pauseItem     *mygo.MenuItem
	autoStartItem *mygo.MenuItem

	hotkey        string
	captureHotkey string
	shotHotkey    string
	// shotHotkeyBlocked 记下"启动时就被别的程序占用、已经报过一次"的那个组合键。
	shotHotkeyBlocked string

	// idle 控制"弹窗闲置即销毁"（Gate 2.5，默认开、picker 除外）。见 idle_windows.go。
	idle *idleWindows

	// 上一次的窗口外观：capture 被闲置销毁后重建要重放一遍，否则 DWM 边框色/材质
	// 会回到系统默认，直到下次保存设置（见 docs/design/MYGO-GATE2-5.md §4）。
	chromeDark   bool
	chromeBorder string
	hasChrome    bool
	backdrop     bool
	hasBackdrop  bool
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
	mygo.RunOnMain(func() {
		p.SetPosition(px, py)
		p.Show()
		if hwnd := p.NativeHandle(); hwnd != 0 {
			clip.ActivateWindow(hwnd)
		}
		p.Focus()
		clip.Logf(w.dataDir, "picker window: pos=(%d,%d) hwnd=%#x fg=%#x",
			px, py, p.NativeHandle(), clip.ForegroundWindowHandle())
	})
	_ = evPickerOpened.Broadcast("")
	return true
}

func (w *wiring) hidePicker() {
	if w.picker != nil {
		w.picker.Hide()
		w.retire("picker", w.picker, func() { w.picker = nil })
	}
}

// retire 在闲置一段时间后把弹窗销毁（默认开）。到点若窗口又可见了（用户连按两次
// 热键）就不销毁。
func (w *wiring) retire(kind string, win *mygo.Window, clear func()) {
	if win == nil {
		return
	}
	w.idle.schedule(kind,
		func() bool { return win.IsVisible() },
		func() {
			w.idle.markClosing(kind)
			win.Close() // 不带 PreventDefault 的 OnClose → 窗口真的销毁
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
	mygo.RunOnMain(func() {
		c.SetPosition(cx, cy)
		c.Show()
		if hwnd := c.NativeHandle(); hwnd != 0 {
			clip.ActivateWindow(hwnd)
		}
		c.Focus()
		clip.Logf(w.dataDir, "capture window: pos=(%d,%d) size=(%dx%d) hwnd=%#x",
			cx, cy, cw, ch, c.NativeHandle())
	})
	_ = evCaptureOpened.Broadcast("")
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
// 所以走 Win32 SetWindowPos，不用 DIP 接口。
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
	// Show 必须走主线程。WebView2 的 controller 要到第一次 Show 时才嵌进来，
	// 所以窗口句柄要等一会儿才有（见下面的轮询）。
	mygo.RunOnMain(func() { s.Show() })

	go func() {
		var hwnd uintptr
		for i := 0; i < 60; i++ {
			if h := s.NativeHandle(); h != 0 {
				hwnd = h
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if hwnd == 0 {
			clip.Logf(w.dataDir, "shot window: 句柄迟迟没建出来，遮罩可能没铺满")
			return
		}
		clip.PlaceOverlayWindow(hwnd, left, top, sw, sh)
		clip.ActivateWindow(hwnd)
		clip.FocusOverlayWindow(hwnd)
		clip.Logf(w.dataDir, "shot window: pos=(%d,%d) size=(%dx%d) hwnd=%#x", left, top, sw, sh, hwnd)
		// 铺好之后再让前端换帧：这时窗口已经是最终尺寸，用户看到的第一眼就是对的。
		_ = evShotOpened.Broadcast("")
	}()
	return true
}

func (w *wiring) hideShotWindow() {
	if w.shot == nil {
		return
	}
	mygo.RunOnMain(func() {
		clip.HideOverlayWindow(w.shot.NativeHandle())
	})
	w.retire("shot", w.shot, func() { w.shot = nil })
}

// applyChrome 把 DWM 的系统边框/标题区刷成当前配色。
//
// picker 有意不设：它是透明窗口上的圆角卡片，DWM 的 1px 边框会沿着方形窗口
// 轮廓画一圈，正好落在本来要透明的四个角上。
func (w *wiring) applyChrome(dark bool, borderHex string) {
	w.chromeDark, w.chromeBorder, w.hasChrome = dark, borderHex, true
	mygo.RunOnMain(w.applyChromeNow)
}

// applyChromeNow 用缓存的颜色刷一遍主窗与「记一笔」（capture 重建后要重放）。
func (w *wiring) applyChromeNow() {
	if w.win != nil {
		clip.SetWindowChrome(w.win.NativeHandle(), w.chromeDark, w.chromeBorder)
	}
	if w.capture != nil {
		clip.SetWindowChrome(w.capture.NativeHandle(), w.chromeDark, w.chromeBorder)
	}
}

// applyCaptureBackdrop 打开/关闭「记一笔」窗口背后的 DWM Acrylic 层，返回是否真的开着。
// 前端拿到 false 就把卡片画回不透明——半透明在没有 backdrop 的窗口里会被合到黑底上。
func (w *wiring) applyCaptureBackdrop(acrylic bool) bool {
	w.backdrop, w.hasBackdrop = acrylic, true
	if w.capture == nil {
		return false
	}
	on := false
	mygo.RunOnMain(func() {
		on = clip.SetWindowBackdrop(w.capture.NativeHandle(), acrylic)
	})
	return on
}

// applyHotkey (re)registers the quick-paste hotkey. 空串表示"不注册"；同一个加速键
// 重复调用是幂等的。
func (w *wiring) applyHotkey(accel string) error {
	if accel == w.hotkey {
		return nil
	}
	if w.hotkey != "" && mygo.GlobalShortcut.IsRegistered(w.hotkey) {
		mygo.GlobalShortcut.Unregister(w.hotkey)
	}
	w.hotkey = ""
	if accel == "" {
		return nil
	}
	if clip.HotkeyTaken(accel) {
		return fmt.Errorf("快捷键 %s 已被其它程序占用", accel)
	}
	if err := mygo.GlobalShortcut.Register(accel, func() {
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
	if w.captureHotkey != "" && mygo.GlobalShortcut.IsRegistered(w.captureHotkey) {
		mygo.GlobalShortcut.Unregister(w.captureHotkey)
	}
	w.captureHotkey = ""
	if accel == "" {
		return nil
	}
	if clip.HotkeyTaken(accel) {
		return fmt.Errorf("快捷键 %s 已被其它程序占用", accel)
	}
	if err := mygo.GlobalShortcut.Register(accel, func() {
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
	if w.shotHotkey != "" && mygo.GlobalShortcut.IsRegistered(w.shotHotkey) {
		mygo.GlobalShortcut.Unregister(w.shotHotkey)
	}
	w.shotHotkey = ""
	if accel == "" {
		return nil
	}
	// 先同步探一次：撞车要能当场报出来，而不是等用户以为"设置保存成功了"。
	if clip.HotkeyTaken(accel) {
		if accel == w.shotHotkeyBlocked {
			clip.Logf(w.dataDir, "shot hotkey still owned by another app: %s（不阻塞其它设置保存）", accel)
			return nil
		}
		return fmt.Errorf("快捷键 %s 已被其它程序占用", accel)
	}
	if err := mygo.GlobalShortcut.Register(accel, func() {
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
	mygo.App.Quit()
}

// ownWindow 报告前台窗口是不是 ClipNest 自己的（展开器不能在自己窗口里动作）。
func (w *wiring) ownWindow() bool {
	fg := clip.ForegroundWindowHandle()
	if fg == 0 {
		return false
	}
	for _, win := range []*mygo.Window{w.win, w.picker, w.capture, w.shot} {
		if win != nil && fg == win.NativeHandle() {
			return true
		}
	}
	return false
}

// createWindows 建主窗，并按需预建三个弹窗。
func (w *wiring) createWindows(preSettings clip.Settings, hidden bool) {
	w.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     "ClipNest 剪贴板",
		Width:     1120,
		Height:    740,
		MinWidth:  900,
		MinHeight: 560,
		Hidden:    hidden,
		// Frameless: the UI draws its own caption bar so the chrome matches the
		// content.
		Frameless: true,
		// 注意：DisableMove 会让 MyGo 忽略 SetPosition/SetBounds（v0.10.0），
		// 摆窗的窗口一律不设它（见 docs/design/MYGO-GATE0.md §6）。
		BackgroundColor: "#0a0b0f",
		URL:             "/",
	})
	// The whole point of a tray app: closing the window must not quit.
	w.win.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		w.win.Hide()
	})
	// 收起窗口时给前端递个信号：草稿的写库防抖在后端（internal/clip/draft.go），
	// 前端收到就立刻 Flush 一次。
	w.win.OnHide(func() { _ = evAppHidden.Broadcast("") })

	// 三个弹窗：默认"用到才建、闲置就销毁"（picker 除外，见 idle_windows.go）。
	if !w.idle.on() {
		w.ensurePicker()
		w.ensureCapture()
		w.ensureShot()
	} else {
		// picker 不参与闲置销毁，但也不必在启动瞬间就建：3 秒后再建，
		// 启动速度不受影响，第一次按热键也不会撞上"冷建"。
		time.AfterFunc(3*time.Second, func() { _ = w.ensurePicker() })
	}
}

// ensurePicker 建（或复用）快速粘贴窗：无边框、置顶、热键唤起、失焦自隐。
func (w *wiring) ensurePicker() *mygo.Window {
	if w.picker != nil {
		return w.picker
	}
	p := mygo.NewWindow(mygo.WindowOptions{
		Title:         "ClipNest",
		Width:         clip.PickerWidth,
		Height:        clip.PickerHeight,
		Frameless:     true,
		AlwaysOnTop:   true,
		Panel:         true,
		Transparent:   true,
		CornerRadius:  12,
		Hidden:        true,
		DisableResize: true,
		URL:           "/?mode=picker",
	})
	p.OnBlur(p.Hide) // HideOnFocusLost 的等价物
	p.OnClose(func(e *mygo.CloseEvent) {
		if w.idle.allowClose("picker") {
			if w.picker == p {
				w.picker = nil
			}
			return
		}
		e.PreventDefault()
		w.hidePicker()
	})
	p.OnHide(func() {
		w.retire("picker", p, func() {
			if w.picker == p {
				w.picker = nil
			}
		})
	})
	w.picker = p
	return p
}

// ensureCapture 建（或复用）「记一笔」浮层：可拖、可拉、尺寸记忆。
func (w *wiring) ensureCapture() *mygo.Window {
	if w.capture != nil {
		return w.capture
	}
	captureW, captureH := w.svc.CaptureSize()
	c := mygo.NewWindow(mygo.WindowOptions{
		Title:       "记一笔",
		Width:       captureW,
		Height:      captureH,
		MinWidth:    clip.CaptureMinWidth,
		MinHeight:   clip.CaptureMinHeight,
		Frameless:   true,
		AlwaysOnTop: true,
		Panel:       true,
		Transparent: true,
		Hidden:      true,
		URL:         "/?mode=capture",
	})
	c.OnBlur(c.Hide)
	c.OnClose(func(e *mygo.CloseEvent) {
		if w.idle.allowClose("capture") {
			if w.capture == c {
				w.capture = nil
			}
			return
		}
		e.PreventDefault()
		w.hideCapture()
	})
	c.OnHide(func() {
		w.retire("capture", c, func() {
			if w.capture == c {
				w.capture = nil
			}
		})
	})
	w.capture = c
	// 重建的窗口把上一次的配色/材质补回去（这两样原本只在设置变化时才刷）。
	if w.hasChrome {
		mygo.RunOnMain(w.applyChromeNow)
	}
	if w.hasBackdrop {
		mygo.RunOnMain(func() {
			clip.SetWindowBackdrop(c.NativeHandle(), w.backdrop)
		})
	}
	return c
}

// ensureShot 建（或复用）框选截图的遮罩窗。
//
// 建的时候给个小尺寸并隐藏：真正的铺满由 showShotWindow 用 SetWindowPos 按显示器
// 物理像素摆。不要开 OnBlur 隐藏——截图的整个过程里它就该一直是前台窗。
func (w *wiring) ensureShot() *mygo.Window {
	if w.shot != nil {
		return w.shot
	}
	s := mygo.NewWindow(mygo.WindowOptions{
		Title:         "ClipNest 截图",
		Width:         320,
		Height:        240,
		Frameless:     true,
		AlwaysOnTop:   true,
		Hidden:        true,
		DisableResize: true,
		BackgroundColor: "#08090c",
		URL:           "/?mode=shot",
	})
	// Esc / 点完立刻切走：遮罩窗不该被当成"用户手上的工作窗口"。
	s.OnClose(func(e *mygo.CloseEvent) {
		if w.idle.allowClose("shot") {
			return // 这是我们自己的闲置销毁，放行
		}
		e.PreventDefault()
		w.hideShotWindow()
	})
	w.shot = s
	return s
}

