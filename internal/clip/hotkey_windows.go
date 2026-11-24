//go:build windows

package clip

import (
	"strings"
)

// 热键"撞车"的同步预检。
//
// 为什么要自己探一遍：Wails 的 GlobalShortcut 在 app.Run() 之前注册只是**排队**
// （global_shortcut_manager.go 的 pending），真正的 RegisterHotKey 在启动后 flush，
// 失败只能经由 app 的错误处理器报出来——调用方拿到的永远是 nil。于是"设置页说保存成功、
// 按下去却没有任何反应"这种最难查的故障就出现了。这里在注册前先用 Win32 试一次
// （注册后立刻注销），把"这套组合键已经被别的程序占了"变成调用方可读的错误。

var (
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000
)

// hotkeyProbeID 是预检用的热键 id：只在我们自己的线程里瞬间用一下，取个不会撞的号。
const hotkeyProbeID = 0x4E53

// hotkeyTaken 报告 accel 这套组合是否已被别的程序占用。
//
// 解析不出来的加速键一律当作"没被占用"返回 false：宁可让 Wails 去试、去报它自己的
// 错误，也不要在这里凭猜测拦下一个其实可用的热键。
func hotkeyTaken(accel string) bool {
	mods, vk, ok := parseAccelerator(accel)
	if !ok {
		return false
	}
	r, _, _ := procRegisterHotKey.Call(0, hotkeyProbeID, uintptr(mods|modNoRepeat), uintptr(vk))
	if r == 0 {
		return true
	}
	procUnregisterHotKey.Call(0, hotkeyProbeID)
	return false
}

// parseAccelerator 把 "Ctrl+Shift+A" 这种写法拆成 (modifiers, virtual-key)。
//
// 只认 Wails 加速键里常用的那部分（修饰键 + 字母/数字/F1-F24/少量命名键），
// 够覆盖设置页录出来的全部组合；其余返回 ok=false。
func parseAccelerator(accel string) (mods uint32, vk uint32, ok bool) {
	parts := strings.Split(accel, "+")
	if len(parts) < 2 {
		return 0, 0, false
	}
	for _, raw := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "ctrl", "control", "cmdorctrl", "commandorcontrol":
			mods |= modControl
		case "shift":
			mods |= modShift
		case "alt", "option", "optionoralt":
			mods |= modAlt
		case "super", "win", "meta", "cmd", "command":
			mods |= modWin
		default:
			return 0, 0, false
		}
	}
	key := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	if len(key) == 1 {
		c := key[0]
		switch {
		case c >= 'a' && c <= 'z':
			return mods, uint32(c - 'a' + 'A'), true
		case c >= '0' && c <= '9':
			return mods, uint32(c), true
		}
	}
	if code, ok := namedVirtualKeys[key]; ok {
		return mods, code, true
	}
	return 0, 0, false
}

// namedVirtualKeys 与 Wails 的 winKeyCodes 保持同一套取值（两边对不上就会出现
// "预检说空闲、实际注册失败"的假阴性）。
var namedVirtualKeys = map[string]uint32{
	"backspace": 0x08, "tab": 0x09, "return": 0x0D, "enter": 0x0D,
	"escape": 0x1B, "esc": 0x1B, "space": 0x20,
	"page up": 0x21, "pageup": 0x21, "page down": 0x22, "pagedown": 0x22,
	"end": 0x23, "home": 0x24, "left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
	"delete": 0x2E, "insert": 0x2D, "numlock": 0x90,
	";": 0xBA, "=": 0xBB, ",": 0xBC, "-": 0xBD, ".": 0xBE, "/": 0xBF,
	"`": 0xC0, "[": 0xDB, "\\": 0xDC, "]": 0xDD, "'": 0xDE, "+": 0xBB,
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74, "f6": 0x75,
	"f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79, "f11": 0x7A, "f12": 0x7B,
	"f13": 0x7C, "f14": 0x7D, "f15": 0x7E, "f16": 0x7F, "f17": 0x80, "f18": 0x81,
	"f19": 0x82, "f20": 0x83, "f21": 0x84, "f22": 0x85, "f23": 0x86, "f24": 0x87,
}
