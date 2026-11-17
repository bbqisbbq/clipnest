//go:build windows

package clip

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- launching -----------------------------------------------------------

// launchProgram starts an executable, honouring quotes and arguments.
// cmd /c start is used so "code ." or a path with spaces both work.
func launchProgram(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return errors.New("没有可启动的目标")
	}
	cmd := exec.Command("cmd", "/c", "start", "", spec)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// runCommand executes a shell command and returns trimmed combined output.
func runCommand(line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("命令为空")
	}
	cmd := exec.Command("cmd", "/c", line)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return "", err
	}
	return text, nil
}

// ---- inline trigger expansion -------------------------------------------

const (
	whKeyboardLL = 13
	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105
	llkhfInjected = 0x10
	vkBack       = 0x08
)

var (
	procSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")

	procGetKeyboardState    = user32.NewProc("GetKeyboardState")
	procToUnicode           = user32.NewProc("ToUnicode")
)

type kbdllhookstruct struct {
	VKCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}


// expander watches keystrokes for "<trigger><suffix>" and replaces the text in
// place. It is only active while ExpandEnabled is on.
type expander struct {
	buf       []rune
	suffix    []rune
	hook      uintptr
	threadID  uint32
	injecting atomic.Bool
	stop      chan struct{}
}

var (
	activeExpander atomic.Pointer[expander]
	// ourForeground reports whether one of our own windows has focus; the expander
	// stays out of the way inside ClipNest itself.
	ourForeground atomic.Pointer[func() bool]
)

// SetExpanderForegroundGuard installs a callback used to skip expansion while
// ClipNest's own windows are focused.
func SetExpanderForegroundGuard(fn func() bool) {
	if fn == nil {
		ourForeground.Store(nil)
		return
	}
	ourForeground.Store(&fn)
}

// StartExpander installs the low-level keyboard hook.
func StartExpander(suffix string) error {
	if suffix == "" {
		suffix = "-"
	}
	StopExpander()

	e := &expander{suffix: []rune(suffix), stop: make(chan struct{})}
	activeExpander.Store(e)

	ready := make(chan error, 1)
	go func() {
		// A low-level hook needs its own thread with a message loop.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		cb := syscall.NewCallback(func(nCode int32, wParam uintptr, lParam uintptr) uintptr {
			if nCode < 0 {
				ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
				return ret
			}
			if wParam == wmKeyDown || wParam == wmSysKeyDown {
				info := (*kbdllhookstruct)(unsafe.Pointer(lParam))
				if info.Flags&llkhfInjected == 0 && e.injecting.Load() == false {
					if e.onKey(int(info.VKCode)) {
						return 1 // swallow the keystroke we just consumed
					}
				}
			}
			ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
			return ret
		})

		hook, _, err := procSetWindowsHookEx.Call(whKeyboardLL, cb, 0, 0)
		if hook == 0 {
			ready <- fmt.Errorf("安装键盘钩子失败: %v", err)
			return
		}
		e.hook = hook
		e.threadID = windows.GetCurrentThreadId()
		ready <- nil

		var m msgT
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		procUnhookWindowsHookEx.Call(e.hook)
	}()

	select {
	case err := <-ready:
		if err != nil {
			activeExpander.Store(nil)
			return err
		}
		Logf(logDir(), "expander: keyboard hook installed")
		return nil
	case <-e.stop:
		return errors.New("expander stopped")
	}
}

// StopExpander removes the hook if one is installed.
func StopExpander() {
	e := activeExpander.Swap(nil)
	if e == nil {
		return
	}
	close(e.stop)
	if e.hook != 0 {
		procUnhookWindowsHookEx.Call(e.hook)
		// Wake the message loop so the thread can exit.
		procPostThreadMessage := user32.NewProc("PostThreadMessageW")
		procPostThreadMessage.Call(uintptr(e.threadID), 0x0012 /* WM_QUIT */, 0, 0)
	}
}

// onKey returns true when the keystroke finished a trigger and must be swallowed.
func (e *expander) onKey(vk int) bool {
	if g := ourForeground.Load(); g != nil && (*g)() {
		e.buf = e.buf[:0]
		return false
	}
	if vk == vkBack {
		if len(e.buf) > 0 {
			e.buf = e.buf[:len(e.buf)-1]
		}
		return false
	}
	r, ok := vkToRune(vk)
	if !ok {
		e.buf = e.buf[:0]
		return false
	}
	e.buf = append(e.buf, r)
	if len(e.buf) > 64 {
		e.buf = append([]rune(nil), e.buf[len(e.buf)-64:]...)
	}
	if len(e.suffix) == 0 || len(e.buf) < len(e.suffix) {
		return false
	}
	if string(e.buf[len(e.buf)-len(e.suffix):]) != string(e.suffix) {
		return false
	}

	candidate := string(e.buf[:len(e.buf)-len(e.suffix)])
	content, hit := lookupTrigger(candidate)
	if !hit {
		return false
	}

	erased := len([]rune(candidate)) + len(e.suffix)
	e.buf = e.buf[:0]
	e.injecting.Store(true)
	go func() {
		defer e.injecting.Store(false)
		expandInto(candidate, content, erased)
	}()
	return true
}

// expandInto deletes the typed trigger and pastes the content in its place.
func expandInto(trigger, content string, erase int) {
	for i := 0; i < erase; i++ {
		injectKey(vkBack, false)
		injectKey(vkBack, true)
	}
	if err := WriteTextToClipboard(content); err != nil {
		Logf(logDir(), "expander: keyboard hook installed")
		return
	}
	_ = SendPaste()
	Logf(logDir(), "expander: %q -> %d bytes", trigger, len(content))
	_ = trigger
}

// vkToRune converts the keys triggers are made of (letters, digits, a few
// punctuation marks) to the character they produce.
func vkToRune(vk int) (rune, bool) {
	state := make([]byte, 256)
	if ret, _, _ := procGetKeyboardState.Call(uintptr(unsafe.Pointer(&state[0]))); ret == 0 {
		return 0, false
	}
	var buf [8]uint16
	ret, _, _ := procToUnicode.Call(uintptr(vk), 0, uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])), 8, 0)
	n := int32(ret)
	if n < 1 {
		return 0, false
	}
	return rune(buf[0]), true
}

// injectKey sends one synthetic key event through the shared SendInput helper.
func injectKey(vk uint16, up bool) {
	if up {
		sendOneInput(inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vk, DwFlags: keyeventfKeyUp}})
		return
	}
	sendOneInput(inputT{Type: inputKeyboard, Ki: keyboardInput{WVk: vk}})
}

// expanderActive reports whether the hook is installed (for the settings page).
func expanderActive() bool { return activeExpander.Load() != nil }
