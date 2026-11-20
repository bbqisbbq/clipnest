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
const Version = "2.8.1"

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
	stopCh       chan struct{}
	stopped      bool
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
	s.engine = expand.New(s)
	return s, nil
}

// Start begins listening for clipboard updates.
func (s *ClipService) Start() error {
	w, err := StartWatcher()
	if err != nil {
		return err
	}
	s.watcher = w
	go s.captureLoop()
	go s.refreshFilesLoop()
	go s.syncLoop()
	s.restartUpdateCheck()
	s.EnsureCaptureHotkey()
	s.refreshTriggers()
	s.applyExpander(s.store.Settings())
	// Whatever is already on the clipboard when the app starts is also part of
	// "what went through the clipboard", so record it once.
	go func() {
		time.Sleep(400 * time.Millisecond)
		s.handleClipboardChange()
	}()
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
