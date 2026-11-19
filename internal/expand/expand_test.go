package expand

import (
	"strings"
	"testing"
	"time"
)

// fakeHost implements Host in memory so the engine can be tested without Win32.
type fakeHost struct {
	triggers  []Trigger
	proc      string
	ownWindow bool

	typed     []string // text injected through Render
	commands  []string // trigger IDs whose command ran
	images    []string
	launched  []string
	fallbacks []string
}

func (h *fakeHost) Match(buf string) (Trigger, int, bool) {
	runes := []rune(buf)
	best := 0
	var found Trigger
	for _, t := range h.triggers {
		key := []rune(t.triggerKey())
		if len(key) > len(runes) {
			continue
		}
		tail := runes[len(runes)-len(key):]
		match := string(tail) == string(key)
		if !match && !t.CaseSensitive {
			match = strings.EqualFold(string(tail), string(key))
		}
		if match && len(key) > best {
			best = len(key)
			found = t
		}
	}
	if best == 0 {
		return Trigger{}, 0, false
	}
	return found, best, true
}

func (t Trigger) triggerKey() string { return t.ID }

func (h *fakeHost) Render(t Trigger) (string, int) { return t.Body, -1 }
func (h *fakeHost) CommandOutput(t Trigger) (string, error) {
	h.commands = append(h.commands, t.ID)
	return t.Body, nil
}
func (h *fakeHost) PasteImage(t Trigger) error { h.images = append(h.images, t.ID); return nil }
func (h *fakeHost) LaunchApp(t Trigger) error  { h.launched = append(h.launched, t.ID); return nil }
func (h *fakeHost) FallbackPaste(text string) error {
	h.fallbacks = append(h.fallbacks, text)
	return nil
}
func (h *fakeHost) ForegroundProcess() string { return h.proc }
func (h *fakeHost) IsOwnWindow() bool         { return h.ownWindow }

func (h *fakeHost) idle() bool { return true }

// feed types a whole string one rune at a time and returns the swallowed keys.
func feed(e *Engine, s string) []bool {
	out := make([]bool, 0, len(s))
	for _, r := range s {
		out = append(out, e.FeedKey(vkFor(r)))
	}
	return out
}

// vkFor / runeFor are a fake keyboard: they map characters to VKs and back
// without touching Win32, so the engine logic is testable anywhere.
func vkFor(r rune) int {
	switch {
	case r >= 'a' && r <= 'z':
		return int(r-'a') + 0x41
	case r >= 'A' && r <= 'Z':
		return int(r-'A') + 0x41
	case r == '-':
		return 0xBD
	case r == ' ':
		return 0x20
	}
	return 0
}

// runeFor is the inverse of vkFor.
func runeFor(vk int) (rune, bool) {
	switch {
	case vk >= 0x41 && vk <= 0x5A:
		return rune('a' + (vk - 0x41)), true
	case vk == 0xBD:
		return '-', true
	case vk == 0x20:
		return ' ', true
	}
	return 0, false
}

func newEngine(h *fakeHost, cfg Config) *Engine {
	e := New(h)
	e.mu.Lock()
	e.cfg = normalise(cfg)
	e.cfg.Enabled = true
	e.runeFor = runeFor
	e.typeTextFn = func(s string) error { h.typed = append(h.typed, s); return nil }
	e.mu.Unlock()
	return e
}

func TestEndCharsFinishAnAbbreviation(t *testing.T) {
	host := &fakeHost{triggers: []Trigger{{ID: "addr", Kind: "text", Body: "杭州市滨江区"}}}
	e := newEngine(host, Config{Suffix: "-", EndChars: " \t,."})

	swallowed := feed(e, "addr ")
	if !swallowed[len(swallowed)-1] {
		t.Fatal("the terminator itself must be swallowed")
	}
	waitFor(t, func() bool { return len(host.typed) == 1 })
	if host.typed[0] != "杭州市滨江区" {
		t.Errorf("typed %q", host.typed[0])
	}
}

func TestSuffixStillWorks(t *testing.T) {
	host := &fakeHost{triggers: []Trigger{{ID: "addr", Kind: "text", Body: "X"}}}
	e := newEngine(host, Config{Suffix: "-", EndChars: ""})
	feed(e, "addr-")
	waitFor(t, func() bool { return len(host.typed) == 1 })
}

