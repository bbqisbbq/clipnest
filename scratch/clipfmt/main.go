//go:build windows

// clipfmt is a developer utility for clipboard-shape forensics: it prints every
// format currently on the Windows clipboard, plus the readable head of the
// textual ones (CF_UNICODETEXT / HTML Format / RTF / text/uri-list).
// Usage: go run ./scratch/clipfmt
package main

import (
	"bytes"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard           = user32.NewProc("OpenClipboard")
	procCloseClipboard          = user32.NewProc("CloseClipboard")
	procEnumClipboardFormats    = user32.NewProc("EnumClipboardFormats")
	procGetClipboardFormatNameW = user32.NewProc("GetClipboardFormatNameW")
	procGetClipboardData        = user32.NewProc("GetClipboardData")
	procGlobalSize              = kernel32.NewProc("GlobalSize")
	procGlobalLock              = kernel32.NewProc("GlobalLock")
	procGlobalUnlock            = kernel32.NewProc("GlobalUnlock")
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
		h, _, _ := procGetClipboardData.Call(uintptr(format))
		var size uintptr
		if h != 0 && format < 0xC000 && format != 2 && format != 3 && format != 9 && format != 14 {
			size, _, _ = procGlobalSize.Call(h)
		}
		data := globalBytes(h)
		fmt.Printf("format %d (%q) handle=%#x size=%d bytes=%d\n", format, name, h, size, len(data))

		switch {
		case format == 13: // CF_UNICODETEXT
			u := unsafe.Slice((*uint16)(unsafe.Pointer(&mustNonEmpty(data)[0])), len(data)/2)
			fmt.Printf("   unicode: %q\n", head(windows.UTF16ToString(u)))
		case name == "HTML Format":
			fmt.Printf("   html: %s\n", head(string(data)))
		case name == "text/uri-list":
			fmt.Printf("   urilist: %s\n", head(string(data)))
		case name == "Rich Text Format":
			fmt.Printf("   rtf: %s\n", head(string(data)))
		case format == 8 || format == 17: // CF_DIB / CF_DIBV5
			if len(data) >= 40 {
				hdr := uint32(data[0])
				w := int32(uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24)
				hh := int32(uint32(data[8]) | uint32(data[9])<<8 | uint32(data[10])<<16 | uint32(data[11])<<24)
				bpp := uint16(data[14]) | uint16(data[15])<<8
				comp := uint32(data[16]) | uint32(data[17])<<8 | uint32(data[18])<<16 | uint32(data[19])<<24
				fmt.Printf("   dib header=%d %dx%d bpp=%d compression=%d\n", hdr, w, hh, bpp, comp)
			}
		}
	}
}

func mustNonEmpty(b []byte) []byte {
	if len(b) == 0 {
		return make([]byte, 2)
	}
	return b
}

func head(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\\n")
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > 700 {
		return s[:700] + "…"
	}
	return s
}

func globalBytes(h uintptr) []byte {
	if h == 0 {
		return nil
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil
	}
	defer procGlobalUnlock.Call(h)
	n, _, _ := procGlobalSize.Call(h)
	if n == 0 || n > 64<<20 {
		return nil
	}
	return bytes.Clone(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(n)))
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
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
