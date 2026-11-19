//go:build windows

package clip

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file holds the "paste at the caret" plumbing for the picker: where to put
// the popup, how to take and give back focus, and how to synthesise Ctrl+V.

var dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

var (
	procGetGUIThreadInfo    = user32.NewProc("GetGUIThreadInfo")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procClientToScreen      = user32.NewProc("ClientToScreen")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsIconic            = user32.NewProc("IsIconic")
	procAttachThreadInput   = user32.NewProc("AttachThreadInput")
	procSetFocus            = user32.NewProc("SetFocus")
	procGetCurrentThreadID  = kernel32.NewProc("GetCurrentThreadId")
	procSendInput           = user32.NewProc("SendInput")
)

const (
	monitorDefaultToNearest = 0x00000002
	swRestore               = 9
	vkControl               = 0x11
	vkBack                  = 0x08
	vkLeft                  = 0x25
	vkMenu                  = 0x12
	vkV                     = 0x56
	inputKeyboard           = 1
	keyeventfKeyUp          = 0x0002
	keyeventfUnicode        = 0x0004
)

type pointT struct {
	X int32
	Y int32
}

type rectT struct {
	Left, Top, Right, Bottom int32
}

type monitorInfoT struct {
	CbSize    uint32
	RcMonitor rectT
	RcWork    rectT
	DwFlags   uint32
}

type guiThreadInfo struct {
	CbSize        uint32
	Flags         uint32
	HwndActive    windows.Handle
	HwndFocus     windows.Handle
	HwndCapture   windows.Handle
	HwndMenuOwner windows.Handle
	HwndMoveSize  windows.Handle
	HwndCaret     windows.Handle
	RcCaretLeft   int32
	RcCaretTop    int32
	RcCaretRight  int32
	RcCaretBottom int32
}

// keyboardInput mirrors KEYBDINPUT. inputT must be sizeof(INPUT) == 40 on x64:
// 4 bytes type + 4 padding + the 32-byte union (MOUSEINPUT is the largest
// member), otherwise SendInput rejects the call with ERROR_INVALID_PARAMETER.
type keyboardInput struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type inputT struct {
	Type uint32
	_    uint32
	Ki   keyboardInput
	_    [8]byte
}

// ForegroundWindowHandle returns the window that currently has focus.
func ForegroundWindowHandle() uintptr {
	h, _, _ := procGetForegroundWindow.Call()
	return h
}

// CaretAnchor returns the best screen position for the picker: the bottom-left
// of the text caret of the focused control, or the mouse cursor when the
// control has no caret (Explorer, some games, …).
func CaretAnchor() (int, int) {
	if x, y, ok := caretScreenPos(); ok {
		return x, y
	}
	var p pointT
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r != 0 {
		return int(p.X), int(p.Y)
	}
	return 200, 200
}

func caretScreenPos() (int, int, bool) {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return 0, 0, false
	}
	var pid uint32
	tid, _, _ := procGetWindowThreadProcessID.Call(fg, uintptr(unsafe.Pointer(&pid)))
	if tid == 0 {
		return 0, 0, false
	}
	var info guiThreadInfo
	info.CbSize = uint32(unsafe.Sizeof(info))
	if r, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, 0, false
	}
	if info.HwndCaret == 0 || info.RcCaretBottom == 0 {
		return 0, 0, false
	}
	pt := pointT{X: info.RcCaretLeft, Y: info.RcCaretBottom}
	if r, _, _ := procClientToScreen.Call(uintptr(info.HwndCaret), uintptr(unsafe.Pointer(&pt))); r == 0 {
		return 0, 0, false
	}
	return int(pt.X), int(pt.Y), true
}

