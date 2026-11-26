//go:build !windows

package clip

// SetWindowDarkMode 只有 Windows 有实现（DWM 的 immersive-dark 标志）。
func SetWindowDarkMode(uintptr, bool) {}
