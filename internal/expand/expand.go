// Package expand implements inline trigger expansion ("type addr␣ → insert the
// phrase") independently of the clipboard manager around it.
//
// Layering:
//
//	internal/winplat    Win32 input injection, hooks, IME state (no domain logic)
//	internal/expand     this package: buffer, matching, dispatch (no Win32 detail)
//	internal/clip       store/service: implements expand.Host
//
// The engine only knows three things: what the user typed, which triggers exist
// (via Host.Match), and how to deliver content (via Host). Everything else —
// SQLite, clipboard history, WebDAV sync — stays out of here.
package expand

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

// vkBack 是 VK_BACK（退格）。放在中立文件里而不是 wininput_windows.go：下面 Feed 的
// switch 要用它，而其它虚拟键码常量是 Windows 专属的，非 Windows 编译时看不见。
const vkBack = 0x08

// Trigger is one expandable phrase, flattened to what the engine needs.
type Trigger struct {
	ID            string
	Kind          string // text | password | url | file | html | image | command | app
	Body          string
	Title         string
	CaseSensitive bool
	KeepTrigger   bool     // keep the abbreviation in place (append instead of replace)
	Apps          []string // only expand inside these processes (empty = everywhere)
	ExcludeApps   []string // never expand inside these processes
}

// Host is the domain side of the engine. Implemented by ClipService.
type Host interface {
	// Match returns the longest trigger that the buffer ends with, plus its rune
	// length. buf has already had its terminator removed.
	Match(buf string) (Trigger, int, bool)
	// Render substitutes template variables for text-like triggers. The second
	// value is the {{cursor}} offset in runes (-1 when the phrase has none).
	Render(t Trigger) (string, int)
	// CommandOutput runs a command trigger and returns what to type.
	CommandOutput(t Trigger) (string, error)
	// PasteImage inserts an image trigger (images cannot be typed).
	PasteImage(t Trigger) error
	// LaunchApp starts a program trigger.
	LaunchApp(t Trigger) error
	// FallbackPaste delivers text through the clipboard when injection fails.
	FallbackPaste(text string) error
	// ForegroundProcess is the image name of the focused process ("code.exe").
	ForegroundProcess() string
	// IsOwnWindow reports whether the foreground window belongs to us.
	IsOwnWindow() bool
}

// Config is the user-facing tuning.
type Config struct {
	Enabled   bool
	Suffix    string // legacy single terminator, kept working
	EndChars  string // any of these finishes a trigger (space, tab, punctuation)
	Debug     bool
	IdleReset time.Duration // buffer forgets after this long without keys
	MaxBuffer int
	Logf      func(format string, args ...any)
}

// Engine owns the keystroke buffer and performs expansions.
type Engine struct {
	host Host
	mu   sync.Mutex
	cfg  Config

	buf      []rune
	lastKey  time.Time
	lastHwnd uintptr

	active   bool
	hook     uintptr
	threadID uint32
	busy     bool // injecting: ignore our own synthetic keys
	// runeFor maps a virtual key to a character; swappable in tests so the engine
	// can be exercised without real keyboard state.
	runeFor func(vk int) (rune, bool)
	// typeTextFn injects characters; swappable in tests so no keystrokes escape.
	typeTextFn func(string) error
}

// New creates an engine bound to a host.
func New(host Host) *Engine {
	return &Engine{
		host:       host,
		cfg:        Config{Suffix: "-", EndChars: " \t,.;:!?)]}", IdleReset: 2 * time.Second, MaxBuffer: 64},
		runeFor:    vkToRune,
		typeTextFn: sendUnicodeText,
	}
}

// Configure applies settings and installs/removes the keyboard hook.
func (e *Engine) Configure(cfg Config) error {
	cfg = normalise(cfg)
	e.mu.Lock()
	e.cfg = cfg
	e.buf = nil
	e.mu.Unlock()

	if cfg.Enabled {
		return e.install()
	}
	e.uninstall()
	return nil
}

func normalise(cfg Config) Config {
	if cfg.Suffix == "" {
		cfg.Suffix = "-"
	}
	if cfg.MaxBuffer <= 0 {
		cfg.MaxBuffer = 64
	}
	if cfg.IdleReset <= 0 {
		cfg.IdleReset = 2 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return cfg
}

// Active reports whether the hook is installed.
func (e *Engine) Active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active
}

