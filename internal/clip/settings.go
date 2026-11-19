package clip

import (
	"sync"
	"time"
)

// Settings controls capture behaviour and the UI. It is persisted as JSON next
// to the item log.
type Settings struct {
	// Capture is the master switch for the listener.
	Capture bool `json:"capture"`
	// Per-kind switches.
	CaptureText  bool `json:"captureText"`
	CaptureLink  bool `json:"captureLink"`
	CaptureImage bool `json:"captureImage"`
	CaptureFiles bool `json:"captureFiles"`
	CaptureRich  bool `json:"captureRich"`

	// CaptureCustom keeps clipboard entries whose format ClipNest does not
	// classify (Excel ranges, colours, audio …). They are stored as raw bytes so
	// they can be put back untouched.
	CaptureCustom bool `json:"captureCustom"`

	// MaxTextLength skips text entries longer than this many characters (0 =
	// unlimited). Guards against pasting whole documents into the history.
	MaxTextLength int `json:"maxTextLength"`

	// Hotkey is a Wails accelerator, e.g. "Ctrl+Shift+V". Empty disables it.
	// Pressing it pops the picker at the text caret and pastes the chosen entry
	// straight into the focused application.
	Hotkey string `json:"hotkey"`
	// CaptureHotkey opens the quick-capture popup (a memo in one keystroke).
	CaptureHotkey string `json:"captureHotkey"`
	// StealthHotkey activates 隐写模式: no visible box, the caret stays where it
	// was, and what you type lands in a memo only when you hit Ctrl+Enter.
	// Empty disables it.
	StealthHotkey string `json:"stealthHotkey"`
	// StealthCommit is the key that commits the invisible buffer (Wails
	// accelerator, "Ctrl+Enter" by default).
	StealthCommit string `json:"stealthCommit"`
	// StealthNewEntry decides what happens when the buffer has no `@名称@`:
	// true = every commit creates a new memo, false = append to the last target.
	StealthNewEntry bool `json:"stealthNewEntry"`
	// PasteOnPick synthesises Ctrl+V after the picker writes the entry to the
	// clipboard. Turning it off makes the picker copy-only.
	PasteOnPick bool `json:"pasteOnPick"`
	// ShowOnLaunch opens the manager window at startup instead of staying in
	// the tray.
	ShowOnLaunch bool `json:"showOnLaunch"`
	// ---- snippets ----
	// SnippetsInPicker shows matching snippets in the quick-paste list.
	SnippetsInPicker bool `json:"snippetsInPicker"`
	// ExpandEnabled turns on inline trigger expansion (a global keyboard hook).
	ExpandEnabled bool `json:"expandEnabled"`
	// ExpandSuffix is the character(s) that complete a trigger, e.g. "-".
	ExpandSuffix string `json:"expandSuffix"`
	// ExpandEndChars are the characters that finish an abbreviation: typing any of
	// them (space, tab, punctuation) expands. This is what makes "addr␣" work
	// without remembering a special suffix.
	ExpandEndChars string `json:"expandEndChars"`

	// ---- appearance ----
	// ThemePreset selects a palette (see frontend/src/lib/themes.ts).
	ThemePreset string `json:"themePreset"`
	// ThemeCustomCSS is user-authored CSS applied on top of the preset.
	ThemeCustomCSS string `json:"themeCustomCss"`

	// UpdateCheck polls GitHub for a newer release shortly after startup.
	UpdateCheck bool `json:"updateCheck"`
	// AutoStart registers the app in HKCU\...\Run.
	AutoStart bool   `json:"autoStart"`
	Theme     string `json:"theme"` // "system" | "dark" | "light"

	// WebDAV sync. SyncURL is a directory; SyncFile is the document name inside
	// that directory.
	SyncEnabled     bool   `json:"syncEnabled"`
	SyncURL         string `json:"syncUrl"`
	SyncUser        string `json:"syncUser"`
	SyncPass        string `json:"syncPass"`
	SyncFile        string `json:"syncFile"`
	SyncIntervalMin int    `json:"syncIntervalMin"`
	// SyncDeviceID is a random per-installation id. Each device writes its own
	// package file, so two machines can never overwrite each other's data.
	SyncDeviceID string `json:"syncDeviceId,omitempty"`
	// SyncSettings shares the non-machine-specific settings (capture toggles,
	// hotkey, paste behaviour, appearance) with other machines.
	SyncSettings bool `json:"syncSettings"`
	// SyncUpdatedAt stamps the last change of the shareable settings, so peers
	// can tell whose copy is newer.
	SyncUpdatedAt time.Time `json:"syncUpdatedAt"`
}

// DefaultSettings is used on first launch or when settings.json is missing.
func DefaultSettings() Settings {
	return Settings{
		Capture:       true,
		CaptureText:   true,
		CaptureLink:   true,
		CaptureImage:  true,
		CaptureFiles:  true,
		CaptureRich:   true,
		CaptureCustom: true,
		MaxTextLength: 100_000,
		Hotkey:        "Ctrl+Shift+V",
		CaptureHotkey: "Ctrl+Shift+N",
		// 隐写模式的激活键：留空表示不注册。默认给一个不常被占用的组合。
		StealthHotkey:   "Ctrl+Alt+N",
		StealthCommit:   "Ctrl+Enter",
		StealthNewEntry: true,
		PasteOnPick:     true,
		ShowOnLaunch:    false,
		AutoStart:       false,
		Theme:           "system",

		SyncEnabled:      false,
		SyncURL:          "",
		SyncUser:         "",
		SyncPass:         "",
		SyncFile:         DefaultSyncFile,
		SyncIntervalMin:  30,
		SyncSettings:     true,
		SnippetsInPicker: true,
		ExpandEnabled:    false,
		ExpandSuffix:     "-",
		ExpandEndChars:   DefaultEndChars,
		ThemePreset:      "default-dark",
		ThemeCustomCSS:   "",
		UpdateCheck:      true,
	}
}

// nowUTC returns a strictly increasing UTC timestamp.
//
// Why not just time.Now(): several rows written in the same microsecond end up
// with identical (or, worse, string-incomparable) timestamps. Every ordering and
// cursor in this app is time-based — pruning "oldest", timeline pagination,
// sync conflict resolution — so ties there show up as flaky behaviour rather
// than a clean error. Forcing monotonicity removes that whole class of bug.
var (
	nowMu   sync.Mutex
	nowLast time.Time
)

func nowUTC() time.Time {
	nowMu.Lock()
	defer nowMu.Unlock()
	t := time.Now().UTC()
	if !t.After(nowLast) {
		t = nowLast.Add(time.Nanosecond)
	}
	nowLast = t
	return t
}
