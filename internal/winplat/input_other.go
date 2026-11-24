//go:build !windows

// Package winplat 的**非 Windows 桩**：只保证"能编译、能跑单测"，任何调用都返回 ErrNotSupported。
//
// 存在的意义是一个编译门禁：`scripts/check-platforms.ps1` 会对 linux/darwin 跑
// `go build ./internal/...`，这样"业务包里混进了 Win32 调用"会当场报红，而不是等到
// 有人真的为 macOS/Linux 开工时才发现。给新平台补实现时，**只改这个文件**（与 platform_other.go
// 里对应的那批函数），业务层一行都不用动。
package winplat

import "errors"

// ErrNotSupported 表示当前平台没有该能力（不是 bug，是还没实现）。
var ErrNotSupported = errors.New("winplat: 当前平台不支持键盘/输入注入")

// 虚拟键码，与 input_windows.go 保持一致（跨平台代码可以直接引用这几个数字）。
const (
	VKBack    = 0x08
	VKControl = 0x11
	VKMenu    = 0x12 // Alt
	VKLeft    = 0x25
	VKV       = 0x56
)

func SendUnicode(string) error      { return ErrNotSupported }
func Backspaces(int)                {}
func LeftArrows(int)                {}
func TapKey(uint16)                 {}
func Chord([]uint16, uint16) error  { return ErrNotSupported }
func Paste() error                  { return ErrNotSupported }
func ForegroundWindow() uintptr     { return 0 }
