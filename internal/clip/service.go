package clip

import (
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
const Version = "0.1.0"

// AppInfo is static information the frontend needs for the about/settings view.
type AppInfo struct {
	Version      string `json:"version"`
	DataDir      string `json:"dataDir"`
	ExePath      string `json:"exePath"`
	OS           string `json:"os"`
	AutoStart    bool   `json:"autoStart"`
	PlatformOK   bool   `json:"platformOk"`
	PlatformNote string `json:"platformNote"`
}

// Hooks lets main.go wire the ClipService to application-level concerns (window
// visibility, quitting, tray menu labels) without importing Wails here.
type Hooks struct {
	Emit          func(name string, data any)
	ShowWindow    func()
	HideWindow    func()
	OnSettings    func(Settings) error
	Quit          func()
	ExePath       string
	PlatformNote  string
	PlatformWorks bool
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
			debounce.Reset(120 * time.Millisecond)
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
	s.mu.Lock()
	s.suppressHash = it.Hash
	s.suppressAt = time.Now()
	s.mu.Unlock()

	var err error
	switch it.Kind {
	case KindImage:
		var data []byte
		data, err = os.ReadFile(s.store.BlobPath(it.ImageFile))
		if err == nil {
			err = WriteImageToClipboard(data)
		}
	case KindFiles:
		paths := make([]string, 0, len(it.Files))
		for _, f := range it.Files {
			paths = append(paths, f.Path)
		}
		err = WriteFilesToClipboard(paths)
	case KindRichText:
		err = WriteHTMLToClipboard(it.HTML, it.Text)
	default:
		err = WriteTextToClipboard(it.Text)
	}
	if err != nil {
		return err
	}
	s.store.MarkCopied(id)
	s.emit("clip:stats", s.store.Stats())
	return nil
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
		data, err := os.ReadFile(s.store.BlobPath(it.ImageFile))
		if err != nil {
			return err
		}
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("clipnest-%s.png", it.ID))
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		return shellOpen(tmp)
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
	s.emit("settings:changed", st)
	s.emit("clip:stats", s.store.Stats())
	return s.store.Settings(), nil
}

// SetCapture is the tray's pause/resume switch.
func (s *ClipService) SetCapture(enabled bool) (Settings, error) {
	st := s.store.Settings()
	st.Capture = enabled
	return s.SaveSettings(st)
}

// GetAppInfo returns version/paths for the about panel.
func (s *ClipService) GetAppInfo() AppInfo {
	autoStart := false
	if s.hooks.PlatformWorks {
		autoStart = autoStartEnabled()
	}
	return AppInfo{
		Version:      Version,
		DataDir:      s.store.Dir(),
		ExePath:      s.hooks.ExePath,
		OS:           "windows",
		AutoStart:    autoStart,
		PlatformOK:   s.hooks.PlatformWorks,
		PlatformNote: s.hooks.PlatformNote,
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

// encodeBase64 is shared by the store's data URL helper.
func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