// ClampToWorkArea keeps a popup of the given size next to the anchor, flipping
// above the anchor when it would overflow the bottom of the monitor.
func ClampToWorkArea(anchorX, anchorY, width, height int) (int, int) {
	pt := pointT{X: int32(anchorX), Y: int32(anchorY)}
	// MonitorFromPoint takes the POINT by value, i.e. as one 64-bit register.
	hmon, _, _ := procMonitorFromPoint.Call(packPoint(pt), monitorDefaultToNearest)
	if hmon == 0 {
		return anchorX, anchorY + 6
	}
	var mi monitorInfoT
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return anchorX, anchorY + 6
	}
	work := mi.RcWork

	x := anchorX
	y := anchorY + 6
	if x+width > int(work.Right) {
		x = int(work.Right) - width - 8
	}
	if x < int(work.Left) {
		x = int(work.Left) + 8
	}
	if y+height > int(work.Bottom) {
		// Flip above the caret.
		y = anchorY - height - 10
		if y < int(work.Top) {
			y = int(work.Top) + 8
		}
	}
	return x, y
}

// packPoint packs a POINT into the 64-bit value MonitorFromPoint expects when
// the coordinates are passed by value (x in the low dword, y in the high dword).
func packPoint(p pointT) uintptr {
	return uintptr(uint32(p.X)) | uintptr(uint32(p.Y))<<32
}

// ActivateWindow brings hwnd to the foreground.
//
// Windows only lets a thread call SetForegroundWindow when that thread is (or is
// attached to) the current foreground thread, so we briefly attach our input
// queue to the foreground window's thread. Without this a background app can
// show a window that never receives a single keystroke.
func ActivateWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	// Only restore a *minimized* window. Calling SW_RESTORE unconditionally
	// un-maximizes a maximized window, which is exactly the "粘贴后窗口退出最大
	// 化" bug: the picker stole focus, we "restored" the target back to normal.
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}

	fg, _, _ := procGetForegroundWindow.Call()
	if fg == hwnd {
		return
	}

	curTid, _, _ := procGetCurrentThreadID.Call()
	var pid uint32
	fgTid, _, _ := procGetWindowThreadProcessID.Call(fg, uintptr(unsafe.Pointer(&pid)))

	attached := false
	if fgTid != 0 && fgTid != curTid {
		if r, _, _ := procAttachThreadInput.Call(curTid, fgTid, 1); r != 0 {
			attached = true
		}
	}
	if !attached {
		// Last resort: a lone Alt press tells Windows "the user is interacting
		// with this process", which unlocks the foreground request.
		keybdTap(vkMenu)
	}

	procBringWindowToTop.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
	procSetFocus.Call(hwnd)

	if attached {
		procAttachThreadInput.Call(curTid, fgTid, 0)
	}
}

// SendPaste synthesises Ctrl+V into whatever window currently has focus.
func SendPaste() error {
	inputs := []inputT{
		{Type: inputKeyboard, Ki: keyboardInput{WVk: vkControl}},
		{Type: inputKeyboard, Ki: keyboardInput{WVk: vkV}},
		{Type: inputKeyboard, Ki: keyboardInput{WVk: vkV, DwFlags: keyeventfKeyUp}},
		{Type: inputKeyboard, Ki: keyboardInput{WVk: vkControl, DwFlags: keyeventfKeyUp}},
	}
	n, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		uintptr(unsafe.Sizeof(inputT{})),
	)
	if int(n) != len(inputs) {
		return fmt.Errorf("SendInput 只送达 %d/%d 个按键事件: %v", int(n), len(inputs), err)
	}
	return nil
}

func keybdTap(vk uint16) {
	down := inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vk}}
	up := inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vk, DwFlags: keyeventfKeyUp}}
	var arr [2]inputT
	arr[0], arr[1] = down, up
	procSendInput.Call(2, uintptr(unsafe.Pointer(&arr[0])), uintptr(unsafe.Sizeof(inputT{})))
}

// PasteInto focuses hwnd and pastes after a short settle delay. The delay
// matters: some apps (Electron, Office) need a moment after activation before
// they process the synthetic Ctrl+V.
func PasteInto(hwnd uintptr) error {
	if hwnd == 0 {
		return errors.New("没有可粘贴的目标窗口")
	}
	time.Sleep(80 * time.Millisecond)
	ActivateWindow(hwnd)
	time.Sleep(90 * time.Millisecond)
	return SendPaste()
}

