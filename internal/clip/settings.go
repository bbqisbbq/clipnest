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
	// CaptureTransparency is how much of the desktop shows through the
	// quick-capture popup, in percent (0 = solid card, the default).
	//
	// 窗口本身是透明的（圆角外侧透出桌面），这一项只改卡片底色的 alpha：
	// 0 时卡片完全不透明，只是四角真透明；大于 0 才有"能看见后面的窗口"的效果。
	// WebView2 糊不了桌面背景，所以这里给不了毛玻璃，只能靠降低 alpha。
	CaptureTransparency int `json:"captureTransparency"`
	// ThemeCustomCSS is user-authored CSS applied on top of the preset.
	ThemeCustomCSS string `json:"themeCustomCss"`
	// EditorToolbar / ComposerToolbar 决定工具栏显示哪些按钮（Vditor 的 item 名）。
	// 空数组 = 用默认的一整套；`|` 表示分隔线。见 frontend/src/lib/editorToolbar.ts。
	EditorToolbar   []string `json:"editorToolbar"`
	ComposerToolbar []string `json:"composerToolbar"`

	// ---- navigation ----
	// DefaultFilterID 是启动时自动选中的侧栏筛选器（空 = 用第一个）。
	// 机器本地：筛选器本身不进同步包，id 在另一台机器上很可能不存在，
	// 跟着设置同步过去只会让那台机器启动到一个"找不到"的筛选器上。
	DefaultFilterID string `json:"defaultFilterId"`

	// UpdateCheck polls GitHub for a newer release shortly after startup.
	UpdateCheck bool `json:"updateCheck"`
	// AutoStart registers the app in HKCU\...\Run.
	AutoStart bool   `json:"autoStart"`
	Theme     string `json:"theme"` // "system" | "dark" | "light"

	// WebDAV sync. SyncURL 是目录；每台机器只写自己那一个包
	// （clipnest_data/<deviceId>-<host>.zip，见 sync_dav.go 的 packageName），
	// 所以没有任何"共享文档名"要配置——旧的 SyncFile 字段已删除。
	SyncEnabled     bool   `json:"syncEnabled"`
	SyncURL         string `json:"syncUrl"`
	SyncUser        string `json:"syncUser"`
	SyncPass        string `json:"syncPass"`
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
		PasteOnPick:   true,
		ShowOnLaunch:  false,
		AutoStart:     false,
		Theme:         "system",

		SyncEnabled:      false,
		SyncURL:          "",
		SyncUser:         "",
		SyncPass:         "",
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

// MaxCaptureTransparency caps the popup's card alpha (100 = 卡片完全透明，只剩文字与一圈细边).
//
// 这个上限**不是**可读性保障：超过 ~50% 之后文字就压在你背后的内容上，遇到花哨的背景
// 会看不清——那是用户自己的取舍，设置页的说明里写清楚了。这里只负责别让脏值（手改
// settings.json、对端同步包）超出物理范围。
const MaxCaptureTransparency = 100

// normalize clamps values that come from settings.json (user-editable) or from
// the UI. Nothing downstream may assume a field arrived validated.
func (st *Settings) normalize() {
	switch {
	case st.CaptureTransparency < 0:
		st.CaptureTransparency = 0
	case st.CaptureTransparency > MaxCaptureTransparency:
		st.CaptureTransparency = MaxCaptureTransparency
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
