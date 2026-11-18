package clip

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bbqisbbq/clipnest/internal/expand"
)

// Version is shown in the UI and written to the log on start.
const Version = "2.3.0"

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
	go s.updateCheckLoop()
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
	_ = s.store.Close()
}

func (s *ClipService) captureLoop() {
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	pending := false
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.watcher.Changes():
			pending = true
			debounce.Reset(45 * time.Millisecond)
		case <-debounce.C:
			if !pending {
				continue
			}
			pending = false
			s.handleClipboardChange()
		}
	}
}

// refreshFilesLoop keeps the "still exists?" flags of file references fresh.
func (s *ClipService) refreshFilesLoop() {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			for _, e := range s.store.ListEntries(EntryQuery{Kind: string(KindFiles), Limit: 500}) {
				if updated, ok := s.store.RefreshEntryFiles(e.ID); ok {
					s.emit("clip:updated", updated)
				}
			}
		}
	}
}

func (s *ClipService) emit(name string, data any) {
	if s.hooks.Emit != nil {
		s.hooks.Emit(name, data)
	}
}

func (s *ClipService) handleClipboardChange() {
	st := s.store.Settings()
	if !st.Capture {
		return
	}

	cap, err := captureClipboard()
	if err != nil || cap == nil {
		return
	}

	// Ignore the echo of our own copy-back.
	s.mu.Lock()
	suppressed := cap.Hash == s.suppressHash && time.Since(s.suppressAt) < 4*time.Second
	s.mu.Unlock()
	if suppressed {
		return
	}

	switch cap.Kind {
	case KindText:
		if !st.CaptureText {
			return
		}
	case KindLink:
		if !st.CaptureLink && !st.CaptureText {
			return
		}
	case KindImage:
		if !st.CaptureImage {
			return
		}
	case KindFiles:
		if !st.CaptureFiles {
			return
		}
	case KindRichText:
		if !st.CaptureRich {
			return
		}
	case KindCustom:
		if !st.CaptureCustom {
			return
		}
	}

	if st.MaxTextLength > 0 && (cap.Kind == KindText || cap.Kind == KindLink || cap.Kind == KindRichText) {
		if len([]rune(cap.Text)) > st.MaxTextLength {
			return
		}
	}

	entry, isNew, err := s.store.SaveCapture(*cap)
	if err != nil {
		return
	}
	// 不再自动清理：用户要求「以保留为主，手动删除」。Prune 保留实现但不再调用。

	if isNew {
		s.emit("clip:new", *entry)
	} else {
		s.emit("clip:bumped", *entry)
	}
	s.emit("clip:stats", s.store.Stats())
}

func (s *ClipService) shouldIgnore(cap *Captured, st Settings, patterns []*regexp.Regexp) bool {
	if len(patterns) == 0 {
		return false
	}
	haystacks := []string{cap.Text, cap.Preview}
	for _, f := range cap.Files {
		haystacks = append(haystacks, f.Name, f.Path)
	}
	for _, re := range patterns {
		for _, h := range haystacks {
			if h != "" && re.MatchString(h) {
				return true
			}
		}
	}
	return false
}

// ---- frontend API -------------------------------------------------------

// GetStats returns counters for the header / settings.
func (s *ClipService) GetStats() Stats { return s.store.Stats() }

// GetImageData returns a data: URL for an entry whose body references an image.
func (s *ClipService) GetImageData(id string) (string, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return "", errors.New("条目不存在")
	}
	token := memoImageToken(e.Content)
	if token == "" {
		return "", errors.New("没有图像数据")
	}
	payload, mime, err := s.store.LoadEntryBlob(token)
	if err != nil {
		return "", err
	}
	if mime == "" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + encodeBase64(payload), nil
}

// CopyEntry writes an entry back to the Windows clipboard.
func (s *ClipService) CopyEntry(id string) error {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return errors.New("条目不存在")
	}
	if err := s.writeEntryToClipboard(e); err != nil {
		return err
	}
	_ = s.store.BumpCopyCount(id)
	s.emit("clip:stats", s.store.Stats())
	return nil
}

