//go:build windows

package clip

import "unsafe"

// Windows 11 会给每个窗口（包括无边框的）画一圈 1px 的边框，颜色由 DWMWA_BORDER_COLOR 决定，
// 不设置时用系统默认（暗色主题下是一条浅灰线）。对圆角卡片 + 透明窗口来说，这条线正好压在
// 本该透出去的边上 —— 黑底上看就是一圈「黑边」（实测 rgb(47,47,47)）。
//
// 这里把它显式设成 DWMWA_COLOR_NONE，让 DWM 不画。主窗是方形带边框的，不调用这个。

// DWMWA_COLOR_NONE：让 DWM 完全不要画边框/标题栏（文档里的魔法值，按 uint32 解释）。
const dwmColorNone = uint32(0xFFFFFFFE)

// SetWindowNoBorder 让 DWM 不再给这个窗口画 1px 边框（弹窗专用）。
func SetWindowNoBorder(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	v := dwmColorNone
	dwmapi.NewProc("DwmSetWindowAttribute").Call(hwnd, uintptr(dwmwaBorderColor),
		uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
}

