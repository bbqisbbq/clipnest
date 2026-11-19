//go:build windows

package clip

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Some applications (WinForms, some archivers and mail clients) publish the
// clipboard through OLE with delayed rendering: EnumClipboardFormats then shows
// only a "DataObject" entry and GetClipboardData finds nothing. This file
// queries that IDataObject directly so those copies are captured too.

var (
	ole32 = windows.NewLazySystemDLL("ole32.dll")

	procOleGetClipboard  = ole32.NewProc("OleGetClipboard")
	procReleaseStgMedium = ole32.NewProc("ReleaseStgMedium")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
)

const (
	coinitApartmentThreaded = 0x2
	dvAspectContent         = 1
	tymedHGlobal            = 1
)

// formatEtc mirrors FORMATETC.
type formatEtc struct {
	CfFormat uint16
	_        uint16
	Ptd      uintptr
	DwAspect uint32
	LIndex   int32
	Tymed    uint32
	_        uint32
}

// stgMedium mirrors STGMEDIUM (64-bit layout).
type stgMedium struct {
	Tymed uint32
	_     uint32
	Data  uintptr
	PUnk  uintptr
}

func comCall(method uintptr, args ...uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(method, args...)
	return hr
}

// vtableMethod 取 obj 这张 COM 接口的虚表里第 index 个函数地址。
//
// obj 用 unsafe.Pointer 而不是 uintptr 存：COM 指针全程保持"指针"身份，
// uintptr 只在 syscall 边界瞬间出现。这样 vet 的 unsafeptr 检查（它对
// uintptr → unsafe.Pointer 一律报警）自然消失，也不会有"指针被当成整数
// 丢进寄存器"的坏味道。偏移用 unsafe.Add 算，指针算术由 go vet 认可。
func vtableMethod(obj unsafe.Pointer, index int) uintptr {
	vtbl := *(*unsafe.Pointer)(obj) // 对象头第一个字段 = 指向虚表的指针
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

// oleGetData asks the clipboard's IDataObject for one clipboard format and
// copies the resulting HGLOBAL payload into Go memory.
func oleGetData(cfFormat uint16) ([]byte, error) {
	fn := func() ([]byte, error) {
		var dataObj unsafe.Pointer
		hr := comCall(procOleGetClipboard.Addr(), uintptr(unsafe.Pointer(&dataObj)))
		if hr != 0 || dataObj == nil {
			return nil, fmt.Errorf("OleGetClipboard failed: %#x", hr)
		}
		defer comCall(vtableMethod(dataObj, 2), uintptr(dataObj)) // IUnknown::Release

		fe := formatEtc{
			CfFormat: cfFormat,
			DwAspect: dvAspectContent,
			LIndex:   -1,
			Tymed:    tymedHGlobal,
		}
		var medium stgMedium
		hr = comCall(vtableMethod(dataObj, 3), uintptr(dataObj),
			uintptr(unsafe.Pointer(&fe)),
			uintptr(unsafe.Pointer(&medium)))
		if hr != 0 || medium.Data == 0 {
			return nil, fmt.Errorf("IDataObject::GetData(0x%04x) failed: %#x", cfFormat, hr)
		}
		defer procReleaseStgMedium.Call(uintptr(unsafe.Pointer(&medium)))

		return globalBytes(medium.Data)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr := comCall(procCoInitializeEx.Addr(), 0, coinitApartmentThreaded); hr == 0 || hr == 1 {
		// 1 = S_FALSE: this thread was already initialised by us.
		defer procCoUninitialize.Call()
	}
	return fn()
}

// oleReadHDrop renders CF_HDROP from an OLE clipboard.
func oleReadHDrop() ([]FileRef, bool) {
	data, err := oleGetData(cfHDrop)
	if err != nil || len(data) < cfDropFilesSize {
		return nil, false
	}
	// The payload starts with a DROPFILES header; walk the wide string list.
	pFiles := binaryLE32(data[0:4])
	if int(pFiles) > len(data) {
		return nil, false
	}
	wide := binaryLE32(data[16:20]) != 0
	out := make([]FileRef, 0, 4)
	if wide {
		var cur []uint16
		for i := int(pFiles); i+1 < len(data); i += 2 {
			v := uint16(data[i]) | uint16(data[i+1])<<8
			if v == 0 {
				if len(cur) == 0 {
					break // double null terminates the list
				}
				out = append(out, statFile(FileRef{Path: windows.UTF16ToString(cur)}))
				cur = cur[:0]
				continue
			}
			cur = append(cur, v)
		}
	} else {
		start := int(pFiles)
		for {
			end := start
			for end < len(data) && data[end] != 0 {
				end++
			}
			if end == start {
				break
			}
			out = append(out, statFile(FileRef{Path: decodeANSI(data[start:end])}))
			start = end + 1
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	for i := range out {
		out[i].Name = filepath.Base(out[i].Path)
	}
	return out, true
}

// oleReadText renders CF_UNICODETEXT from an OLE clipboard.
func oleReadText() (string, bool) {
	data, err := oleGetData(cfUnicodeText)
	if err != nil || len(data) < 2 {
		return "", false
	}
	s := decodeUTF16(data)
	return s, s != ""
}

func binaryLE32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

var errOLEUnavailable = errors.New("OLE clipboard unavailable")
