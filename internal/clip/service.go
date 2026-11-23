package clip

import (
	"context"
	"encoding/base64"
	"sync"
	"time"

	"github.com/bbqisbbq/clipnest/internal/expand"
)

// 服务装配与生命周期：ClipService/Hooks/AppInfo 的定义、打开数据目录、启动与关闭，
// 以及窗口、版本、日志、主题这类应用级入口。
// 不负责：采集管线（service_capture.go）、条目与剪贴板交互（service_paste.go）、
// 设置与开机自启（service_settings.go）。

// Version is shown in the UI and written to the log on start.
const Version = "2.9.4"

// AppInfo is static information the frontend needs for the about/settings view.
type AppInfo struct {
	Version      string `json:"version"`
	DataDir      string `json:"dataDir"`
	ExePath      string `json:"exePath"`
	OS           string `json:"os"`
	AutoStart    bool   `json:"autoStart"`
	PlatformOK   bool   `json:"platformOk"`
	PlatformNote string `json:"platformNote"`
	// AutoStartTarget is the command line the logon entry points at.
	AutoStartTarget string `json:"autoStartTarget"`
}

// Hooks lets main.go wire the service to application-level concerns (window
// visibility, the picker popup, quitting, tray menu labels) without importing
// Wails here.
type Hooks struct {
	Emit          func(name string, data any)
	ShowWindow    func()
	HideWindow    func()
	Quit          func()
	OnSettings    func(Settings) error
	ExePath       string
	PlatformNote  string
	PlatformWorks bool

	// ShowPickerAt shows the popup window anchored at the given screen point and
	// reports whether it could be shown.
	ShowPickerAt func(x, y int) bool
	// HidePicker hides the popup window.
	HidePicker func()
	// ShowCaptureAt shows the quick-capture popup at the given screen point.
	ShowCaptureAt func(x, y int) bool
	// HideCapture hides the quick-capture popup.
	HideCapture func()
	// ApplyChrome paints the native window parts (borders, caption) to match the
	// current theme. Implemented by main.go because only it knows the window handles.
	ApplyChrome func(dark bool, borderHex string)
	// ApplyBackdrop switches the quick-capture window's DWM backdrop (Acrylic) on
	// or off and reports whether it is now on. Implemented by main.go for the same
	// reason as ApplyChrome. See backdrop_windows.go for why半透明 needs it.
	ApplyBackdrop func(acrylic bool) bool
	// OpenURL opens a link in the default browser.
	OpenURL func(url string) error
}

// ClipService is the object bound to the webview.
type ClipService struct {
	store   *Store
	watcher *Watcher
	hooks   Hooks

	mu           sync.Mutex
	suppressHash string
	suppressAt   time.Time
	// suppressAllUntil 是程序化写剪贴板之后的短窗口：写 HTML/RTF（图文混排回填）时
	// 哈希口径对不上，这段时间内的采集回声一律丢弃。见 service_capture.go。
	suppressAllUntil time.Time
	stopCh           chan struct{}
	stopped          bool

	// OCR 队列（见 ocr_worker.go）：图片条目入队后由单个 worker 串行识别。
	ocrQueue chan string
	ocrOnce  sync.Once

	// bg 收集所有后台循环，bgCtx 让正在跑的 HTTP 请求随退出一起取消。
	// Shutdown 先等它们收尾再关库，否则同步会对着已关闭的 *sql.DB 写。
	bg       sync.WaitGroup
	bgCtx    context.Context
	bgCancel context.CancelFunc
	// targetHwnd is the window that had focus when the picker was opened; it is
	// where the pasted content must land.
	targetHwnd uintptr

	// engine is the inline-trigger engine (internal/expand); it owns the keyboard
	// hook and asks this service for content through expand.Host.
	engine *expand.Engine
	// ownWindow reports whether one of our own windows is focused.
	ownWindow func() bool

	// syncMu guards the sync status; syncRestart serialises loop restarts.
	syncMu      sync.Mutex
	syncStatus  SyncStatus
	syncRestart sync.Mutex
	syncCancel  context.CancelFunc

	// updateRestart/updateCancel 同理：更新检查只允许有一个待执行的 goroutine，
	// 设置变更时取消重排（见 update.go 的 restartUpdateCheck）。
	updateRestart sync.Mutex
	updateCancel  context.CancelFunc

	// 快捷输入草稿：前端每敲一个字送到 draftPending，draftTimer 防抖合并成一次
	// SQLite 写（见 draft.go）。Shutdown 时显式 flush，退出不丢最后几个字。
	draftMu      sync.Mutex
	draftPending string
	draftDirty   bool
	draftTimer   *time.Timer
}

// NewService opens the store inside dataDir.
func NewClipService(dataDir string, hooks Hooks) (*ClipService, error) {
	st, err := Open(dataDir)
	if err != nil {
		return nil, err
	}
	s := &ClipService{
		store:  st,
		hooks:  hooks,
		stopCh: make(chan struct{}),
	}
	s.bgCtx, s.bgCancel = context.WithCancel(context.Background())
	s.engine = expand.New(s)
	return s, nil
}

// spawn runs a background loop that Shutdown waits for.
func (s *ClipService) spawn(fn func()) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		fn()
	}()
}

