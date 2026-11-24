//go:build windows

package clip

import (

	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Watcher owns a message-only Win32 window that receives WM_CLIPBOARDUPDATE,
// which is the only reliable way to notice clipboard changes without polling.
type Watcher struct {
	changes chan struct{}
	hwnd    uintptr
	class   string
	stopMu  sync.Mutex
	stopped bool
	ready   chan error
}

var activeWatcher *Watcher

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type msgT struct {
	Hwnd    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
	_       uint32
}

const (
	hwndMessage  = ^uintptr(2) // (HWND)-3
	wsOverlapped = 0x00000000
)

var wndProcCallback = windows.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmClipboardUpdate:
		if activeWatcher != nil {
			select {
			case activeWatcher.changes <- struct{}{}:
			default: // a capture is already queued
			}
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return ret
})

// StartWatcher creates the listener and returns it. The change channel must be
// drained by the caller.
func StartWatcher() (*Watcher, error) {
	w := &Watcher{
		changes: make(chan struct{}, 4),
		class:   fmt.Sprintf("ClipNestListener%d", windows.GetCurrentProcessId()),
		ready:   make(chan error, 1),
	}
	activeWatcher = w
	go w.loop()
	if err := <-w.ready; err != nil {
		activeWatcher = nil
		return nil, err
	}
	return w, nil
}

// Changes is signalled whenever the clipboard content changes.
func (w *Watcher) Changes() <-chan struct{} { return w.changes }

func (w *Watcher) loop() {
	// A message loop and the window that owns it must live on one OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	clsName, _ := windows.UTF16PtrFromString(w.class)
	wc := wndClassExW{
		Style:         0,
		LpfnWndProc:   wndProcCallback,
		HInstance:     windows.Handle(hInst),
		LpszClassName: clsName,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))

	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		// Re-registering an existing class is fine (another instance already
		// claimed the name); anything else is fatal.
		if lastErr := windows.GetLastError(); lastErr != windows.ERROR_CLASS_ALREADY_EXISTS {
			w.ready <- fmt.Errorf("RegisterClassEx 失败: %v", err)
			return
		}
	}

	title, _ := windows.UTF16PtrFromString("ClipNest Clipboard Listener")
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(title)),
		wsOverlapped,
		0, 0, 0, 0,
		hwndMessage,
		0,
		hInst,
		0,
	)
	if hwnd == 0 {
		w.ready <- fmt.Errorf("CreateWindowEx 失败: %v", err)
		return
	}
	w.hwnd = hwnd

	if r, _, lerr := procAddClipboardFormatListener.Call(hwnd); r == 0 {
		w.ready <- fmt.Errorf("AddClipboardFormatListener 失败: %v", lerr)
		procDestroyWindow.Call(hwnd)
		return
	}

	w.ready <- nil

	var msg msgT
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 { // 0 = WM_QUIT, -1 = error
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	procRemoveClipboardListener.Call(hwnd)
	procUnregisterClassW.Call(uintptr(unsafe.Pointer(clsName)), hInst)
}

// Stop tears the listener down.
func (w *Watcher) Stop() {
	w.stopMu.Lock()
	defer w.stopMu.Unlock()
	if w.stopped {
		return
	}
	w.stopped = true
	if w.hwnd != 0 {
		procPostMessageW.Call(w.hwnd, wmClose, 0, 0)
	}
	if activeWatcher == w {
		activeWatcher = nil
	}
}
