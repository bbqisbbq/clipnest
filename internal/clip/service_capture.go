package clip

import (
	"context"
	"errors"
	"fmt"
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
			// Tail：只看最新的文件类条目。缺了它就是每 3 分钟重扫最旧的那 500 条，
			// 新条目的「文件还在不在」永远停在采集那一刻。
			for _, e := range s.store.ListEntries(EntryQuery{Kind: string(KindFiles), Limit: 500, Tail: true}) {
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
	if err != nil {
		// 静默失败是排查噩梦的根源：用户报"某类内容采不到"时，日志里得有线索。
		Logf(s.store.Dir(), "capture read failed: %v", err)
		return
	}
	if cap == nil {
		return
	}

	// Ignore the echo of our own copy-back.
	//
	// 两种抑制：① 精确哈希（写纯文本时成立）；② 一个很短的"一律丢弃"窗口 ——
	// 写图文混排条目时放的是 HTML/RTF，采集侧读回来算的是 htmlHash/rtfHash，
	// 跟条目自己的口径对不上，靠哈希拦不住；不拦就是"复制一次多一条采集记录"。
	s.mu.Lock()
	blanket := time.Now().Before(s.suppressAllUntil)
	suppressed := blanket || (cap.Hash == s.suppressHash && time.Since(s.suppressAt) < 4*time.Second)
	s.mu.Unlock()
	if suppressed {
		if blanket {
			Logf(s.store.Dir(), "capture echo suppressed (rich copy-back window)")
		}
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

	// 只拿到地址的网页图片：真正的字节要去下载，绝不能挡在采集路径上
	// （下一次复制会被 45ms 合并窗口吃掉）。下载完自己落库、自己发事件。
	if cap.Kind == KindImage && len(cap.PNG) == 0 && cap.RemoteImageURL != "" {
		s.captureRemoteImage(*cap, st)
		return
	}

	s.saveCaptured(*cap)
}

// captureRemoteImage 把"只有 <img src> 的一条复制"补成真正的图片条目。
//
// 三条出口：① 下载成功 → 存图片（顺带进 OCR 队列）；② 用户关掉了自动下载 →
// 立刻降级成富文本，不让人等；③ 下载失败（超时/防盗链/不是图片）→ 也降级成富文本，
// 并记一行日志说明为什么。无论哪条，这次复制都不会凭空消失。
func (s *ClipService) captureRemoteImage(cap Captured, st Settings) {
	if !st.CaptureRemoteImage {
		s.saveCaptured(richFallbackFromRemote(cap))
		return
	}
	src := cap.RemoteImageURL
	sourceApp := cap.SourceApp
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(s.bgCtx, remoteImageTimeout)
		defer cancel()
		img, err := fetchRemoteImage(ctx, src)
		if err != nil {
			Logf(s.store.Dir(), "remote image fetch failed (%s): %v —— 退回富文本", src, err)
			s.saveCaptured(richFallbackFromRemote(cap))
			return
		}
		img.SourceApp = sourceApp
		Logf(s.store.Dir(), "remote image fetched: %s (%dx%d, %s)", src, img.Width, img.Height, humanBytes(len(img.PNG)))
		s.saveCaptured(*img)
	}()
}

// saveCaptured 是采集管线的尾巴：落库 → 通知界面 → 统计 → 图片排 OCR。
// 剪贴板同步路径、「网页图片后台下载」与截图共用这一份，别在两处各写一遍。
// 返回落库后的条目（失败时 nil）；第二个返回值表示是"新条目"还是"又见了一次"。
func (s *ClipService) saveCaptured(cap Captured) (*Entry, bool) {
	entry, isNew, err := s.store.SaveCapture(cap)
	if err != nil {
		Logf(s.store.Dir(), "save capture failed (kind=%s, format=%q): %v", cap.Kind, cap.FormatName, err)
		return nil, false
	}

	if isNew {
		s.emit("clip:new", *entry)
	} else {
		s.emit("clip:bumped", *entry)
	}
	s.emit("clip:stats", s.store.Stats())
	// 图片条目丢进 OCR 队列：识别慢（约 1 秒/张），绝不能挡在采集路径上。
	if entry.Kind == KindImage {
		s.enqueueOCR(entry.ID)
	}
	return entry, isNew
}

// The quick-capture popup: a one-box editor for a memo.
//
// 尺寸：默认 620×220，用户拖动窗口边缘可以改；改过的尺寸记在本机 meta 表里
// （metaCaptureSize），下次唤起照旧 —— 位置仍然跟着光标走，那是这个窗口的立身之本。
const (
	CaptureWidth  = 620
	CaptureHeight = 220
	// 下限：抬头 + 底栏本身就要吃掉 70px 左右，再小编辑器就没地方放字了。
	CaptureMinWidth  = 420
	CaptureMinHeight = 160
	// 上限：记住的尺寸可能来自更大的显示器，不能让窗口大到无处安放。
	CaptureMaxWidth  = 1600
	CaptureMaxHeight = 1200
)

// metaCaptureSize holds the size the user last left the quick-capture window at,
// formatted "620x220". Deliberately device-local: it lives in the meta table, so
// the WebDAV package sync does not drag one machine's window size onto another's.
const metaCaptureSize = "capture.size"

// CaptureSize returns the size the quick-capture window opens at: whatever the
// user dragged it to last time, or the default.
func (s *ClipService) CaptureSize() (int, int) {
	if w, h, ok := parseCaptureSize(s.store.Meta(metaCaptureSize)); ok {
		return w, h
	}
	return CaptureWidth, CaptureHeight
}

// SaveCaptureSize remembers the size the user left the window at.
//
// 只记「真的拖出来的」尺寸：小于下限的值不是用户拖出来的（窗口刚创建、被隐藏时
// 读到的 0×0 / 几十像素这类瞬时值），记下来只会把默认尺寸换成最小值，所以直接忽略。
// 超过上限的才夹：记住的尺寸可能来自更大的显示器，那时收一收比忘掉好。
func (s *ClipService) SaveCaptureSize(w, h int) {
	if w < CaptureMinWidth || h < CaptureMinHeight {
		return
	}
	w = min(w, CaptureMaxWidth)
	h = min(h, CaptureMaxHeight)
	next := fmt.Sprintf("%dx%d", w, h)
	// 拖窗口位置也会走 WindowEndResize，尺寸没变就别写库、也别刷日志。
	if s.store.Meta(metaCaptureSize) == next {
		return
	}
	if err := s.store.SetMeta(metaCaptureSize, next); err != nil {
		Logf(s.store.Dir(), "capture size save failed: %v", err)
		return
	}
	Logf(s.store.Dir(), "capture size remembered: %s", next)
}

func parseCaptureSize(raw string) (int, int, bool) {
	var w, h int
	if _, err := fmt.Sscanf(raw, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	w, h = clampCaptureSize(w, h)
	return w, h, true
}

func clampCaptureSize(w, h int) (int, int) {
	return min(max(w, CaptureMinWidth), CaptureMaxWidth),
		min(max(h, CaptureMinHeight), CaptureMaxHeight)
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

// EnsureCaptureHotkey repairs a profile whose captureHotkey ended up empty
// because an older build shipped no default for it. Runs once (meta marker), so
// a later deliberate clear is respected.
//
// 修好之后要**真的重新注册热键**：以前只写库，而热键是在 Start 之前按旧设置
// 注册的，于是"被治愈"的那一次启动里 Ctrl+Shift+N 依然是空的。
func (s *ClipService) EnsureCaptureHotkey() {
	if s.store.Meta("capture.hotkeyInit") != "" {
		return
	}
	st := s.store.Settings()
	if st.CaptureHotkey == "" {
		st.CaptureHotkey = DefaultSettings().CaptureHotkey
		if err := s.store.SaveSettings(st); err != nil {
			Logf(s.store.Dir(), "capture hotkey heal failed: %v", err)
		} else if s.hooks.OnSettings != nil {
			if err := s.hooks.OnSettings(st); err != nil {
				Logf(s.store.Dir(), "capture hotkey re-register failed: %v", err)
			}
		}
	}
	_ = s.store.SetMeta("capture.hotkeyInit", "1")
}
