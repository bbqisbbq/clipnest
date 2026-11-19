//go:build windows

// Package winplat holds the raw Win32 plumbing shared by the clipboard side
// (internal/clip) and the keyboard-hook side (internal/expand).
//
// 为什么单独一个包：这两个包以前各自维护一份 KEYBDINPUT/INPUT 与 SendInput 调用。
// 这类代码的坑是结构体尺寸（sizeof(INPUT) 必须是 40，"4 字节 type + 4 字节 padding
// + 32 字节 union"），记一遍都容易错，记两遍必然有一份是抄错的。现在只有这一份。
//
// 约定：这里只放"薄"的系统调用封装，不带业务语义（谁该在什么时候粘贴，由调用方决定）。
package winplat

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procSendInput          = user32.NewProc("SendInput")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
)

// 虚拟键码。只列这里真正用到的几个。
const (
	VKBack    = 0x08
	VKControl = 0x11
	VKMenu    = 0x12 // Alt
	VKLeft    = 0x25
	VKV       = 0x56
)

const (
	inputKeyboard    = 1
	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004
)

// keyboardInput mirrors KEYBDINPUT.
type keyboardInput struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// inputT mirrors INPUT. inputT must be sizeof(INPUT) == 40 on x64: 4 bytes type
// + 4 padding + the 32-byte union (MOUSEINPUT is its largest member)，否则
// SendInput 直接以 ERROR_INVALID_PARAMETER 拒绝整批。
type inputT struct {
	Type uint32
	_    uint32
	Ki   keyboardInput
	_    [8]byte // union 里 MOUSEINPUT 比 KEYBDINPUT 大的那 8 字节
}

// 尺寸守门：unsafe.Sizeof 是常量表达式，尺寸一旦不是 40 这条就编译失败
// （index out of bounds），而不是等到运行时 SendInput 静默返回 0。
var _ = [1]struct{}{}[unsafe.Sizeof(inputT{})-40]

func send(batch []inputT) error {
	if len(batch) == 0 {
		return nil
	}
	ret, _, err := procSendInput.Call(
		uintptr(len(batch)),
		uintptr(unsafe.Pointer(&batch[0])),
		unsafe.Sizeof(batch[0]),
	)
	if int(ret) != len(batch) {
		return fmt.Errorf("SendInput %d/%d: %v", ret, len(batch), err)
	}
	return nil
}

// SendUnicode types text as Unicode keystrokes (KEYEVENTF_UNICODE), which needs
// no clipboard and works in nearly every editor. 分批投递（每批 64 个 UTF-16
// 单元）是因为一次性塞上千个事件时目标程序会丢键或卡很久。
func SendUnicode(text string) error {
	if text == "" {
		return nil
	}
	units, _ := windows.UTF16FromString(text)
	if len(units) > 0 {
		units = units[:len(units)-1] // drop trailing NUL
	}
	const chunk = 64
	for start := 0; start < len(units); start += chunk {
		end := min(start+chunk, len(units))
		batch := make([]inputT, 0, (end-start)*2)
		for _, u := range units[start:end] {
			batch = append(batch,
				inputT{Type: inputKeyboard, Ki: keyboardInput{WScan: u, DwFlags: keyeventfUnicode}},
				inputT{Type: inputKeyboard, Ki: keyboardInput{WScan: u, DwFlags: keyeventfUnicode | keyeventfKeyUp}},
			)
		}
		if err := send(batch); err != nil {
			return err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// Backspaces deletes n characters in one batch (atomic from the app's view).
func Backspaces(n int) {
	if n <= 0 {
		return
	}
	n = min(n, 200)
	batch := make([]inputT, 0, n*2)
	for i := 0; i < n; i++ {
		batch = append(batch,
			inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: VKBack}},
			inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: VKBack, DwFlags: keyeventfKeyUp}},
		)
	}
	_ = send(batch)
}

// LeftArrows moves the caret left one keypress at a time（{{cursor}} 用）。逐次
// 投递而不是一批，是因为不少控件对"一批连按 ←"只处理第一个。
func LeftArrows(n int) {
	if n <= 0 {
		return
	}
	n = min(n, 200)
	for i := 0; i < n; i++ {
		_ = send([]inputT{
			{Type: inputKeyboard, Ki: keyboardInput{WVk: VKLeft}},
			{Type: inputKeyboard, Ki: keyboardInput{WVk: VKLeft, DwFlags: keyeventfKeyUp}},
		})
		time.Sleep(6 * time.Millisecond)
	}
}

// TapKey presses and releases one key.
func TapKey(vk uint16) {
	_ = send([]inputT{
		{Type: inputKeyboard, Ki: keyboardInput{WVk: vk}},
		{Type: inputKeyboard, Ki: keyboardInput{WVk: vk, DwFlags: keyeventfKeyUp}},
	})
}

// Chord holds the modifiers down, taps vk, then releases the modifiers in
// reverse order — one batch so nothing else can interleave.
func Chord(mods []uint16, vk uint16) error {
	batch := make([]inputT, 0, 2*len(mods)+2)
	for _, m := range mods {
		batch = append(batch, inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: m}})
	}
	batch = append(batch,
		inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vk}},
		inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vk, DwFlags: keyeventfKeyUp}},
	)
	for i := len(mods) - 1; i >= 0; i-- {
		batch = append(batch, inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: mods[i], DwFlags: keyeventfKeyUp}})
	}
	return send(batch)
}

// Paste synthesises Ctrl+V into whatever window currently has focus.
func Paste() error {
	if err := Chord([]uint16{VKControl}, VKV); err != nil {
		return fmt.Errorf("粘贴键未送达：%w", err)
	}
	return nil
}

// ForegroundWindow returns the focused top-level window handle (0 on failure).
func ForegroundWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return hwnd
}