// writeEntryToClipboard puts an entry on the clipboard and suppresses the
// capture echo that the listener would otherwise turn into a new entry.
func (s *ClipService) writeEntryToClipboard(e Entry) error {
	s.mu.Lock()
	s.suppressHash = e.Hash
	s.suppressAt = time.Now()
	s.mu.Unlock()

	switch e.Kind {
	case KindImage:
		token := memoImageToken(e.Content)
		data, _, err := s.store.LoadEntryBlob(token)
		if err != nil {
			return err
		}
		return WriteImageToClipboard(data)
	case KindFiles:
		paths := make([]string, 0, len(e.Files))
		for _, f := range e.Files {
			paths = append(paths, f.Path)
		}
		return WriteFilesToClipboard(paths)
	case KindRichText:
		if e.RTF != "" {
			return WriteRTFToClipboard(e.RTF, e.Content)
		}
		return WriteHTMLToClipboard(e.HTML, e.Content)
	case KindCustom:
		data, _, err := s.store.LoadEntryBlob(memoImageToken(e.Content))
		if err != nil {
			return fmt.Errorf("「%s」的原始数据已不可用", e.FormatName)
		}
		return WriteRawToClipboard(e.FormatID, e.FormatName, data)
	default:
		return WriteTextToClipboard(e.Content)
	}
}

// DeleteAll removes every non-pinned entry and reports how many went away.
func (s *ClipService) DeleteAll() (int, error) {
	n := s.store.DeleteAllUnpinned()
	s.emit("clip:cleared", n)
	s.emit("clip:stats", s.store.Stats())
	return n, nil
}

// OpenEntry launches an entry with its default handler.
func (s *ClipService) OpenEntry(id string) error {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return errors.New("条目不存在")
	}
	switch e.Kind {
	case KindLink:
		return shellOpen(e.Content)
	case KindFiles:
		if len(e.Files) == 0 {
			return errors.New("没有文件")
		}
		return revealInExplorer(e.Files[0].Path)
	case KindImage:
		data, _, err := s.store.LoadEntryBlob(memoImageToken(e.Content))
		if err != nil {
			return err
		}
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("clipnest-%s.png", e.ID))
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		return shellOpen(tmp)
	case KindCustom:
		return fmt.Errorf("「%s」是自定义剪贴板格式，只能复制回剪贴板", e.FormatName)
	default:
		return errors.New("该类型不支持直接打开")
	}
}

// GetSettings returns the current settings.
func (s *ClipService) GetSettings() Settings { return s.store.Settings() }

// SaveSettings persists settings, applies side effects and returns what was
// actually stored.
func (s *ClipService) SaveSettings(st Settings) (Settings, error) {
	prev := s.store.Settings()
	if st.MaxItems <= 0 {
		st.MaxItems = 1000
	}
	if st.Hotkey == "" {
		st.Hotkey = prev.Hotkey
	}
	// 可同步的字段变了才 bump 时间戳——否则设置永远同步不出去，
	// 或者每次保存都无谓地覆盖别的机器。
	if shareableStamp(st) != shareableStamp(prev) {
		st.SyncUpdatedAt = nowUTC()
	}
	if err := s.store.SaveSettings(st); err != nil {
		return prev, err
	}
	if prev.AutoStart != st.AutoStart {
		if err := setAutoStart(st.AutoStart, s.hooks.ExePath); err != nil {
			return s.store.Settings(), fmt.Errorf("设置开机启动失败: %w", err)
		}
	}
	if s.hooks.OnSettings != nil {
		if err := s.hooks.OnSettings(st); err != nil {
			return s.store.Settings(), err
		}
	}
	// 不再自动清理：用户要求「以保留为主，手动删除」。Prune 保留实现但不再调用。
	go s.syncLoop()
	go s.updateCheckLoop()
	s.refreshTriggers()
	s.applyExpander(s.store.Settings())
	s.emit("settings:changed", st)
	s.emit("clip:stats", s.store.Stats())
	return s.store.Settings(), nil
}

// applyExpander pushes trigger settings into the expansion engine.
func (s *ClipService) applyExpander(st Settings) {
	suffix := strings.TrimSpace(st.ExpandSuffix)
	if suffix == "" {
		suffix = "-"
	}
	endChars := st.ExpandEndChars
	if endChars == "" {
		endChars = DefaultEndChars
	}
	err := s.engine.Configure(expand.Config{
		Enabled:   st.ExpandEnabled,
		Suffix:    suffix,
		EndChars:  endChars,
		Debug:     false,
		IdleReset: 2 * time.Second,
		Logf:      func(format string, args ...any) { Logf(s.store.Dir(), format, args...) },
	})
	if err != nil {
		Logf(s.store.Dir(), "expander start failed: %v", err)
		return
	}
	if st.ExpandEnabled {
		Logf(s.store.Dir(), "expander enabled (suffix=%q endChars=%q, %d triggers)",
			suffix, endChars, s.triggerCount())
	}
}