func TestNoTriggerNoSwallow(t *testing.T) {
	host := &fakeHost{triggers: []Trigger{{ID: "addr", Kind: "text", Body: "X"}}}
	e := newEngine(host, Config{Suffix: "-", EndChars: " "})
	swallowed := feed(e, "hello ")
	for i, s := range swallowed {
		if s {
			t.Fatalf("key %d should not be swallowed", i)
		}
	}
	if len(host.typed) != 0 {
		t.Error("nothing should have expanded")
	}
}

func TestOwnWindowNeverExpands(t *testing.T) {
	host := &fakeHost{ownWindow: true, triggers: []Trigger{{ID: "addr", Kind: "text", Body: "X"}}}
	e := newEngine(host, Config{Suffix: "-", EndChars: " "})
	feed(e, "addr ")
	time.Sleep(20 * time.Millisecond)
	if len(host.typed) != 0 {
		t.Error("must not expand inside our own windows")
	}
}

func TestAppFilters(t *testing.T) {
	onlyIDE := Trigger{ID: "ide", Kind: "text", Body: "X", Apps: []string{"code"}}
	neverShell := Trigger{ID: "sh", Kind: "text", Body: "Y", ExcludeApps: []string{"windowsterminal"}}

	host := &fakeHost{proc: "code.exe", triggers: []Trigger{onlyIDE, neverShell}}
	e := newEngine(host, Config{Suffix: "-", EndChars: " "})
	feed(e, "ide ")
	waitFor(t, func() bool { return len(host.typed) == 1 })

	host2 := &fakeHost{proc: "notepad.exe", triggers: []Trigger{onlyIDE}}
	e2 := newEngine(host2, Config{Suffix: "-", EndChars: " "})
	feed(e2, "ide ")
	time.Sleep(20 * time.Millisecond)
	if len(host2.typed) != 0 {
		t.Error("Apps list must restrict expansion to listed processes")
	}

	host3 := &fakeHost{proc: "WindowsTerminal.exe", triggers: []Trigger{neverShell}}
	e3 := newEngine(host3, Config{Suffix: "-", EndChars: " "})
	feed(e3, "sh ")
	time.Sleep(20 * time.Millisecond)
	if len(host3.typed) != 0 {
		t.Error("ExcludeApps must block expansion")
	}
}

func TestCommandOutputIsTypedAndImagesPasted(t *testing.T) {
	host := &fakeHost{triggers: []Trigger{
		{ID: "gb", Kind: "command", Body: "main"},
		{ID: "logo", Kind: "image", Body: "logo"},
	}}
	e := newEngine(host, Config{Suffix: "-", EndChars: " "})
	feed(e, "gb ")
	waitFor(t, func() bool { return len(host.commands) == 1 })

	feed(e, "logo ")
	waitFor(t, func() bool { return len(host.images) == 1 })
}

func TestLongestTriggerWins(t *testing.T) {
	host := &fakeHost{triggers: []Trigger{
		{ID: "addr", Kind: "text", Body: "short"},
		{ID: "myaddr", Kind: "text", Body: "long"},
	}}
	e := newEngine(host, Config{Suffix: "-", EndChars: " "})
	feed(e, "myaddr ")
	waitFor(t, func() bool { return len(host.typed) == 1 })
	if host.typed[0] != "long" {
		t.Errorf("expected the longer trigger, got %q", host.typed[0])
	}
}

func TestValidateTrigger(t *testing.T) {
	cases := []struct {
		trigger  string
		endChars string
		want     string
	}{
		{"addr", " ", ""},
		{"", " ", ""},
		{"addr.", " .", "是终止符"},
		{"addr ", " ", "空格"},
	}
	for _, c := range cases {
		got := ValidateTrigger(c.trigger, "-", c.endChars)
		if c.want == "" && got != "" {
			t.Errorf("ValidateTrigger(%q) = %q, want empty", c.trigger, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("ValidateTrigger(%q) = %q, want %q", c.trigger, got, c.want)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the engine to expand")
}
