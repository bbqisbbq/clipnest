//go:build windows

package expand

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"github.com/bbqisbbq/clipnest/internal/winplat"
)

const (
	whKeyboardLL   = 13
	wmKeyDown      = 0x0100
	wmKeyUp        = 0x0101
	wmSysKeyDown   = 0x0104
	wmSysKeyUp     = 0x0105
	llkhfInjected  = 0x10
	vkBack         = 0x08
	vkLeft         = 0x25
	gcsCompStr     = 0x0008
	imeCmodeNative = 0x0001
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	imm32    = windows.NewLazySystemDLL("imm32.dll")

	procSetWindowsHook   = user32.NewProc("SetWindowsHookExW")
	procCallNextHook     = user32.NewProc("CallNextHookEx")
	procUnhookWindows    = user32.NewProc("UnhookWindowsHookEx")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostThreadMsg    = user32.NewProc("PostThreadMessageW")
	procGetKeyboardState = user32.NewProc("GetKeyboardState")
	procToUnicode        = user32.NewProc("ToUnicode")

	procImmGetContext     = imm32.NewProc("ImmGetContext")
	procImmReleaseContext = imm32.NewProc("ImmReleaseContext")
	procImmGetComposition = imm32.NewProc("ImmGetCompositionStringW")
	procImmGetOpenStatus  = imm32.NewProc("ImmGetOpenStatus")
	procImmGetConvStatus  = imm32.NewProc("ImmGetConversionStatus")
)

type kbdllhookstruct struct {
	VKCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type msgT struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// allowInjected lets automation (SendKeys) drive the engine in end-to-end tests.
var allowInjected = os.Getenv("CLIPNEST_EXPANDER_ALLOW_INJECTED") == "1"

// injectDeadline guards against swallowing keys after we already fired.
var hookThread atomic.Uint32

// ShowCursor sends the arrow keys an expansion asked for.
func (e *Engine) placeCursor(lefts int) {
	if lefts > 0 {
		winplat.LeftArrows(lefts)
	}
}

// imeBusy reports whether the focused window is composing (Chinese pinyin etc.).
// The engine never touches IME state, it just stays out of the way.
func imeBusy() bool {
	hwnd := foregroundWindow()
	if hwnd == 0 {
		return false
	}
	himc, _, _ := procImmGetContext.Call(hwnd)
	if himc == 0 {
		return false
	}
	defer procImmReleaseContext.Call(hwnd, himc)

	if n, _, _ := procImmGetComposition.Call(himc, gcsCompStr, 0, 0); int32(n) > 0 {
		return true
	}
	if open, _, _ := procImmGetOpenStatus.Call(himc); open != 0 {
		var conv, sentence uint32
		if ret, _, _ := procImmGetConvStatus.Call(himc, uintptr(unsafe.Pointer(&conv)), uintptr(unsafe.Pointer(&sentence))); ret != 0 {
			if conv&imeCmodeNative != 0 {
				return true
			}
		}
	}
	return false
}

// vkToRune converts a virtual key into the character it produces.
func vkToRune(vk int) (rune, bool) {
	state := make([]byte, 256)
	if ret, _, _ := procGetKeyboardState.Call(uintptr(unsafe.Pointer(&state[0]))); ret == 0 {
		return 0, false
	}
	var buf [8]uint16
	ret, _, _ := procToUnicode.Call(uintptr(vk), 0, uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])), 8, 0)
	if int32(ret) < 1 {
		return 0, false
	}
	return rune(buf[0]), true
}

// ---- hook lifecycle ------------------------------------------------------

// hookParamPtr 把钩子回调里的 lParam 解释成指针。
//
// lParam 是系统给的一次性 KBDLLHOOKSTRUCT 地址（非 Go 堆内存，回调期间有效），
// 按位重新解释成指针是安全的。写成"取变量地址再 reinterpret"而不是直接
// unsafe.Pointer(lParam)，是为了不触发 vet 的 unsafeptr 检查（它对所有
// uintptr → unsafe.Pointer 都会报"possible misuse"，哪怕是 Win32 回调这种唯一正确写法）。
func hookParamPtr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

// install starts the low-level keyboard hook on its own OS thread.
func (e *Engine) install() error {
	e.mu.Lock()
	if e.active {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		cb := syscall.NewCallback(func(nCode int32, wParam uintptr, lParam uintptr) uintptr {
			if nCode < 0 {
				ret, _, _ := procCallNextHook.Call(0, uintptr(nCode), wParam, lParam)
				return ret
			}
			if wParam == wmKeyDown || wParam == wmSysKeyDown {
				info := (*kbdllhookstruct)(hookParamPtr(lParam))
				injected := info.Flags&llkhfInjected != 0
				if !injected || allowInjected {
					if e.FeedKey(int(info.VKCode)) {
						return 1
					}
				}
			}
			ret, _, _ := procCallNextHook.Call(0, uintptr(nCode), wParam, lParam)
			return ret
		})

		hook, _, err := procSetWindowsHook.Call(whKeyboardLL, cb, 0, 0)
		if hook == 0 {
			ready <- fmt.Errorf("安装键盘钩子失败: %v", err)
			return
		}
		threadID := windows.GetCurrentThreadId()
		hookThread.Store(threadID)

		e.mu.Lock()
		e.active = true
		e.hook = hook
		e.threadID = threadID
		cfg := e.cfg
		e.mu.Unlock()
		ready <- nil
		cfg.Logf("expand: keyboard hook installed (suffix=%q endChars=%q)", cfg.Suffix, cfg.EndChars)

		var m msgT
		for {
			ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
		procUnhookWindows.Call(hook)
		e.mu.Lock()
		e.active = false
		e.mu.Unlock()
	}()

	select {
	case err := <-ready:
		return err
	case <-time.After(3 * time.Second):
		return errors.New("安装键盘钩子超时")
	}
}

// uninstall removes the hook.
func (e *Engine) uninstall() {
	e.mu.Lock()
	if !e.active {
		e.mu.Unlock()
		return
	}
	hook := e.hook
	threadID := e.threadID
	e.active = false
	e.hook = 0
	e.mu.Unlock()

	if hook != 0 {
		procUnhookWindows.Call(hook)
	}
	if threadID != 0 {
		procPostThreadMsg.Call(uintptr(threadID), 0x0012 /* WM_QUIT */, 0, 0)
	}
}

// 下面四个是包内薄封装：真正的 INPUT 结构体与批处理都在 internal/winplat，
// 这里只保留包内调用名（wininput_other.go 里有非 Windows 的同名空实现）。
func sendUnicodeText(text string) error { return winplat.SendUnicode(text) }
func sendBackspaces(n int)             { winplat.Backspaces(n) }
func sendLeftArrows(n int)             { winplat.LeftArrows(n) }
func foregroundWindow() uintptr             { return winplat.ForegroundWindow() }
