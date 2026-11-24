package clip

// 截图的数据契约（中立文件）：Windows 抓屏（shot_windows.go）产出它，
// 业务面（service_shot.go）与平台桩（platform_other.go）都引用它。
// 类型放在 Windows 文件里会让非 Windows 编译时 service 层编不过。

// ShotFrame is one frozen screen grab: the monitor under the cursor, in physical
// pixels, already encoded as PNG (what the overlay shows and what a crop slices).
type ShotFrame struct {
	PNG    []byte
	Left   int
	Top    int
	Width  int
	Height int
}
