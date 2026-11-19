package clip

import (
	"errors"
	"time"
)

// 采集管线：监听剪贴板变化 → 去抖 → 按设置过滤 → 落库 → 向界面发事件；
// 另含「记一笔」快速输入窗的显隐与它的热键自愈。
// 不负责：格式的读取与构造（clipboard_*.go）、条目写回剪贴板（service_paste.go）。

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

	if isNew {
		s.emit("clip:new", *entry)
	} else {
		s.emit("clip:bumped", *entry)
	}
	s.emit("clip:stats", s.store.Stats())
}

// CaptureSize is the quick-capture popup: a one-box editor for a memo.
const (
	CaptureWidth  = 620
	CaptureHeight = 220
)

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
