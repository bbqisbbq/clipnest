package clip

import (
	"errors"
	"regexp"
	"strings"
)

// 隐写模式（stealth）：热键激活后光标处不出现任何输入框，焦点交给一个全透明的
// 输入窗；你打的字只进内存缓冲（带输入法，中文照常合成），按提交键才写成备忘。
//
//	@名称@   把这次（以及之后的）内容写进标题为「名称」的条目
//	@-@      忘掉名称，之后每次提交都新建一条
//	Ctrl+Enter 提交并清空缓冲，模式继续
//	Esc      退出；缓冲非空时先自动保存一次（不丢字）
//
// 不负责：窗口的创建与定位（main.go）、可见的界面（frontend/src/Stealth.tsx）。

const (
	// StealthWidth/Height 是透明输入窗的大小：它必须落在光标附近，输入法的
	// 候选框才会出现在你正在看的地方。
	StealthWidth  = 460
	StealthHeight = 32
	// StealthHudWidth/Height 是屏幕角落那个小提示条的尺寸。
	StealthHudWidth  = 200
	StealthHudHeight = 36
)

// StealthState drives the corner HUD. It is polled, so it stays boring on
// purpose: no events, no cross-window plumbing.
type StealthState struct {
	Active  bool   `json:"active"`
	Target  string `json:"target"`  // 目标条目名（"" = 每次新建一条）
	Chars   int    `json:"chars"`   // 当前缓冲的字数
	Pending bool   `json:"pending"` // 缓冲里有内容
	Saved   string `json:"saved"`   // 最近一次提交写到了哪个条目
	Message string `json:"message"` // 一句提示（错误或结果）
}

// StealthResult is what one commit reports back to the input window.
type StealthResult struct {
	Target  string `json:"target"`
	Chars   int    `json:"chars"`
	Created bool   `json:"created"`
	Message string `json:"message"`
}

// @名称@ / @-@：名称里不许出现 @ 和换行，长度交给 SaveEntry 的标题上限。
var stealthTargetPattern = regexp.MustCompile(`@([^@\r\n]{1,60})@`)

// splitStealthTarget pulls the first `@名称@` out of the buffer.
// body is what actually gets saved; reset is true for `@-@`.
func splitStealthTarget(text string) (body, name string, reset bool) {
	m := stealthTargetPattern.FindStringSubmatchIndex(text)
	if m == nil {
		return text, "", false
	}
	name = strings.TrimSpace(text[m[2]:m[3]])
	body = text[:m[0]] + text[m[1]:]
	if name == "-" {
		return body, "", true
	}
	if name == "" {
		return body, "", false
	}
	return body, name, false
}

// ShowStealth activates the mode: capture the target window, drop the invisible
// input window at the caret and show the corner HUD.
func (s *ClipService) ShowStealth() error {
	if s.hooks.ShowStealthAt == nil {
		return errors.New("隐写输入窗口未就绪")
	}
	s.stealthMu.Lock()
	active := s.stealthOn
	s.stealthMu.Unlock()
	if active {
		// 已经开着：让前端把它手上的缓冲保存掉并退出（它才知道当前文字）。
		s.emit("stealth:close-request", "")
		return nil
	}

	target := ForegroundWindowHandle()
	if target == 0 {
		return errors.New("没有可用的目标窗口")
	}
	x, y := CaretAnchor()

	s.stealthMu.Lock()
	s.stealthOn = true
	s.stealthHwnd = target
	s.stealthChars = 0
	s.stealthMessage = ""
	s.stealthMu.Unlock()

	if !s.hooks.ShowStealthAt(x, y) {
		s.stealthMu.Lock()
		s.stealthOn = false
		s.stealthMu.Unlock()
		return errors.New("无法显示隐写输入窗口")
	}
	if s.hooks.ShowStealthHud != nil {
		s.hooks.ShowStealthHud()
	}
	Logf(s.store.Dir(), "stealth show: target=%#x anchor=(%d,%d)", target, x, y)
	s.emit("stealth:opened", "")
	return nil
}