// triggerCount reports how many triggers are loaded (for diagnostics).
func (s *ClipService) triggerCount() int {
	trigMu.RLock()
	defer trigMu.RUnlock()
	return len(trigIndex.folded)
}

// SetCapture is the tray's pause/resume switch.
func (s *ClipService) SetCapture(enabled bool) (Settings, error) {
	st := s.store.Settings()
	st.Capture = enabled
	return s.SaveSettings(st)
}

// SetAutoStart toggles the logon entry (tray menu / settings both use it).
func (s *ClipService) SetAutoStart(enabled bool) (Settings, error) {
	st := s.store.Settings()
	st.AutoStart = enabled
	return s.SaveSettings(st)
}

// IsAutoStartEnabled reports the actual registry state, not just the setting.
func (s *ClipService) IsAutoStartEnabled() bool {
	if !s.hooks.PlatformWorks {
		return false
	}
	return autoStartEnabled()
}

// ReconcileAutoStart makes the Run registry entry match the stored setting,
// repairing a stale path after the app was moved or reinstalled. Called once at
// startup so a moved installation doesn't silently break autostart.
//
// A pre-existing entry that ClipNest did not write itself (for example, the one
// the installer creates when the user ticks "开机自动启动") is adopted rather
// than deleted: the installer's choice wins on first run. An entry pointing at a
// binary that no longer exists (a moved or deleted build) is *repaired*, never
// adopted — adopting a dead path is how a stale debug build keeps hijacking
// logon.
func (s *ClipService) ReconcileAutoStart() {
	if !s.hooks.PlatformWorks || s.hooks.ExePath == "" {
		return
	}
	st := s.store.Settings()
	current := autoStartCommandValue()
	registered := exePathFromCommand(current)
	registeredOK := registered != "" && fileExists(registered)
	self := filepath.Clean(s.hooks.ExePath)
	sameExe := registered != "" && strings.EqualFold(filepath.Clean(registered), self)

	Logf(s.store.Dir(), "autostart: registry=%q (exists=%v) settings=%v exe=%s",
		current, registeredOK, st.AutoStart, self)

	register := func(reason string) {
		if err := setAutoStart(true, s.hooks.ExePath); err != nil {
			Logf(s.store.Dir(), "autostart reconcile failed (%s): %v", reason, err)
			return
		}
		Logf(s.store.Dir(), "autostart registered (%s): %s", reason, autoStartCommand(s.hooks.ExePath))
	}

	switch {
	case current == "":
		if st.AutoStart {
			register("missing entry")
		}

	case !registeredOK:
		// Dead path: repoint if the user wants autostart, otherwise clean up.
		if st.AutoStart {
			register("stale path " + registered)
		} else if err := setAutoStart(false, ""); err == nil {
			Logf(s.store.Dir(), "autostart removed stale entry: %q", current)
		}

	case st.AutoStart && !sameExe:
		register("points at another build")

	case !st.AutoStart:
		// Adopt the installer's choice.
		st.AutoStart = true
		if err := s.store.SaveSettings(st); err != nil {
			Logf(s.store.Dir(), "autostart adopt failed: %v", err)
			return
		}
		Logf(s.store.Dir(), "autostart adopted from registry: %q", current)
		s.emit("settings:changed", st)
	}
}

// exePathFromCommand pulls the executable path out of a Run value like
// `"C:\Program Files\X\app.exe" --background`.
func exePathFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.Index(cmd[1:], `"`); end >= 0 {
			return cmd[1 : end+1]
		}
		return strings.TrimPrefix(cmd, `"`)
	}
	if i := strings.Index(strings.ToLower(cmd), ".exe"); i >= 0 {
		return cmd[:i+len(".exe")]
	}
	return ""
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := osStat(path)
	return err == nil
}


// AutoStartTarget is the command line that would be registered, for the UI to
// show what "开机自启" actually points at.
func (s *ClipService) AutoStartTarget() string {
	if cmd := autoStartCommandValue(); cmd != "" {
		return cmd
	}
	if s.hooks.ExePath == "" {
		return ""
	}
	return autoStartCommand(s.hooks.ExePath)
}

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
func (s *ClipService) GetVersion() string { return Version }