// Config returns the current configuration.
func (e *Engine) Config() Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

func (e *Engine) log(format string, args ...any) { e.cfg.Logf(format, args...) }

// ---- keystroke handling --------------------------------------------------

// FeedKey processes one virtual-key code. Returns true when the keystroke
// completed a trigger and must be swallowed. Exported so tests can drive the
// engine without a real hook.
func (e *Engine) FeedKey(vk int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.cfg.Enabled || e.busy {
		return false
	}
	if e.host.IsOwnWindow() {
		e.buf = e.buf[:0]
		return false
	}
	if imeBusy() {
		if len(e.buf) > 0 {
			e.log("expand: skip, IME is composing (%q)", string(e.buf))
		}
		e.buf = e.buf[:0]
		return false
	}

	now := time.Now()
	if now.Sub(e.lastKey) > e.cfg.IdleReset {
		e.buf = e.buf[:0]
	}
	e.lastKey = now
	if hwnd := foregroundWindow(); hwnd != 0 && hwnd != e.lastHwnd {
		e.buf = e.buf[:0]
		e.lastHwnd = hwnd
	}

	switch vk {
	case vkBack:
		if len(e.buf) > 0 {
			e.buf = e.buf[:len(e.buf)-1]
		}
		return false
	}

	r, ok := e.runeFor(vk)
	if e.cfg.Debug {
		e.log("expand debug: vk=%#x rune=%q ok=%v buf=%q", vk, r, ok, string(e.buf))
	}
	if !ok {
		// Not a character (arrow keys, F-keys, modifier-only…): the word ended.
		e.buf = e.buf[:0]
		return false
	}

	e.buf = append(e.buf, r)
	if len(e.buf) > e.cfg.MaxBuffer {
		e.buf = append([]rune(nil), e.buf[len(e.buf)-e.cfg.MaxBuffer:]...)
	}

	// Did what we just typed finish an abbreviation? Two ways:
	//   1. an end-char (space, punctuation) — the natural "I finished the word"
	//   2. the configured suffix (legacy "-")
	if !e.endsWithTerminator() {
		return false
	}
	trimmed, termLen := e.stripTerminator()
	trigger, trigLen, hit := e.host.Match(string(trimmed))
	if e.cfg.Debug {
		e.log("expand debug: candidate=%q hit=%v kind=%s id=%s", string(trimmed), hit, trigger.Kind, trigger.ID)
	}
	if !hit {
		// A word ended without matching anything: start fresh next time.
		e.buf = e.buf[:0]
		return false
	}
	if !e.appAllowed(trigger) {
		e.log("expand: %q skipped, %s is excluded", trigger.Title, e.host.ForegroundProcess())
		e.buf = e.buf[:0]
		return false
	}

	erase := trigLen + termLen
	if trigger.KeepTrigger {
		erase = termLen
	}
	e.buf = e.buf[:0]
	e.busy = true
	go func() {
		defer func() {
			e.mu.Lock()
			e.busy = false
			e.mu.Unlock()
		}()
		e.deliver(trigger, erase)
	}()
	return true
}

// endsWithTerminator reports whether the buffer ends with the suffix or an end-char.
func (e *Engine) endsWithTerminator() bool {
	if len(e.buf) == 0 {
		return false
	}
	last := e.buf[len(e.buf)-1]
	if suffix := []rune(e.cfg.Suffix); len(suffix) > 0 {
		if len(e.buf) >= len(suffix) && string(e.buf[len(e.buf)-len(suffix):]) == string(suffix) {
			return true
		}
	}
	return strings.ContainsRune(e.cfg.EndChars, last)
}

// stripTerminator returns the buffer without its terminator and the terminator
// length in runes (multi-character suffixes supported).
func (e *Engine) stripTerminator() ([]rune, int) {
	if suffix := []rune(e.cfg.Suffix); len(suffix) > 0 && len(e.buf) >= len(suffix) {
		if string(e.buf[len(e.buf)-len(suffix):]) == string(suffix) {
			return e.buf[:len(e.buf)-len(suffix)], len(suffix)
		}
	}
	return e.buf[:len(e.buf)-1], 1
}

