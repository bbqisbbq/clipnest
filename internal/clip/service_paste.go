package clip

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 条目回到剪贴板/系统：复制、取图、打开、清空历史，以及快速粘贴弹窗（picker）的
// 显隐与「回车直粘原窗口」。
// 不负责：采集（service_capture.go）、条目存储（entry_*.go）。

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

// echoBlanketWindow 是"刚写完富文本剪贴板"之后一律丢弃采集回声的窗口。
// WM_CLIPBOARDUPDATE 是毫秒级到达的，1.2 秒足够；再长就会把人紧接着的一次真实复制吞掉。
const echoBlanketWindow = 1200 * time.Millisecond

// CopyEntry puts an entry on the clipboard.
//
// html/plain 由前端给：HTML 是**卡片同一套渲染器（marked）**的产物，图片仍是
// `memoimg:` 令牌 —— 换成真字节交给 Go 做，因为字节在 Go 这边（entry_blobs）。
// 传空串时退回纯文本（图片令牌会被剥掉，不会把 `![](memoimg:…)` 丢给别人）。
func (s *ClipService) CopyEntry(id, html, plain string) error {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return errors.New("条目不存在")
	}
	if err := s.writeEntryToClipboard(e, html, plain); err != nil {
		return err
	}
	_ = s.store.BumpCopyCount(id)
	s.emit("clip:stats", s.store.Stats())
	return nil
}

// writeEntryToClipboard puts an entry on the clipboard and suppresses the
// capture echo that the listener would otherwise turn into a new entry.
//
// 抑制的 key 必须是「采集侧会算出来的那个哈希」（entryEchoHash），
// 不能用 e.Hash：那是条目自己的内容哈希，两种口径算出来的值从来就不相等，
// 于是手写/导入的备忘被复制回剪贴板时不会被认作"自己写的"，会多出一条
// 来源显示 clipnest 的采集条目。
func (s *ClipService) writeEntryToClipboard(e Entry, htmlDoc, plain string) error {
	s.mu.Lock()
	s.suppressHash = entryEchoHash(e)
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
		// 私有格式的字节挂在采集哈希下（正文只是格式名），见 SaveEntryBlobAs。
		if e.Hash == "" {
			return fmt.Errorf("「%s」没有可用的原始数据", e.FormatName)
		}
		data, _, err := s.store.LoadEntryBlob(e.Hash)
		if err != nil {
			return fmt.Errorf("「%s」的原始数据已不可用", e.FormatName)
		}
		return WriteRawToClipboard(e.FormatID, e.FormatName, data)
	default:
		return s.writeBodyEntry(e, htmlDoc, plain)
	}
}

// writeBodyEntry 回填"正文型"条目（文本 / 链接 / 手写备忘）。
//
// 正文里有图片（`memoimg:` 令牌）时必须**图文一起**交给目标程序，所以同时放三份
// 表示：HTML（data URI）、RTF（\pict）、纯文本。只有纯文本一条路时，图片会以
// `![](memoimg:ab12…)` 的形式裸奔到别人的聊天框里 —— 图丢了、还多一串垃圾。
func (s *ClipService) writeBodyEntry(e Entry, htmlDoc, plain string) error {
	tokens := memoTokensIn(e.Content)
	if htmlDoc == "" || len(tokens) == 0 {
		// 没有渲染结果或没有图：纯文本就够，但要保证令牌不泄露出去。
		return WriteTextToClipboard(stripMemoTokens(e.Content))
	}

	pngs := make(map[string][]byte, len(tokens))
	for _, token := range tokens {
		if data, _, err := s.store.LoadEntryBlob(token); err == nil && len(data) > 0 {
			pngs[token] = data
		}
	}
	htmlFragment := embedMemoImages(htmlDoc, pngs)
	if plain == "" {
		plain = stripMemoTokens(e.Content)
	}
	// RTF 必须用**还带着 memoimg: 令牌**的那一版：\pict 要靠令牌去 pngs 里取字节，
	// 而 embedMemoImages 之后 src 已经是 data URI 了（这里踩过一次：图片段落变成空行）。
	rtf := htmlToRTF(htmlDoc, pngs)

	// 写的是 HTML/RTF：采集侧读回来算的是 htmlHash/rtfHash，跟条目自己的口径对不上，
	// 靠哈希抑制回声不可靠 —— 所以开一个短窗口，这段时间内一律丢弃回声。
	s.mu.Lock()
	s.suppressAllUntil = time.Now().Add(echoBlanketWindow)
	s.mu.Unlock()

	return WriteRichToClipboard(htmlFragment, rtf, plain)
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

// CopyPlainText 把一段纯文本放进剪贴板（OCR 识别结果、右键菜单里的"复制"都用它）。
//
// 同样要走回声抑制：不抑制的话"复制识别文字"会被自己的监听器当成新内容采集一条。
func (s *ClipService) CopyPlainText(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("没有可复制的内容")
	}
	s.mu.Lock()
	s.suppressHash = entryEchoHash(Entry{Content: text, Kind: KindText})
	s.suppressAt = time.Now()
	s.mu.Unlock()
	return WriteTextToClipboard(text)
}

// PickEntry copies an entry and, when paste is true, pastes it into the window
// that was focused when the popup opened. It returns a short status string the
// popup can show while it closes.
func (s *ClipService) PickEntry(id string, paste bool, html, plain string) (string, error) {
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
	if err := s.writeEntryToClipboard(e, html, plain); err != nil {
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
