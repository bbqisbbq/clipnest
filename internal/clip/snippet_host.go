package clip

import (
	"errors"
	"strings"
	"sync"
	"unicode"

	"github.com/bbqisbbq/clipnest/internal/expand"
)

// This file is the bridge between the snippet domain (SQLite, groups, actions)
// and the trigger engine (internal/expand). The engine knows nothing about
// ClipNest; ClipService implements expand.Host here.
//
// Trigger index: kept in memory because the engine consults it on every
// keystroke. Two maps — exact (case-sensitive triggers) and lowercased (the
// default, case-insensitive behaviour) — plus the longest registered length so
// the suffix scan stays short.

type triggerIndex struct {
	exact    map[string]expand.Trigger
	folded   map[string]expand.Trigger
	maxRunes int
}

var (
	trigMu    sync.RWMutex
	trigIndex = triggerIndex{exact: map[string]expand.Trigger{}, folded: map[string]expand.Trigger{}}
)

// refreshTriggers rebuilds the in-memory index from the snippet table.
func (s *ClipService) refreshTriggers() {
	next := triggerIndex{exact: map[string]expand.Trigger{}, folded: map[string]expand.Trigger{}}
	for _, sp := range s.store.AllSnippets() {
		key := strings.TrimSpace(sp.Trigger)
		if key == "" {
			continue
		}
		t := expand.Trigger{
			ID:            sp.ID,
			Kind:          string(sp.Kind),
			Body:          sp.Body,
			Title:         sp.Title,
			CaseSensitive: sp.CaseSensitive,
			KeepTrigger:   sp.KeepTrigger,
			Apps:          sp.Apps,
			ExcludeApps:   sp.ExcludeApps,
		}
		// Index the trigger as typed plus its "clean" forms: people write "addr-"
		// in the editor but the engine matches "addr" (the terminator is stripped
		// before matching), and trailing punctuation is never part of a word.
		suffix := suffixOf(s.store.Settings().ExpandSuffix)
		aliases := []string{key}
		if trimmed := strings.TrimSuffix(key, suffix); trimmed != "" && trimmed != key {
			aliases = append(aliases, trimmed)
		}
		if trimmed := strings.TrimRightFunc(key, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}); trimmed != "" && trimmed != key {
			aliases = append(aliases, trimmed)
		}
		for _, alias := range aliases {
			if n := len([]rune(alias)); n > next.maxRunes {
				next.maxRunes = n
			}
			if sp.CaseSensitive {
				// Case matters: only the exact spelling is registered.
				next.exact[alias] = t
				continue
			}
			next.folded[strings.ToLower(alias)] = t
		}
	}
	trigMu.Lock()
	trigIndex = next
	trigMu.Unlock()
}

// Match returns the longest trigger that buf ends with (engine-side matching).
func (s *ClipService) Match(buf string) (expand.Trigger, int, bool) {
	if buf == "" {
		return expand.Trigger{}, 0, false
	}
	runes := []rune(buf)

	trigMu.RLock()
	idx := trigIndex
	trigMu.RUnlock()

	max := len(runes)
	if idx.maxRunes < max {
		max = idx.maxRunes
	}
	for l := max; l >= 1; l-- {
		tail := string(runes[len(runes)-l:])
		if t, ok := idx.exact[tail]; ok {
			return t, l, true
		}
		if t, ok := idx.folded[strings.ToLower(tail)]; ok {
			return t, l, true
		}
	}
	return expand.Trigger{}, 0, false
}

// Render substitutes template variables for text-like triggers and reports the
// {{cursor}} offset.
func (s *ClipService) Render(t expand.Trigger) (string, int) {
	switch SnippetKind(t.Kind) {
	case SnippetText, SnippetPassword, SnippetURL:
		return renderTemplate(t.Body)
	case SnippetFile:
		return renderTemplate(strings.Join(splitPaths(t.Body), "\n"))
	case SnippetHTML:
		return renderTemplate(t.Body)
	}
	return "", -1
}

// CommandOutput runs a command trigger and returns its output for typing.
func (s *ClipService) CommandOutput(t expand.Trigger) (string, error) {
	return s.snippetCommandOutput(t.ID)
}

// PasteImage inserts an image trigger (images have no text form).
func (s *ClipService) PasteImage(t expand.Trigger) error { return s.pasteImageInline(t.ID) }

// LaunchApp starts a program trigger.
func (s *ClipService) LaunchApp(t expand.Trigger) error {
	s.store.MarkSnippetUsed(t.ID)
	return launchProgram(t.Body)
}

// FallbackPaste delivers text through the clipboard when injection is refused.
func (s *ClipService) FallbackPaste(text string) error {
	if err := WriteTextToClipboard(text); err != nil {
		return err
	}
	return SendPaste()
}

// ForegroundProcess is the focused window's image name ("code.exe").
func (s *ClipService) ForegroundProcess() string {
	hwnd := ForegroundWindowHandle()
	if hwnd == 0 {
		return ""
	}
	return ProcessImageName(hwnd)
}

// IsOwnWindow reports whether ClipNest itself is focused.
func (s *ClipService) IsOwnWindow() bool {
	s.mu.Lock()
	fn := s.ownWindow
	s.mu.Unlock()
	if fn == nil {
		return false
	}
	return fn()
}

// SetOwnWindowCheck lets the app tell the engine which windows are ours, so
// triggers never fire inside ClipNest's own inputs.
func (s *ClipService) SetOwnWindowCheck(fn func() bool) {
	s.mu.Lock()
	s.ownWindow = fn
	s.mu.Unlock()
}

// ExpandActive reports whether the keyboard hook is installed.
func (s *ClipService) ExpandActive() bool { return s.engine.Active() }

// TriggerPreview validates a trigger against the current settings and returns a
// warning for the editor ("" when the trigger is fine).
func (s *ClipService) TriggerPreview(trigger string) string {
	st := s.store.Settings()
	return expand.ValidateTrigger(trigger, st.ExpandSuffix, st.ExpandEndChars)
}

// errEngineMissing keeps call sites honest while the engine is being wired.
var errEngineMissing = errors.New("触发器引擎未初始化")

// DefaultEndChars are the characters that finish an abbreviation. Space and tab
// cover "type the word, hit space"; the punctuation covers writing prose.
const DefaultEndChars = " \t,.;:!?)]}"
// suffixOf normalises the configured terminator ("-" when empty).
func suffixOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}