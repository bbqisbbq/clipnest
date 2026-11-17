//go:build windows

package clip

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Clipboard formats ClipNest understands.
const (
	cfText          = 1
	cfDIB           = 8
	cfUnicodeText   = 13
	cfHDrop         = 15
	cfDIBV5         = 17
	cfBitmap        = 2
	cfDropFilesSize = 20
)

const (
	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmClipboardUpdate = 0x031D

	gmemMoveable = 0x0002

	processQueryLimitedInformation = 0x1000

	errorAlreadyExists = 183
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procEnumClipboardFormats       = user32.NewProc("EnumClipboardFormats")
	procGetClipboardFormatNameW    = user32.NewProc("GetClipboardFormatNameW")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procAddClipboardFormatListener = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardListener    = user32.NewProc("RemoveClipboardFormatListener")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procUnregisterClassW           = user32.NewProc("UnregisterClassW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID   = user32.NewProc("GetWindowThreadProcessId")

	procGlobalAlloc    = kernel32.NewProc("GlobalAlloc")
	procGlobalLock     = kernel32.NewProc("GlobalLock")
	procGlobalUnlock   = kernel32.NewProc("GlobalUnlock")
	procGlobalSize     = kernel32.NewProc("GlobalSize")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procOpenProcess    = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle    = kernel32.NewProc("CloseHandle")

	procDragQueryFileW = shell32.NewProc("DragQueryFileW")
)

// clipboardFormats caches the registered custom format ids.
type clipboardFormats struct {
	html     uint32
	png      uint32
	uriList  uint32
	rtf      uint32
	initOnce bool
}

var formats clipboardFormats

func registerFormat(name string) uint32 {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	id, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(id)
}

func initFormats() {
	if formats.initOnce {
		return
	}
	formats.html = registerFormat("HTML Format")
	formats.png = registerFormat("PNG")
	formats.uriList = registerFormat("text/uri-list")
	formats.rtf = registerFormat("Rich Text Format")
	formats.initOnce = true
}

// openClipboard retries: other applications may hold the clipboard briefly.
func openClipboard() error {
	var lastErr error
	for i := 0; i < 12; i++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		lastErr = err
		time.Sleep(time.Duration(10+i*8) * time.Millisecond)
	}
	return fmt.Errorf("无法打开剪贴板（可能被其它程序占用）: %v", lastErr)
}

func closeClipboard() { procCloseClipboard.Call() }

// globalBytes copies the bytes of a clipboard handle into Go memory.
func globalBytes(handle uintptr) ([]byte, error) {
	if handle == 0 {
		return nil, errors.New("empty clipboard handle")
	}
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, errors.New("clipboard payload is empty")
	}
	ptr, _, _ := procGlobalLock.Call(handle)
	if ptr == 0 {
		return nil, errors.New("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(handle)
	out := make([]byte, int(size))
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(size)))
	return out, nil
}

// readFormat copies a clipboard format out of the open clipboard.
func readFormat(format uint32) ([]byte, error) {
	h, _, err := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return nil, fmt.Errorf("GetClipboardData: %v", err)
	}
	return globalBytes(h)
}

func formatAvailable(format uint32) bool {
	r, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	return r != 0
}

// setClipboardBytes stores data under format in the open clipboard.
func setClipboardBytes(format uint32, data []byte) error {
	if len(data) == 0 {
		return errors.New("refusing to set empty clipboard data")
	}
	h, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: %v", err)
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return errors.New("GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(data)), data)
	procGlobalUnlock.Call(h)

	if r, _, serr := procSetClipboardData.Call(uintptr(format), h); r == 0 {
		return fmt.Errorf("SetClipboardData: %v", serr)
	}
	return nil // the system owns the handle now
}

// utf16Bytes encodes s as UTF-16LE with a terminating null.
func utf16Bytes(s string) []byte {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		u = []uint16{}
	}
	buf := make([]byte, len(u)*2)
	for i, v := range u {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return buf
}

func decodeUTF16(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		v := uint16(b[i]) | uint16(b[i+1])<<8
		if v == 0 {
			break
		}
		u = append(u, v)
	}
	return windows.UTF16ToString(u)
}

// foregroundAppName returns the executable name of the window that had focus
// when the copy happened (best effort; "" when unknown).
func foregroundAppName() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)

	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}

const autoRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autoRunValue = "ClipNest"

// autoStartCommand is what gets written to the Run key. The --background flag
// tells the app it was started by Windows at logon, so it stays in the tray
// instead of popping the window in the user's face.
//
// NOTE: build the quoted command line by hand — fmt's %q would escape the
// backslashes into `C:\\path\\app.exe`, which the Windows loader cannot resolve.
func autoStartCommand(exePath string) string {
	return `"` + exePath + `" --background`
}

// setAutoStart registers/unregisters the app in HKCU Run.
func setAutoStart(enabled bool, exePath string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enabled {
		if err := k.DeleteValue(autoRunValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	if exePath == "" {
		return errors.New("无法确定程序路径")
	}
	return k.SetStringValue(autoRunValue, autoStartCommand(exePath))
}

// autoStartEnabled reports whether the Run entry exists.
func autoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autoRunValue)
	return err == nil
}

// autoStartCommandValue returns the registered command line ("" when absent).
func autoStartCommandValue() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(autoRunValue)
	if err != nil {
		return ""
	}
	return v
}
