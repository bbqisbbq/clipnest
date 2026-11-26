package clip

import (
	"context"
	"encoding/base64"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bbqisbbq/clipnest/internal/embed"
	"github.com/bbqisbbq/clipnest/internal/expand"
)

// 服务装配与生命周期：ClipService/Hooks/AppInfo 的定义、打开数据目录、启动与关闭，
// 以及窗口、版本、日志、主题这类应用级入口。
// 不负责：采集管线（service_capture.go）、条目与剪贴板交互（service_paste.go）、
// 设置与开机自启（service_settings.go）。

// Version is shown in the UI and written to the log on start.
const Version = "2.13.0"

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
	// ShowShotWindow shows the screenshot overlay window over the frozen frame and
	// reports whether it could be shown. Implemented by main.go (it owns the window).
	ShowShotWindow func() bool
	// HideShotWindow hides the screenshot overlay window.
	HideShotWindow func()
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
	// PP-OCR 引擎（见 ocr_engine.go）：懒加载，模型只加载一次。
	ocrPPOCR ocrEngineState
	// 资源下载的互斥标记：同一次只允许一个下载在跑（见 ocr_assets.go）。
	ocrDownloading atomic.Bool
	// 语义模型的下载互斥（见 embed_assets.go），和 OCR 那两个下载可以并行。
	embedDownloading atomic.Bool

	// bg 收集所有后台循环，bgCtx 让正在跑的 HTTP 请求随退出一起取消。
	// Shutdown 先等它们收尾再关库，否则同步会对着已关闭的 *sql.DB 写。
	bg       sync.WaitGroup
	bgCtx    context.Context
	bgCancel context.CancelFunc
	// targetHwnd is the window that had focus when the picker was opened; it is
	// where the pasted content must land.
	targetHwnd uintptr

	// 截图（见 service_shot.go）：shot 是"已经拍好、等着被裁"的那一帧（nil = 没有待裁的帧），
	// shotSourceApp 是按快捷键时前台程序的 exe 名（截图条目的来源）。
	// 两者都由 mu 保护。
	shot          *ShotFrame
	shotSourceApp string

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

	// 未保存草稿：**两个编辑器各一个槽**（见 draft.go）。前端每敲一个字送到
	// slot.pending，slot.timer 防抖合并成一次 SQLite 写。
	//
	// 分成两个是因为「记一笔」那个窗口闲置会被销毁（idle.go），页面连同它的
	// React state 一起没了 —— 那份草稿不落库就等于关一次窗丢一次稿。
	composerDraft *draftSlot
	captureDraft  *draftSlot

	// 语义向量（见 embed_worker.go）：队列 + 懒加载的引擎 + 闲置释放定时器。
	//
	// 引擎**不常驻**：会话驻留 +43MB，而内存预算只有 120~180MB。用到才加载，
	// 闲置 embedIdleRelease 之后把会话销毁（下次用再付 0.25 秒冷加载）。
	embedOnce sync.Once
	embedEng  *embed.Engine
	embedQueue chan string
	embedMu    sync.Mutex
	embedIdle  *time.Timer
}

// NewService opens the store inside dataDir.
func NewClipService(dataDir string, hooks Hooks) (*ClipService, error) {
	st, err := Open(dataDir)
	if err != nil {
		return nil, err
	}
	s := &ClipService{
		store:         st,
		hooks:         hooks,
		stopCh:        make(chan struct{}),
		composerDraft: newDraftSlot(metaComposerDraft, metaComposerDraftAt),
		captureDraft:  newDraftSlot(metaCaptureDraft, metaCaptureDraftAt),
	}
	s.bgCtx, s.bgCancel = context.WithCancel(context.Background())
	s.engine = expand.New(s)
	// 把「上次同步」从库里读回内存：否则一重启设置页就回到「从未同步」+ 1970 时间戳。
	s.loadSyncStatus()
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
	// 同理：截图是后加的功能，老 profile 里没有 shotHotkey，补上默认值并注册
	s.EnsureShotDefaults()
	// 后台循环先起来：剪贴板监听失败（消息窗被别的程序挡住这类极端情况）不该
	// 连带把同步、更新检查、缩写展开一起掐掉 —— 那会让程序"看起来正常但什么都不做"。
	s.spawn(s.refreshFilesLoop)
	s.spawn(s.syncLoop)
	s.startOCRWorker()
	s.startEmbedWorker()
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
	// 退出前把防抖窗口里的草稿写掉：先停掉两个定时器（免得它们在 store.Close 之后
	// 再触发），再同步写一次。这一步必须早于 store.Close。
	//
	// 两个槽都要写：「记一笔」的草稿和主窗口的一样重要——它连窗口都会被销毁，
	// 库里的那一份是唯一副本。
	stopDraftTimer(s.composerDraft)
	stopDraftTimer(s.captureDraft)
	// 语义模型的会话也在这时候放掉：43MB，别带到进程退出前
	s.stopEmbedIdleTimer()
	if s.embedEng != nil {
		s.embedEng.Close()
	}
	if err := s.flushDraft(s.composerDraft); err != nil {
		Logf(s.store.Dir(), "draft flush on shutdown failed (composer): %v", err)
	}
	if err := s.flushDraft(s.captureDraft); err != nil {
		Logf(s.store.Dir(), "draft flush on shutdown failed (capture): %v", err)
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


















