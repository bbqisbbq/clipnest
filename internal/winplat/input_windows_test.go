//go:build windows

package winplat

import (
	"testing"
	"unsafe"
)

// SendInput 对 cbSize 很挑：必须是 sizeof(INPUT)，x64 下 40 字节
// （4 类型 + 4 对齐 + 32 的 union，MOUSEINPUT 是最大成员）。尺寸错了不是
// "少打几个字"，而是整批直接返回 0 + ERROR_INVALID_PARAMETER。
func TestInputStructSize(t *testing.T) {
	if got := unsafe.Sizeof(keyboardInput{}); got != 24 {
		t.Fatalf("sizeof(KEYBDINPUT) = %d, want 24", got)
	}
	if got := unsafe.Sizeof(inputT{}); got != 40 {
		t.Fatalf("sizeof(INPUT) = %d, want 40", got)
	}
}