// appAllowed applies the per-trigger process filters.
func (e *Engine) appAllowed(t Trigger) bool {
	if len(t.Apps) == 0 && len(t.ExcludeApps) == 0 {
		return true
	}
	proc := strings.ToLower(e.host.ForegroundProcess())
	if proc == "" {
		return true // unknown process: do not block the user
	}
	for _, bad := range t.ExcludeApps {
		if matchesProcess(proc, bad) {
			return false
		}
	}
	if len(t.Apps) == 0 {
		return true
	}
	for _, good := range t.Apps {
		if matchesProcess(proc, good) {
			return true
		}
	}
	return false
}

func matchesProcess(proc, pattern string) bool {
	p := strings.ToLower(strings.TrimSpace(pattern))
	if p == "" {
		return false
	}
	if !strings.HasSuffix(p, ".exe") {
		p += ".exe"
	}
	return proc == p
}

// deliver performs the replacement. Text goes through Unicode injection (no
// clipboard round-trip); images and rich text fall back to the clipboard.
func (e *Engine) deliver(t Trigger, erase int) {
	if erase > 0 {
		sendBackspaces(erase)
	}

	switch t.Kind {
	case "image":
		if err := e.host.PasteImage(t); err != nil {
			e.log("expand: %q image failed: %v", t.Title, err)
		}
		return

	case "app":
		if err := e.host.LaunchApp(t); err != nil {
			e.log("expand: %q launch failed: %v", t.Title, err)
			return
		}
		e.log("expand: %q -> launched", t.Title)
		return

	case "html":
		// Rich text can only travel through the clipboard.
		text, _ := e.host.Render(t)
		if err := e.host.FallbackPaste(text); err != nil {
			e.log("expand: %q html failed: %v", t.Title, err)
			return
		}
		e.log("expand: %q -> rich text pasted", t.Title)
		return

	case "command":
		out, err := e.host.CommandOutput(t)
		if err != nil {
			e.log("expand: %q command failed: %v", t.Title, err)
			return
		}
		e.typeText(out, t)
		return
	}

	text, cursor := e.host.Render(t)
	e.typeText(text, t)
	e.placeCursor(CursorOffset(text, cursor))
}

// typeText injects text at the caret, falling back to the clipboard only when
// the target rejects injected Unicode input.
func (e *Engine) typeText(text string, t Trigger) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		e.log("expand: %q produced no text", t.Title)
		return
	}
	if err := e.typeTextFn(text); err != nil {
		e.log("expand: unicode inject failed (%v), falling back to paste", err)
		if perr := e.host.FallbackPaste(text); perr != nil {
			e.log("expand: fallback paste failed: %v", perr)
			return
		}
	}
	e.log("expand: %q -> %d chars", t.Title, len([]rune(text)))
}

// CursorOffset converts a {{cursor}} position into "how many ← to send".
func CursorOffset(text string, cursor int) int {
	if cursor < 0 {
		return 0
	}
	total := len([]rune(text))
	if cursor >= total {
		return 0
	}
	return total - cursor
}

// IsWordRune reports whether r could be part of a trigger (used by the editor UI
// to warn about triggers that cannot be typed).
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:/", r)
}

// ValidateTrigger explains why a trigger cannot work, or returns "".
func ValidateTrigger(trigger, suffix, endChars string) string {
	if strings.TrimSpace(trigger) == "" {
		return ""
	}
	if strings.TrimSpace(trigger) != trigger {
		return "缩写首尾不要有空格"
	}
	t := trigger
	for _, r := range t {
		if strings.ContainsRune(endChars, r) || r == ' ' {
			return fmt.Sprintf("缩写里的 %q 是终止符，会提前触发", r)
		}
		if !IsWordRune(r) {
			return fmt.Sprintf("缩写里的 %q 不方便直接键入（建议只用字母、数字、- _ . : /）", r)
		}
	}
	if strings.HasSuffix(t, suffix) {
		return fmt.Sprintf("缩写不要以终止符 %q 结尾", suffix)
	}
	return ""
}
