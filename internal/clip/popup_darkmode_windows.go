//go:build windows

package clip

import "unsafe"

// 弹窗（快速粘贴 / 记一笔 / 截图）需要和主题同步的只有一枚 DWM 标志：immersive-dark。
//
// 为什么单独拎出来：它们的边框由 SetWindowNoBorder 关掉（DWM 那条 1px 线会压在圆角透出去
// 的边上），但 immersive-dark 必须跟着主题走 —— DWM 的亚克力（弹窗半透明用的那层）会按这枚
// 标志决定用浅色还是深色材质。实测（100% 透明、只看亚克力本身）：不设的时候浅色/深色主题
// 量到一模一样的 (153,151,155)，也就是深色主题下弹窗仍然糊着一层浅色玻璃。
func SetWindowDarkMode(hwnd uintptr, dark bool) {
	if hwnd == 0 {
		return
	}
	v := int32(0)
	if dark {
		v = 1
	}
	dwmapi.NewProc("DwmSetWindowAttribute").Call(hwnd, uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
}
