//go:build windows

// clipdump is a developer utility: it prints every format currently on the
// Windows clipboard so clipboard bugs can be diagnosed without guesswork.
// Usage: go run ./scratch/clipdump
package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procEnumClipboardFormats = user32.NewProc("EnumClipboardFormats")
	procGetClipboardFormatNameW = user32.NewProc("GetClipboardFormatNameW")
	procGetClipboardData    = user32.NewProc("GetClipboardData")
	procGlobalSize          = kernel32.NewProc("GlobalSize")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	procDragQueryFileW      = shell32.NewProc("DragQueryFileW")
)

func main() {
	r, _, err := procOpenClipboard.Call(0)
	if r == 0 {
		fmt.Println("OpenClipboard failed:", err)
		return
	}
	defer procCloseClipboard.Call()

	var format uint32
	for {
		next, _, _ := procEnumClipboardFormats.Call(uintptr(format))
		if next == 0 {
			break
		}
		format = uint32(next)
		name := formatName(format)
		size, _, _ := procGetClipboardData.Call(uintptr(format))
		var gsize uintptr
		// Only ask for the size of real HGLOBAL payloads; OLE objects and
		// metafiles are handles of a different kind and GlobalSize on them
		// corrupts the heap.
		if size != 0 && format < 0xC000 && format != 2 && format != 3 && format != 14 {
			gsize, _, _ = procGlobalSize.Call(size)
		}
		fmt.Printf("format %d (%s) handle=%#x size=%d\n", format, name, size, gsize)

		if format == 15 { // CF_HDROP
			count, _, _ := procDragQueryFileW.Call(size, 0xFFFFFFFF, 0, 0)
			fmt.Println("  hdrop count:", count)
			for i := uintptr(0); i < count; i++ {
				buf := make([]uint16, 1024)
				n, _, _ := procDragQueryFileW.Call(size, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
				fmt.Printf("  file %d: %q (n=%d)\n", i, windows.UTF16ToString(buf[:n]), n)
			}
		}
	}
}

func formatName(id uint32) string {
	if id < 0xC000 {
		switch id {
		case 1:
			return "CF_TEXT"
		case 2:
			return "CF_BITMAP"
		case 8:
			return "CF_DIB"
		case 13:
			return "CF_UNICODETEXT"
		case 15:
			return "CF_HDROP"
		case 17:
			return "CF_DIBV5"
		}
		return "builtin"
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetClipboardFormatNameW.Call(uintptr(id), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}
