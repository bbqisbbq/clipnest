//go:build !windows

package clip

// 非 Windows 上没有热键预检（热键注册本身也只是桩），一律报告"没被占用"。
func hotkeyTaken(string) bool { return false }