// StealthProgress is called by the input window on every edit so the HUD can
// show how much is buffered and where it will land.
func (s *ClipService) StealthProgress(text string) StealthState {
	_, name, reset := splitStealthTarget(text)
	s.stealthMu.Lock()
	defer s.stealthMu.Unlock()
	if reset {
		s.stealthTarget = ""
	} else if name != "" {
		s.stealthTarget = name
	}
	s.stealthChars = len([]rune(strings.TrimSpace(text)))
	return s.stealthStateLocked()
}

// StealthState is the HUD's read side.
func (s *ClipService) StealthState() StealthState {
	s.stealthMu.Lock()
	defer s.stealthMu.Unlock()
	return s.stealthStateLocked()
}

func (s *ClipService) stealthStateLocked() StealthState {
	return StealthState{
		Active:  s.stealthOn,
		Target:  s.stealthTarget,
		Chars:   s.stealthChars,
		Pending: s.stealthChars > 0,
		Saved:   s.stealthSaved,
		Message: s.stealthMessage,
	}
}

// StealthCommit writes the buffer to its target entry and reports where it went.
// The buffer is the caller's; the frontend clears it on success.
func (s *ClipService) StealthCommit(text string) (StealthResult, error) {
	res, err := s.writeStealth(text)
	s.stealthMu.Lock()
	if err == nil {
		s.stealthChars = 0
		s.stealthSaved = res.Target
		s.stealthMessage = res.Message
	} else {
		s.stealthMessage = err.Error()
	}
	s.stealthMu.Unlock()
	return res, err
}

// writeStealth resolves the target name and appends/creates the entry.
func (s *ClipService) writeStealth(text string) (StealthResult, error) {
	body, name, reset := splitStealthTarget(text)
	body = strings.TrimSpace(body)
	if body == "" {
		return StealthResult{}, errors.New("没有可保存的内容")
	}

	s.stealthMu.Lock()
	if reset {
		s.stealthTarget = ""
	}
	if name != "" {
		s.stealthTarget = name
	}
	target := s.stealthTarget
	s.stealthMu.Unlock()

	var (
		entry   Entry
		err     error
		created bool
	)
	if target != "" {
		if existing, ok := s.store.FindEntryByTitle(target); ok {
			entry, err = s.store.AppendTextToEntry(existing.ID, body)
		} else {
			entry, err = s.store.SaveEntry(Entry{
				Title:   target,
				Content: body,
				Source:  SourceManual,
				Kind:    KindText,
			})
			created = true
		}
	} else {
		entry, err = s.store.SaveEntry(Entry{Content: body, Source: SourceManual, Kind: KindText})
		created = true
	}
	if err != nil {
		return StealthResult{}, err
	}

	if created {
		s.emit("clip:new", entry)
	} else {
		s.emit("clip:updated", entry)
	}
	s.emit("clip:stats", s.store.Stats())

	chars := len([]rune(body))
	msg := "已写入 " + target
	if target == "" {
		msg = "已存成新条目"
	}
	Logf(s.store.Dir(), "stealth commit: target=%q chars=%d created=%v", target, chars, created)
	return StealthResult{Target: target, Chars: chars, Created: created, Message: msg}, nil
}

// CloseStealth leaves the mode and gives focus back to the window that had it.
// A non-empty buffer is saved first: Esc must never lose what you typed.
func (s *ClipService) CloseStealth(text string) (StealthResult, error) {
	var (
		res StealthResult
		err error
	)
	if strings.TrimSpace(text) != "" {
		res, err = s.writeStealth(text)
	}

	s.stealthMu.Lock()
	s.stealthOn = false
	s.stealthChars = 0
	s.stealthMessage = ""
	targetHwnd := s.stealthHwnd
	s.stealthHwnd = 0
	s.stealthMu.Unlock()

	if s.hooks.HideStealthHud != nil {
		s.hooks.HideStealthHud()
	}
	if s.hooks.HideStealth != nil {
		s.hooks.HideStealth()
	}
	if targetHwnd != 0 {
		go ActivateWindow(targetHwnd)
	}
	s.emit("stealth:closed", res.Message)
	return res, err
}

// StealthHotkey returns the accelerator that activates the mode ("" = disabled).
func (s *ClipService) StealthHotkey() string { return s.store.Settings().StealthHotkey }
