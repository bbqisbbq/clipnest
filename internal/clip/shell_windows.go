//go:build windows

package clip

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procShellExecuteW = shell32.NewProc("ShellExecuteW")

const swShowNormal = 1

// OpenExternal opens a URL in the default browser (exported for main.go's hooks).
func OpenExternal(target string) error { return shellOpen(target) }

// shellOpen opens a path or URL with its default handler.
func shellOpen(target string) error {
	op, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	r, _, serr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		swShowNormal,
	)
	if r <= 32 {
		return fmt.Errorf("无法打开 %q: %v", target, serr)
	}
	return nil
}

// revealInExplorer highlights a file in Explorer.
func revealInExplorer(path string) error {
	args, err := windows.UTF16PtrFromString("/select,\"" + path + "\"")
	if err != nil {
		return err
	}
	exe, _ := windows.UTF16PtrFromString("explorer.exe")
	r, _, serr := procShellExecuteW.Call(
		0,
		0,
		uintptr(unsafe.Pointer(exe)),
		uintptr(unsafe.Pointer(args)),
		0,
		swShowNormal,
	)
	if r <= 32 {
		return fmt.Errorf("无法定位 %q: %v", path, serr)
	}
	return nil
}

// clipboardOwnerApp reports the executable that currently owns the clipboard.
func clipboardOwnerApp() string {
	hwnd, _, _ := procGetClipboardOwner.Call()
	if hwnd == 0 {
		return ""
	}
	return processNameForWindow(hwnd)
}

var procGetClipboardOwner = user32.NewProc("GetClipboardOwner")

func processNameForWindow(hwnd uintptr) string {
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
	return windows.UTF16ToString(buf[:size])
}