// ---- picker (hotkey popup) ---------------------------------------------

// PickerSize is the popup's logical size; main.go uses it for clamping.
const (
	PickerWidth  = 468
	PickerHeight = 424
)

// CaptureSize is the quick-capture popup: a one-box editor for a memo.
const (
	CaptureWidth  = 620
	CaptureHeight = 220
)

// ShowPicker pops the list at the caret of whatever window has focus.
func (s *ClipService) ShowPicker() error {
	if s.hooks.ShowPickerAt == nil {
		return errors.New("弹窗窗口未就绪")
	}
	// Capture the target window before our popup steals focus.
	s.mu.Lock()
	s.targetHwnd = ForegroundWindowHandle()
	target := s.targetHwnd
	s.mu.Unlock()
	if target == 0 {
		return errors.New("没有可粘贴的目标窗口")
	}
	x, y := CaretAnchor()
	ok := s.hooks.ShowPickerAt(x, y)
	Logf(s.store.Dir(), "picker show: target=%#x anchor=(%d,%d) shown=%v", target, x, y, ok)
	if !ok {
		return errors.New("无法显示弹窗")
	}
	return nil
}

// HidePicker dismisses the popup and gives focus back to the target window.
func (s *ClipService) HidePicker() {
	if s.hooks.HidePicker != nil {
		s.hooks.HidePicker()
	}
	s.mu.Lock()
	target := s.targetHwnd
	s.mu.Unlock()
	if target != 0 {
		go ActivateWindow(target)
	}
}

// PickItem copies an entry and, when paste is true, pastes it into the window
// that was focused when the popup opened. It returns a short status string the
// popup can show while it closes.
func (s *ClipService) PickEntry(id string, paste bool) (string, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return "", errors.New("条目不存在")
	}
	s.mu.Lock()
	target := s.targetHwnd
	s.mu.Unlock()
	if target == 0 {
		target = ForegroundWindowHandle()
	}

	// 先放回剪贴板（保留格式），需要时再合成 Ctrl+V
	if err := s.writeEntryToClipboard(e); err != nil {
		return "", err
	}
	if paste && s.store.Settings().PasteOnPick {
		s.pasteAndLog(target, "entry")
	}
	_ = s.store.BumpCopyCount(e.ID)
	s.emit("clip:stats", s.store.Stats())
	return e.Content, nil
}

// PickerHotkey returns the accelerator that opens the picker ("" = disabled).
func (s *ClipService) PickerHotkey() string { return s.store.Settings().Hotkey }

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

// ShowCapture pops the quick-capture window at the caret so a memo can be typed
// without leaving whatever you were doing.
func (s *ClipService) ShowCapture() error {
	if s.hooks.ShowCaptureAt == nil {
		return errors.New("快速输入窗口未就绪")
	}
	s.mu.Lock()
	s.targetHwnd = ForegroundWindowHandle()
	target := s.targetHwnd
	s.mu.Unlock()

	x, y := CaretAnchor()
	if !s.hooks.ShowCaptureAt(x, y) {
		return errors.New("无法显示快速输入窗口")
	}
	Logf(s.store.Dir(), "capture show: target=%#x anchor=(%d,%d)", target, x, y)
	return nil
}

// CloseCapture hides the quick-capture window and gives focus back.
func (s *ClipService) CloseCapture() {
	if s.hooks.HideCapture != nil {
		s.hooks.HideCapture()
	}
	s.mu.Lock()
	target := s.targetHwnd
	s.mu.Unlock()
	if target != 0 {
		go ActivateWindow(target)
	}
}

// CaptureHotkey returns the accelerator that opens the quick-capture popup.
func (s *ClipService) CaptureHotkey() string { return s.store.Settings().CaptureHotkey }

// EnsureCaptureHotkey repairs a profile whose captureHotkey ended up empty
// because an older build shipped no default for it. Runs once (meta marker), so
// a later deliberate clear is respected.
func (s *ClipService) EnsureCaptureHotkey() {
	if s.store.Meta("capture.hotkeyInit") != "" {
		return
	}
	st := s.store.Settings()
	if st.CaptureHotkey == "" {
		st.CaptureHotkey = DefaultSettings().CaptureHotkey
		if err := s.store.SaveSettings(st); err != nil {
			Logf(s.store.Dir(), "capture hotkey heal failed: %v", err)
		}
	}
	_ = s.store.SetMeta("capture.hotkeyInit", "1")
}