// sendOneInput injects a single struct via SendInput (used by the expander).
func sendOneInput(in inputT) {
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

// sendLeftArrows walks the caret left by n characters (used by {{cursor}}).
func sendLeftArrows(n int) {
	if n <= 0 {
		return
	}
	if n > 200 {
		n = 200
	}
	for i := 0; i < n; i++ {
		keybdTap(vkLeft)
		time.Sleep(6 * time.Millisecond)
	}
}

// InjectUnicodeText types text as Unicode keystrokes (KEYEVENTF_UNICODE) instead
// of going through the clipboard. No clipboard round-trip means no flicker, no
// "paste" flash in the target app, and the user's clipboard stays untouched.
func InjectUnicodeText(text string) error {
	if text == "" {
		return nil
	}
	const chunk = 64
	units, _ := windows.UTF16FromString(text) // trailing NUL dropped below
	if len(units) > 0 {
		units = units[:len(units)-1]
	}
	for start := 0; start < len(units); start += chunk {
		end := start + chunk
		if end > len(units) {
			end = len(units)
		}
		batch := make([]inputT, 0, (end-start)*2)
		for _, u := range units[start:end] {
			batch = append(batch,
				inputT{Type: inputKeyboard, Ki: keyboardInput{WScan: u, DwFlags: keyeventfUnicode}},
				inputT{Type: inputKeyboard, Ki: keyboardInput{WScan: u, DwFlags: keyeventfUnicode | keyeventfKeyUp}},
			)
		}
		if ret, _, err := procSendInput.Call(uintptr(len(batch)), uintptr(unsafe.Pointer(&batch[0])),
			unsafe.Sizeof(batch[0])); ret != uintptr(len(batch)) {
			return fmt.Errorf("注入文本失败: %d/%d %v", ret, len(batch), err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// InjectBackspaces sends n backspaces in a single SendInput batch. One batch is
// atomic from the target app's point of view, which is what makes the erase
// before an expansion feel instant instead of "backspace, backspace, …".
func InjectBackspaces(n int) {
	if n <= 0 {
		return
	}
	if n > 200 {
		n = 200
	}
	batch := make([]inputT, 0, n*2)
	for i := 0; i < n; i++ {
		batch = append(batch,
			inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vkBack}},
			inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vkBack, DwFlags: keyeventfKeyUp}},
		)
	}
	procSendInput.Call(uintptr(len(batch)), uintptr(unsafe.Pointer(&batch[0])), unsafe.Sizeof(batch[0]))
}

// ---- 原生窗口外观（跟随主题） -------------------------------------------

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36
)

// SetWindowChrome makes the native parts of a frameless window match the theme:
// the 1px system border, the (invisible) caption area and the shadow. Without
// this the window keeps the system's light/dark chrome and looks "off" next to
// our own colors.
func SetWindowChrome(hwnd uintptr, dark bool, borderHex string) {
	if hwnd == 0 {
		return
	}
	procDwmSetWindowAttribute := dwmapi.NewProc("DwmSetWindowAttribute")

	useDark := int32(0)
	if dark {
		useDark = 1
	}
	procDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&useDark)), unsafe.Sizeof(useDark))

	if rgb, ok := parseHexColor(borderHex); ok {
		procDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaBorderColor),
			uintptr(unsafe.Pointer(&rgb)), unsafe.Sizeof(rgb))
		procDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaCaptionColor),
			uintptr(unsafe.Pointer(&rgb)), unsafe.Sizeof(rgb))
	}
}

// parseHexColor turns "#rrggbb" into the COLORREF value DWM expects (0x00BBGGRR).
func parseHexColor(hex string) (uint32, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, false
	}
	var r, g, b uint32
	if _, err := fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b); err != nil {
		return 0, false
	}
	return uint32(b)<<16 | uint32(g)<<8 | uint32(r), true
}
