//go:build !windows

package clip

// SetWindowBackdrop 只在 Windows 上有实现（DWM 的 Acrylic 背景层）；其它平台
// 永远返回 false，调用方据此退回不透明卡片。
func SetWindowBackdrop(hwnd uintptr, acrylic bool) bool { return false }
