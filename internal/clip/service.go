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
)

// Version is shown in the UI and written to the log on start.
const Version = "0.6.0"

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
	ignoreRe     []*regexp.Regexp
	stopCh       chan struct{}
	stopped      bool
	// targetHwnd is the window that had focus when the picker was opened; it is
	// where the pasted content must land.
	targetHwnd uintptr

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
	s.compileIgnorePatterns(s.store.Settings())
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
	s.refreshTriggerIndex()
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
			items := s.store.List(Filter{Kind: string(KindFiles), Limit: 500})
			for _, it := range items {
				if updated, ok := s.store.RefreshFiles(it.ID); ok {
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

func (s *ClipService) compileIgnorePatterns(st Settings) {
	out := make([]*regexp.Regexp, 0, len(st.IgnorePatterns))
	for _, p := range st.IgnorePatterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if re, err := regexp.Compile(p); err == nil {
			out = append(out, re)
		}
	}
	s.mu.Lock()
	s.ignoreRe = out
	s.mu.Unlock()
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
	patterns := s.ignoreRe
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
	if s.shouldIgnore(cap, st, patterns) {
		return
	}

	item, isNew, err := s.store.Add(*cap)
	if err != nil {
		return
	}
	s.store.Prune(st.MaxItems, st.MaxAgeDays)

	if isNew {
		s.emit("clip:new", *item)
	} else {
		s.emit("clip:bumped", *item)
	}
	s.emit("clip:stats", s.store.Stats())
}

func (s *ClipService) shouldIgnore(cap *Captured, st Settings, patterns []*regexp.Regexp) bool {
	app := strings.ToLower(cap.SourceApp)
	for _, ignore := range st.IgnoreApps {
		ignore = strings.ToLower(strings.TrimSpace(ignore))
		if ignore != "" && app == ignore {
			return true
		}
	}
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

// GetItems returns the history page for the UI.
func (s *ClipService) GetItems(filter Filter) ([]Item, error) {
	return s.store.List(filter), nil
}

// GetItem returns a single entry (including refreshed file state).
func (s *ClipService) GetItem(id string) (Item, error) {
	it, ok := s.store.Get(id)
	if !ok {
		return Item{}, errors.New("条目不存在")
	}
	if it.Kind == KindFiles {
		if refreshed, ok := s.store.RefreshFiles(id); ok {
			return refreshed, nil
		}
	}
	return it, nil
}

// GetStats returns counters for the header.
func (s *ClipService) GetStats() Stats { return s.store.Stats() }

// GetImageData returns a data: URL for an image entry.
func (s *ClipService) GetImageData(id string) (string, error) {
	it, ok := s.store.Get(id)
	if !ok || it.ImageFile == "" {
		return "", errors.New("没有图像数据")
	}
	return s.store.ReadBlobData(it.ImageFile)
}

// CopyItem writes an entry back to the Windows clipboard.
func (s *ClipService) CopyItem(id string) error {
	it, ok := s.store.Get(id)
	if !ok {
		return errors.New("条目不存在")
	}
	if err := s.writeToClipboard(it); err != nil {
		return err
	}
	s.store.MarkCopied(id)
	s.emit("clip:stats", s.store.Stats())
	return nil
}

// writeToClipboard puts an entry on the clipboard and suppresses the capture
// echo that the clipboard listener would otherwise turn into a new entry.
func (s *ClipService) writeToClipboard(it Item) error {
	s.mu.Lock()
	s.suppressHash = it.Hash
	s.suppressAt = time.Now()
	s.mu.Unlock()

	switch it.Kind {
	case KindImage:
		data, err := s.store.LoadBlobByName(it.ImageFile)
		if err != nil {
			return err
		}
		return WriteImageToClipboard(data)
	case KindFiles:
		paths := make([]string, 0, len(it.Files))
		for _, f := range it.Files {
			paths = append(paths, f.Path)
		}
		return WriteFilesToClipboard(paths)
	case KindRichText:
		if it.RTF != "" {
			return WriteRTFToClipboard(it.RTF, it.Text)
		}
		return WriteHTMLToClipboard(it.HTML, it.Text)
	case KindCustom:
		data, err := s.store.LoadBlobByName(it.BinaryFile)
		if err != nil {
			return err
		}
		return WriteRawToClipboard(it.FormatID, it.FormatName, data)
	default:
		return WriteTextToClipboard(it.Text)
	}
}

// DeleteItem removes one entry.
func (s *ClipService) DeleteItem(id string) error {
	if !s.store.Delete(id) {
		return errors.New("条目不存在")
	}
	s.emit("clip:deleted", id)
	s.emit("clip:stats", s.store.Stats())
	return nil
}

// DeleteWhere removes non-pinned entries matching the filter and reports how
// many disappeared.
func (s *ClipService) DeleteWhere(filter Filter) (int, error) {
	n := s.store.DeleteWhere(filter)
	s.emit("clip:cleared", n)
	s.emit("clip:stats", s.store.Stats())
	return n, nil
}

// SetPinned toggles the favourite flag.
func (s *ClipService) SetPinned(id string, pinned bool) (Item, error) {
	it, ok := s.store.SetPinned(id, pinned)
	if !ok {
		return Item{}, errors.New("条目不存在")
	}
	return it, nil
}

// RefreshItem re-checks a file entry's references.
func (s *ClipService) RefreshItem(id string) (Item, error) {
	if it, ok := s.store.RefreshFiles(id); ok {
		return it, nil
	}
	return Item{}, errors.New("条目不存在")
}

// OpenItem launches the entry with its default handler (link in browser, file
// in Explorer, image in the default viewer).
func (s *ClipService) OpenItem(id string) error {
	it, ok := s.store.Get(id)
	if !ok {
		return errors.New("条目不存在")
	}
	switch it.Kind {
	case KindLink:
		return shellOpen(it.Text)
	case KindFiles:
		if len(it.Files) == 0 {
			return errors.New("没有文件")
		}
		return revealInExplorer(it.Files[0].Path)
	case KindImage:
		data, err := s.store.LoadBlobByName(it.ImageFile)
		if err != nil {
			return err
		}
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("clipnest-%s.png", it.ID))
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		return shellOpen(tmp)
	case KindCustom:
		return fmt.Errorf("「%s」是自定义剪贴板格式，只能复制回剪贴板", it.FormatName)
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
	if err := s.store.SaveSettings(st); err != nil {
		return prev, err
	}
	if prev.AutoStart != st.AutoStart {
		if err := setAutoStart(st.AutoStart, s.hooks.ExePath); err != nil {
			return s.store.Settings(), fmt.Errorf("设置开机启动失败: %w", err)
		}
	}
	s.compileIgnorePatterns(st)
	if s.hooks.OnSettings != nil {
		if err := s.hooks.OnSettings(st); err != nil {
			return s.store.Settings(), err
		}
	}
	s.store.Prune(st.MaxItems, st.MaxAgeDays)
	go s.syncLoop()
	go s.updateCheckLoop()
	s.refreshTriggerIndex()
	s.applyExpander(s.store.Settings())
	s.emit("settings:changed", st)
	s.emit("clip:stats", s.store.Stats())
	return s.store.Settings(), nil
}

// applyExpander starts/stops the inline-trigger keyboard hook to match settings.
func (s *ClipService) applyExpander(st Settings) {
	suffix := strings.TrimSpace(st.ExpandSuffix)
	if suffix == "" {
		suffix = "-"
	}
	if st.ExpandEnabled {
		if err := StartExpander(suffix); err != nil {
			Logf(s.store.Dir(), "expander start failed: %v", err)
			return
		}
		Logf(s.store.Dir(), "expander enabled (suffix=%q, %d triggers)", suffix, len(triggerSnapshot()))
		return
	}
	StopExpander()
}

// triggerSnapshot returns the loaded triggers (for logging/diagnostics).
func triggerSnapshot() []string {
	triggerMu.RLock()
	defer triggerMu.RUnlock()
	out := make([]string, 0, len(triggerIndex))
	for k := range triggerIndex {
		out = append(out, k)
	}
	return out
}

// ExpanderActive reports whether the inline expansion hook is installed.
func (s *ClipService) ExpanderActive() bool { return expanderActive() }

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
// than deleted: the installer's choice wins on first run.
func (s *ClipService) ReconcileAutoStart() {
	if !s.hooks.PlatformWorks || s.hooks.ExePath == "" {
		return
	}
	st := s.store.Settings()
	have := strings.ToLower(autoStartCommandValue())
	want := strings.ToLower(s.hooks.ExePath)

	switch {
	case st.AutoStart && !strings.Contains(have, want):
		if err := setAutoStart(true, s.hooks.ExePath); err != nil {
			Logf(s.store.Dir(), "autostart reconcile failed: %v", err)
			return
		}
		Logf(s.store.Dir(), "autostart registered: %s", autoStartCommand(s.hooks.ExePath))
	case !st.AutoStart && strings.Contains(have, "clipnest"):
		// Adopt the existing entry (installer choice) into the settings.
		st.AutoStart = true
		if err := s.store.SaveSettings(st); err != nil {
			Logf(s.store.Dir(), "autostart adopt failed: %v", err)
			return
		}
		Logf(s.store.Dir(), "autostart adopted from registry: %s", have)
		s.emit("settings:changed", st)
	}
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
func (s *ClipService) PickItem(id string, paste bool) (string, error) {
	it, ok := s.store.Get(id)
	if !ok {
		return "", errors.New("条目不存在")
	}
	if err := s.writeToClipboard(it); err != nil {
		return "", err
	}
	s.store.MarkCopied(id)

	s.mu.Lock()
	target := s.targetHwnd
	s.mu.Unlock()

	if s.hooks.HidePicker != nil {
		s.hooks.HidePicker()
	}
	s.emit("clip:stats", s.store.Stats())
	Logf(s.store.Dir(), "picker pick: id=%s kind=%s paste=%v target=%#x", it.ID, it.Kind, paste, target)

	if !paste {
		if target != 0 {
			go ActivateWindow(target)
		}
		return "已复制", nil
	}
	go func() {
		if err := PasteInto(target); err != nil {
			Logf(s.store.Dir(), "picker paste failed: %v", err)
			s.emit("picker:paste-failed", err.Error())
			return
		}
		Logf(s.store.Dir(), "picker paste sent: target=%#x", target)
	}()
	return "已粘贴", nil
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
