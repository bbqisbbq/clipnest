package clip

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

func init() {
	// {{clipboard}} 读取当前剪贴板文本
	clipboardTextForTemplate = func() string {
		cap, err := captureClipboard()
		if err != nil || cap == nil {
			return ""
		}
		return cap.Text
	}
}

// decodeBase64Image accepts a "data:image/png;base64,…" URL or bare base64.
func decodeBase64Image(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "base64,"); i >= 0 {
		s = s[i+len("base64,"):]
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw) < 8 || string(raw[1:4]) != "PNG" {
		return nil, errors.New("只支持 PNG 图片")
	}
	return raw, nil
}

func osStat(p string) (os.FileInfo, error) { return os.Stat(p) }

// runSnippet executes the action that fits the snippet's kind. paste=true means
// "put it where the caret is" (text/image/file); actions like opening a URL or
// launching a program ignore it.
func (s *ClipService) runSnippet(sp Snippet, paste bool, target uintptr) (string, error) {
	s.store.MarkSnippetUsed(sp.ID)

	switch sp.Kind {
	case SnippetText, SnippetPassword:
		text, cursor := renderTemplate(sp.Body)
		if err := WriteTextToClipboard(text); err != nil {
			return "", err
		}
		if paste && target != 0 {
			go s.pasteAndLogAt(target, "snippet", leftArrows(text, cursor))
			return "已粘贴", nil
		}
		if target != 0 {
			go ActivateWindow(target)
		}
		if sp.Kind == SnippetPassword {
			return "已复制密码", nil
		}
		return "已复制", nil

	case SnippetHTML:
		// Rich text can only travel through the clipboard (CF_HTML + plain text).
		plain := strings.TrimSpace(stripHTML(sp.Body))
		if err := WriteHTMLToClipboard(sp.Body, plain); err != nil {
			return "", err
		}
		if paste && target != 0 {
			go s.pasteAndLog(target, "snippet-html")
			return "已粘贴富文本", nil
		}
		if target != 0 {
			go ActivateWindow(target)
		}
		return "已复制富文本", nil

	case SnippetImage:
		payload, _, err := s.store.LoadEntryBlob(memoImageToken(sp.Body))
		if err != nil || len(payload) == 0 {
			return "", errors.New("这个片段没有图片数据")
		}
		if err := WriteImageToClipboard(payload); err != nil {
			return "", err
		}
		if paste && target != 0 {
			go s.pasteAndLog(target, "snippet-image")
			return "已粘贴图片", nil
		}
		if target != 0 {
			go ActivateWindow(target)
		}
		return "已复制图片", nil

	case SnippetFile:
		paths := splitPaths(sp.Body)
		if len(paths) == 0 {
			return "", errors.New("没有有效的路径")
		}
		// A single directory is a "go there" action; files are content you paste.
		if len(paths) == 1 && isDirPath(paths[0]) {
			if err := revealInExplorer(paths[0]); err != nil {
				return "", err
			}
			return "已打开文件夹", nil
		}
		if err := WriteFilesToClipboard(paths); err != nil {
			return "", err
		}
		if paste && target != 0 {
			go s.pasteAndLog(target, "snippet-files")
			return "已粘贴文件", nil
		}
		if target != 0 {
			go ActivateWindow(target)
		}
		return fmt.Sprintf("已复制 %d 个文件", len(paths)), nil

	case SnippetURL:
		if paste {
			// Ctrl+Enter ("copy only") comes through as paste=false; here paste
			// means "insert the link at the caret".
			if err := WriteTextToClipboard(sp.Body); err != nil {
				return "", err
			}
			if target != 0 {
				go s.pasteAndLog(target, "snippet-url")
				return "已粘贴链接", nil
			}
		}
		if err := WriteTextToClipboard(sp.Body); err != nil {
			return "", err
		}
		if err := s.openExternal(sp.Body); err != nil {
			return "已复制，但打开失败：" + err.Error(), nil
		}
		return "已打开网址", nil

	case SnippetApp:
		if err := launchProgram(sp.Body); err != nil {
			return "", err
		}
		return "已启动", nil

	case SnippetCommand:
		// A command's *output* is what you want at the caret, so it is handled
		// like text: copy it (and paste it when asked) instead of running the
		// command silently and letting the output disappear into the log.
		out, err := runCommand(sp.Body)
		if err != nil {
			return "", fmt.Errorf("命令失败: %w", err)
		}
		out = strings.TrimRight(out, "\r\n")
		if out == "" {
			return "命令没有输出", nil
		}
		if err := WriteTextToClipboard(out); err != nil {
			return "", err
		}
		if paste && target != 0 {
			go s.pasteAndLog(target, "snippet-command")
			return truncateRunes(firstLine(out), 40), nil
		}
		if target != 0 {
			go ActivateWindow(target)
		}
		return truncateRunes(firstLine(out), 40), nil
	}
	return "", errors.New("未知的片段类型")
}