// Start begins listening for clipboard updates.
func (s *ClipService) Start() error {
	// 老 profile 的「记一笔」热键自愈（修完会重新注册，见 EnsureCaptureHotkey）
	s.EnsureCaptureHotkey()
	// 后台循环先起来：剪贴板监听失败（消息窗被别的程序挡住这类极端情况）不该
	// 连带把同步、更新检查、缩写展开一起掐掉 —— 那会让程序"看起来正常但什么都不做"。
	s.spawn(s.refreshFilesLoop)
	s.spawn(s.syncLoop)
	s.startOCRWorker()
	s.restartUpdateCheck()
	s.refreshTriggers()
	s.applyExpander(s.store.Settings())

	w, err := StartWatcher()
	if err != nil {
		Logf(s.store.Dir(), "clipboard watcher start failed: %v", err)
		s.emit("capture:opened", "监听失败")
		return err
	}
	s.watcher = w
	s.spawn(s.captureLoop)
	// Whatever is already on the clipboard when the app starts is also part of
	// "what went through the clipboard", so record it once.
	s.spawn(func() {
		select {
		case <-s.stopCh:
			return
		case <-time.After(400 * time.Millisecond):
		}
		s.handleClipboardChange()
	})
	return nil
}

// Shutdown stops listening and flushes the store.
func (s *ClipService) Shutdown() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	close(s.stopCh)
	s.mu.Unlock()

	if s.watcher != nil {
		s.watcher.Stop()
	}
	s.syncRestart.Lock()
	if s.syncCancel != nil {
		s.syncCancel()
		s.syncCancel = nil
	}
	s.syncRestart.Unlock()
	s.updateRestart.Lock()
	if s.updateCancel != nil {
		s.updateCancel()
		s.updateCancel = nil
	}
	s.updateRestart.Unlock()
	// 退出前把防抖窗口里的草稿写掉：先停掉定时器（免得它在 store.Close 之后再触发），
	// 再同步写一次。这一步必须早于 store.Close。
	s.draftMu.Lock()
	if s.draftTimer != nil {
		s.draftTimer.Stop()
		s.draftTimer = nil
	}
	s.draftMu.Unlock()
	if err := s.flushComposerDraft(); err != nil {
		Logf(s.store.Dir(), "draft flush on shutdown failed: %v", err)
	}

	// 取消在途请求，再等后台循环收尾：同步正写到一半就关库，会刷出
	// "database is closed" 并可能中断一次 merge。
	s.bgCancel()
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// 还有循环卡着（理论上不该有）：进程马上就退出，WAL 是崩溃安全的，
		// 不干净地 Close 也好过让它们对着已关闭的库写。
		Logf(s.store.Dir(), "shutdown: background loops still running after 5s, leaving db open")
		return
	}
	_ = s.store.Close()
}

// ---- frontend API -------------------------------------------------------

// GetStats returns counters for the header / settings.
func (s *ClipService) GetStats() Stats { return s.store.Stats() }

// GetAppInfo returns version/paths for the about panel.
func (s *ClipService) GetAppInfo() AppInfo {
	autoStart := false
	target := ""
	if s.hooks.PlatformWorks {
		autoStart = autoStartEnabled()
		target = s.AutoStartTarget()
	}
	return AppInfo{
		Version:         Version,
		DataDir:         s.store.Dir(),
		ExePath:         s.hooks.ExePath,
		OS:              "windows",
		AutoStart:       autoStart,
		PlatformOK:      s.hooks.PlatformWorks,
		PlatformNote:    s.hooks.PlatformNote,
		AutoStartTarget: target,
	}
}

// OpenDataDir reveals the data directory in Explorer.
func (s *ClipService) OpenDataDir() error { return revealInExplorer(s.store.Dir()) }

// ShowWindow brings the manager window back from the tray.
func (s *ClipService) ShowWindow() {
	if s.hooks.ShowWindow != nil {
		s.hooks.ShowWindow()
	}
}

// HideWindow hides the manager window to the tray.
func (s *ClipService) HideWindow() {
	if s.hooks.HideWindow != nil {
		s.hooks.HideWindow()
	}
}

// Quit exits the application (tray menu "退出" uses the same path).
func (s *ClipService) Quit() {
	if s.hooks.Quit != nil {
		s.hooks.Quit()
	}
}

// Version returns the app version as a string method too (handy in JS).

// LogClient records a frontend error/note in clipnest.log, so a blank window or
// a broken binding can be diagnosed from outside the webview.
func (s *ClipService) LogClient(message string) {
	if len(message) > 4000 {
		message = message[:4000] + "…"
	}
	Logf(s.store.Dir(), "ui: %s", message)
}

// encodeBase64 is shared by the store's data URL helper.
func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// SetWindowChrome is called by the UI whenever the theme changes, so the native
// window border follows the palette instead of staying system-default.
func (s *ClipService) SetWindowChrome(dark bool, borderHex string) {
	if s.hooks.ApplyChrome != nil {
		s.hooks.ApplyChrome(dark, borderHex)
	}
}

// SetCaptureBackdrop 由快速输入窗自己调用：卡片要半透明时先请求一层 DWM backdrop，
// 否则 WebView2 会把半透明像素合到黑底上（卡片反而更暗）。返回值 = 现在是否真的
// 有这层背景；false 时前端必须把卡片画成不透明，别硬透。
func (s *ClipService) SetCaptureBackdrop(enabled bool) bool {
	if s.hooks.ApplyBackdrop == nil {
		return false
	}
	return s.hooks.ApplyBackdrop(enabled)
}
