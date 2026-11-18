package clip

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"
)

// Trigger index: snippets that expand inline, loaded into memory so the keyboard
// hook never touches SQLite (it runs on every keystroke).

type triggerEntry struct {
	ID    string
	Kind  SnippetKind
	Title string
	Body  string
}

var (
	triggerMu    sync.RWMutex
	triggerIndex map[string]triggerEntry
)

// refreshTriggerIndex reloads the in-memory trigger table.
func (s *ClipService) refreshTriggerIndex() {
	suffix := strings.TrimSpace(s.store.Settings().ExpandSuffix)
	if suffix == "" {
		suffix = "-"
	}
	next := make(map[string]triggerEntry, 32)
	for _, sp := range s.store.AllSnippets() {
		raw := NormalizeTrigger(sp.Trigger)
		if raw == "" {
			continue
		}
		entry := triggerEntry{ID: sp.ID, Kind: sp.Kind, Title: sp.Title, Body: sp.Body}
		next[raw] = entry
		// The hook matches "<缩写><后缀>" and looks up the abbreviation part, so
		// "addr-" must also be reachable as "addr". Strip the configured suffix,
		// and (belt and braces) any trailing punctuation the user typed in.
		if trimmed := strings.TrimSuffix(raw, strings.ToLower(suffix)); trimmed != "" && trimmed != raw {
			next[trimmed] = entry
		}
		if trimmed := strings.TrimRightFunc(raw, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}); trimmed != "" && trimmed != raw {
			next[trimmed] = entry
		}
	}
	triggerMu.Lock()
	triggerIndex = next
	triggerMu.Unlock()
}

// Inline expansion modes: what a trigger does when it fires.
const (
	// inlineType types the snippet text at the caret (no clipboard involved).
	inlineType = "type"
	// inlineImage pastes the snippet image; an image cannot be typed.
	inlineImage = "image"
	// inlineExecute runs the snippet action (command / program / folder).
	inlineExecute = "execute"
)

// inlineMode says how a snippet kind behaves when its trigger fires. Every kind
// is usable as a trigger: the ones that carry no text simply run their action
// instead of silently doing nothing.
func inlineMode(kind SnippetKind) string {
	switch kind {
	case SnippetText, SnippetPassword, SnippetURL, SnippetFile:
		return inlineType
	case SnippetImage:
		return inlineImage
	case SnippetCommand, SnippetApp:
		return inlineExecute
	}
	return inlineType
}

// inlineText is the text typed for a "type" snippet ("" for other modes).
func inlineText(e triggerEntry) string {
	if inlineMode(e.Kind) != inlineType {
		return ""
	}
	if e.Kind == SnippetFile {
		return strings.Join(splitPaths(e.Body), "\n")
	}
	return e.Body
}

// lookupTrigger is called by the expander for every keystroke. Every kind comes
// back: a command or app snippet has no text to type, but its trigger must still
// run the action rather than silently ignoring the abbreviation.
func lookupTrigger(key string) (triggerEntry, bool) {
	triggerMu.RLock()
	defer triggerMu.RUnlock()
	e, ok := triggerIndex[NormalizeTrigger(key)]
	return e, ok
}

// snippetActionHooks lets the platform expander execute actions and paste images
// without owning a *ClipService.
var (
	snippetHookMu         sync.RWMutex
	runSnippetByIDHook    func(id string) (string, error)
	pasteSnippetImageHook func(id string) error
)

// SetSnippetActionHooks wires the service into the expander.
func SetSnippetActionHooks(run func(id string) (string, error), pasteImage func(id string) error) {
	snippetHookMu.Lock()
	runSnippetByIDHook = run
	pasteSnippetImageHook = pasteImage
	snippetHookMu.Unlock()
}

func runSnippetByID(id string) (string, error) {
	snippetHookMu.RLock()
	fn := runSnippetByIDHook
	snippetHookMu.RUnlock()
	if fn == nil {
		return "", errors.New("片段动作还没准备好")
	}
	return fn(id)
}

func pasteSnippetImage(id string) error {
	snippetHookMu.RLock()
	fn := pasteSnippetImageHook
	snippetHookMu.RUnlock()
	if fn == nil {
		return errors.New("片段图片刚准备好")
	}
	return fn(id)
}


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

// SaveItemAsSnippet turns a clipboard-history entry into a snippet ("存为片段"),
// so something you just copied can become a reusable phrase with a trigger.
func (s *ClipService) SaveItemAsSnippet(itemID string) (Snippet, error) {
	it, ok := s.store.Get(itemID)
	if !ok {
		return Snippet{}, errors.New("条目不存在")
	}

	sp := Snippet{Title: truncateRunes(it.Preview, 24), Group: "来自历史"}
	switch it.Kind {
	case KindText, KindRichText:
		sp.Kind = SnippetText
		sp.Body = it.Text
	case KindLink:
		sp.Kind = SnippetURL
		sp.Body = it.Text
	case KindFiles:
		sp.Kind = SnippetFile
		paths := make([]string, 0, len(it.Files))
		for _, f := range it.Files {
			paths = append(paths, f.Path)
		}
		sp.Body = strings.Join(paths, "\n")
	case KindImage:
		sp.Kind = SnippetImage
		sp.Body = it.Preview
	default:
		return Snippet{}, errors.New("这个类型暂不支持存为片段")
	}

	saved, err := s.store.SaveSnippet(sp)
	if err != nil {
		return Snippet{}, err
	}
	if it.Kind == KindImage {
		if payload, err := s.store.LoadBlobByName(it.ImageFile); err == nil && len(payload) > 0 {
			_ = s.store.SaveSnippetBlob(saved.ID, ".png", payload)
		}
	}
	s.refreshTriggerIndex()
	Logf(s.store.Dir(), "snippet from history: item=%s -> snippet=%s (%s)", it.ID, saved.ID, saved.Kind)
	out, ok := s.store.GetSnippet(saved.ID)
	if !ok {
		return Snippet{}, errors.New("片段保存失败")
	}
	return out, nil
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

	case SnippetImage:
		payload, err := s.store.SnippetBlob(sp.ID)
		if err != nil {
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
		out, err := runCommand(sp.Body)
		if err != nil {
			return "", fmt.Errorf("命令失败: %w", err)
		}
		if out == "" {
			return "已执行", nil
		}
		return truncateRunes(out, 60), nil
	}
	return "", errors.New("未知的片段类型")
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
func (s *ClipService) TestSnippet(id string) (string, error) {
	sp, ok := s.store.GetSnippet(id)
	if !ok {
		return "", errors.New("片段不存在")
	}
	status, err := s.runSnippet(sp, false, 0)
	if err != nil {
		return "", err
	}
	return status, nil
}

// pasteImageInline writes an image snippet to the clipboard and pastes it into
// whatever window has focus — the expander's path, since an image has no text
// form that could be typed.
func (s *ClipService) pasteImageInline(id string) error {
	payload, err := s.store.SnippetBlob(id)
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
func (s *ClipService) GrabClipboardImage() (string, error) {
	cap, err := captureClipboard()
	if err != nil || cap == nil || len(cap.PNG) == 0 {
		return "", nil
	}
	return encodeBase64(cap.PNG), nil
}