// firstLine is the first non-empty line of command output.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func (s *ClipService) pasteAndLog(target uintptr, what string) {
	s.pasteAndLogAt(target, what, 0)
}

// pasteAndLogAt pastes and then walks the caret back `lefts` characters, which
// is how {{cursor}} ends up "ready to type" instead of at the end of the text.
func (s *ClipService) pasteAndLogAt(target uintptr, what string, lefts int) {
	if err := PasteInto(target); err != nil {
		Logf(s.store.Dir(), "%s paste failed: %v", what, err)
		s.emit("picker:paste-failed", err.Error())
		return
	}
	if lefts > 0 {
		sendLeftArrows(lefts)
	}
	Logf(s.store.Dir(), "%s paste sent: target=%#x lefts=%d", what, target, lefts)
}

func (s *ClipService) openExternal(url string) error {
	if s.hooks.OpenURL != nil {
		return s.hooks.OpenURL(url)
	}
	return shellOpen(url)
}

// splitPaths accepts one path per line (Windows paths can contain spaces).
func splitPaths(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// PickSnippet is the picker's entry point: run the snippet and dismiss the popup.
func (s *ClipService) PickSnippet(id string, paste bool) (string, error) {
	sp, ok := s.store.GetSnippet(id)
	if !ok {
		return "", errors.New("片段不存在")
	}

	s.mu.Lock()
	target := s.targetHwnd
	s.mu.Unlock()

	if s.hooks.HidePicker != nil {
		s.hooks.HidePicker()
	}

	status, err := s.runSnippet(sp, paste, target)
	if err != nil {
		Logf(s.store.Dir(), "snippet pick failed: id=%s kind=%s: %v", sp.ID, sp.Kind, err)
		s.emit("picker:paste-failed", err.Error())
		return "", err
	}
	Logf(s.store.Dir(), "snippet pick: id=%s kind=%s paste=%v -> %s", sp.ID, sp.Kind, paste, status)
	s.emit("clip:stats", s.store.Stats())
	return status, nil
}

// TestSnippet runs a snippet from the manager window (no pasting into a target).

// snippetCommandOutput runs a command snippet and returns its trimmed output.
// The expander types this at the caret, so `ipconfig-` inserts the addresses
// instead of silently running something in the background.
func (s *ClipService) snippetCommandOutput(id string) (string, error) {
	sp, ok := s.store.GetSnippet(id)
	if !ok {
		return "", errors.New("片段不存在")
	}
	if sp.Kind != SnippetCommand {
		return "", errors.New("这个片段不是命令类型")
	}
	out, err := runCommand(sp.Body)
	if err != nil {
		return "", err
	}
	s.store.MarkSnippetUsed(id)
	return out, nil
}

// SnippetDraft is a pre-filled (unsaved) snippet: the editor opening with the
// content of a clipboard entry, so what was collected can be edited into a phrase.

// ImageData is a data URL for image entries (the editor needs the bytes).

// pasteImageInline writes an image snippet to the clipboard and pastes it into
// whatever window has focus — the expander's path, since an image has no text
// form that could be typed.
func (s *ClipService) pasteImageInline(id string) error {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return errors.New("片段不存在")
	}
	payload, _, err := s.store.LoadEntryBlob(memoImageToken(e.Content))
	if err != nil || len(payload) == 0 {
		return errors.New("这个片段没有图片数据")
	}
	if err := WriteImageToClipboard(payload); err != nil {
		return err
	}
	target := ForegroundWindowHandle()
	if target == 0 {
		return nil
	}
	return PasteInto(target)
}

// PickerSnippets powers "--" search inside the popup.
func (s *ClipService) PickerSnippets(query string, limit int) []Snippet {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.store.sortedSnippetsForPicker(query, limit)
}

// GrabClipboardImage returns the image currently on the clipboard as base64 PNG
// ("" when there is none). Used by the snippet editor's "从剪贴板抓取图片".

// stripHTML makes a crude plain-text fallback for the HTML clipboard format.
func stripHTML(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
			continue
		case '>':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
