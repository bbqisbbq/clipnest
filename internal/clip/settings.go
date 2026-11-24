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

	// CaptureRemoteImage：网页上「复制图片」时剪贴板里常常只有一段 <img src>、
	// 没有位图（见 capture_imgfetch.go）。开着就去把那张图下载回来存成图片条目，
	// 关掉则只记一条带地址的富文本。要联网，所以做成开关。
	CaptureRemoteImage bool `json:"captureRemoteImage"`

	// MaxTextLength skips text entries longer than this many characters (0 =
	// unlimited). Guards against pasting whole documents into the history.
	MaxTextLength int `json:"maxTextLength"`

	// Hotkey is a Wails accelerator, e.g. "Ctrl+Shift+V". Empty disables it.
	// Pressing it pops the picker at the text caret and pastes the chosen entry
	// straight into the focused application.
	Hotkey string `json:"hotkey"`
	// CaptureHotkey opens the quick-capture popup (a memo in one keystroke).
	CaptureHotkey string `json:"captureHotkey"`
	// ShotHotkey opens the region-screenshot overlay (Ctrl+Shift+A by default).
	// Empty disables it.
	ShotHotkey string `json:"shotHotkey"`
	// CopyShotToClipboard puts the finished screenshot on the clipboard as well,
	// so it can be pasted straight away. Off = only the history entry.
	CopyShotToClipboard bool `json:"copyShotToClipboard"`
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
	// PrivacyInput 打开后，编辑器里只有光标所在的那个段落保持可读，其余段落被遮成
	// 极淡（色调贴近背景）。这是**纯视觉**保护：不影响保存、不影响已保存的内容、
	// 也不进同步包（机器本地字段，见 sync_merge.go 的 localSettingsFields）。
	PrivacyInput bool `json:"privacyInput"`
	// OcrEngine 选哪个 OCR 引擎：`system`（默认，Windows 自带，零依赖）或 `ppocr`
	// （PP-OCRv5 + onnxruntime，要下载 42MB 资源，中文和小字的准确率明显更高）。
	// 机器本地字段：算力是本机的，模型也是本机装的。
	OcrEngine string `json:"ocrEngine"`
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
		// 网页图片自动下载：默认开。浏览器复制图片时经常不给位图，关掉就只能存地址。
		CaptureRemoteImage: true,
		MaxTextLength:      100_000,
		Hotkey:             "Ctrl+Shift+V",
		CaptureHotkey:      "Ctrl+Shift+N",
		ShotHotkey:         DefaultShotHotkey,
		// 截完顺手进剪贴板：截图这个动作 90% 是为了马上用掉它。
		CopyShotToClipboard: true,
		PasteOnPick:         true,
		ShowOnLaunch:        false,
		AutoStart:           false,
		Theme:               "system",

		SyncEnabled:      false,
		SyncURL:          "",
		SyncUser:         "",
		SyncPass:         "",
		SyncIntervalMin:  120,
		SyncSettings:     true,
		SnippetsInPicker: true,
		ExpandEnabled:    false,
		ExpandSuffix:     "-",
		ExpandEndChars:   DefaultEndChars,
		ThemePreset:      "default-dark",
		ThemeCustomCSS:   "",
		UpdateCheck:      true,
		OcrEngine:        OcrEngineSystem,
	}
}

// MaxCaptureTransparency caps the popup's card alpha (100 = 卡片完全透明，只剩文字与一圈细边).
//
// 这个上限**不是**可读性保障：超过 ~50% 之后文字就压在你背后的内容上，遇到花哨的背景
// 会看不清——那是用户自己的取舍，设置页的说明里写清楚了。这里只负责别让脏值（手改
// settings.json、对端同步包）超出物理范围。
const MaxCaptureTransparency = 100

// MaxSyncIntervalMin caps the auto-sync interval at one day (0 = 只在手动点击时同步).
//
// 包是全量的，所以间隔不再是"越小越好"：一台攒了几千条的机器每半小时推一遍整包
// 纯属浪费配额。默认给 120 分钟，上限一天，够用也不至于让两台机器长期对不上。
const MaxSyncIntervalMin = 1440

// normalize clamps values that come from settings.json (user-editable) or from
// the UI. Nothing downstream may assume a field arrived validated.
func (st *Settings) normalize() {
	switch {
	case st.CaptureTransparency < 0:
		st.CaptureTransparency = 0
	case st.CaptureTransparency > MaxCaptureTransparency:
		st.CaptureTransparency = MaxCaptureTransparency
	}
	switch st.OcrEngine {
	case "", OcrEngineSystem:
		st.OcrEngine = OcrEngineSystem
	case OcrEnginePPOCR:
	default:
		st.OcrEngine = OcrEngineSystem // 认不出来的值一律退回默认，别让引擎选择卡住整个识别
	}
	switch {
	case st.SyncIntervalMin < 0:
		st.SyncIntervalMin = 0
	case st.SyncIntervalMin > MaxSyncIntervalMin:
		st.SyncIntervalMin = MaxSyncIntervalMin
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
