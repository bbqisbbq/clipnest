package clip

import "errors"

// ErrNotSupported 是"当前平台还没实现这项能力"的统一错误（不是 bug）。
//
// 它必须住在中立文件里：Windows 侧与 platform_other.go 的桩都要引用同一个值，
// 以前它声明在 watcher_windows.go，于是非 Windows 编译时桩自己都编不过。
var ErrNotSupported = errors.New("clipboard integration is only implemented on Windows")
