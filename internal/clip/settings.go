package clip

import "time"

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
	// MaxItems caps the history; oldest unpinned entries are pruned first.
	MaxItems int `json:"maxItems"`
	// MaxAgeDays drops entries older than this (0 = keep forever).
	MaxAgeDays int `json:"maxAgeDays"`

	// IgnorePatterns are regular expressions; a capture whose text, preview or
	// any file name matches one of them is dropped (password managers etc.).
	IgnorePatterns []string `json:"ignorePatterns"`
	// IgnoreApps are executable names (e.g. "keepass.exe") whose copies are
	// never recorded.
	IgnoreApps []string `json:"ignoreApps"`

	// Hotkey is a Wails accelerator, e.g. "Ctrl+Shift+V". Empty disables it.
	// Pressing it pops the picker at the text caret and pastes the chosen entry
	// straight into the focused application.
	Hotkey string `json:"hotkey"`
	// PasteOnPick synthesises Ctrl+V after the picker writes the entry to the
	// clipboard. Turning it off makes the picker copy-only.
	PasteOnPick bool `json:"pasteOnPick"`
	// ShowOnLaunch opens the manager window at startup instead of staying in
	// the tray.
	ShowOnLaunch bool `json:"showOnLaunch"`
	// CloseToTray hides the window on close instead of quitting. Kept for
	// documentation: it is always true in this build.
	CloseToTray bool `json:"closeToTray"`
	// PasteOnCopy writes the selected entry back to the clipboard when the user
	// picks it (always effectively true; the flag lets the UI stay honest).
	AutoStart bool `json:"autoStart"`
	Theme     string `json:"theme"` // "system" | "dark" | "light"
}

// DefaultSettings is used on first launch or when settings.json is missing.
func DefaultSettings() Settings {
	return Settings{
		Capture:        true,
		CaptureText:    true,
		CaptureLink:    true,
		CaptureImage:   true,
		CaptureFiles:   true,
		CaptureRich:    true,
		CaptureCustom:  true,
		MaxTextLength:  100_000,
		MaxItems:       1000,
		MaxAgeDays:     30,
		IgnorePatterns: nil,
		IgnoreApps:     nil,
		Hotkey:         "Ctrl+Shift+V",
		PasteOnPick:    true,
		ShowOnLaunch:   false,
		CloseToTray:    true,
		AutoStart:      false,
		Theme:          "system",
	}
}

// clone returns a deep copy so callers can mutate freely.
func (s Settings) clone() Settings {
	out := s
	out.IgnorePatterns = append([]string(nil), s.IgnorePatterns...)
	out.IgnoreApps = append([]string(nil), s.IgnoreApps...)
	return out
}

func nowUTC() time.Time { return time.Now().UTC() }
