//go:build !windows

package clip

// SetWindowNoBorder 只有 Windows 有实现（DWM 边框）。
func SetWindowNoBorder(uintptr) {}
