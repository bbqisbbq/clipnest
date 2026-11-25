//go:build windows

package clip

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 屏幕取图（GDI BitBlt）与全屏遮罩窗的摆放。
//
// 只做两件底层的事：把"光标所在那块显示器"拍成一张 PNG，以及把 Wails 建的遮罩窗
// 摆到那块显示器上（用 SetWindowPos 走物理像素，绕开 换壳前 Wails 的 DIP 换算——否则
// 缩放 150% 的屏幕上遮罩会比屏幕小一圈，截出来的图就会错位）。
// 不负责：选区交互（前端 Shot.tsx）、条目的落库（service_shot.go）。

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procCreateDCW              = gdi32.NewProc("CreateDCW")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procEnumChildWindows       = user32.NewProc("EnumChildWindows")
	procGetClassNameW          = user32.NewProc("GetClassNameW")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
)

const (
	srcCopy      = 0x00CC0020
	captureBlt   = 0x40000000 // 连分层窗口（浏览器/播放器）一起拍
	dibRGBColors = 0

	hwndTopmost   = ^uintptr(0) // (HWND)-1
	swpShowWindow = 0x0040
	swHide        = 0
)

// bitmapInfoHeader mirrors BITMAPINFOHEADER.
type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// bitmapInfo 只用到 32bpp 那一支，所以调色板数组留一个占位即可。
type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// ShotFrame is one frozen screen grab: the monitor under the cursor, in physical
// pixels, already encoded as PNG (what the overlay shows and what a crop slices).
//
// 类型本体在 shot.go（中立文件），这里只放抓屏实现。

// CaptureCursorMonitor grabs the monitor under the mouse cursor.
//
// 只拍一块显示器（而不是整个虚拟桌面）：多屏时坐标换算会翻倍复杂，而"在哪个屏
// 上按的快捷键就截哪个屏"本来就是人的预期。
func CaptureCursorMonitor() (*ShotFrame, error) {
	rect, err := cursorMonitorRect()
	if err != nil {
		return nil, err
	}
	w := int(rect.Right - rect.Left)
	h := int(rect.Bottom - rect.Top)
	if w <= 0 || h <= 0 {
		return nil, errors.New("显示器尺寸异常")
	}

	screenDC, err := createDisplayDC()
	if err != nil {
		return nil, err
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, errors.New("CreateCompatibleDC 失败")
	}
	defer procDeleteDC.Call(memDC)

	bitmap, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	if bitmap == 0 {
		return nil, errors.New("CreateCompatibleBitmap 失败")
	}
	defer procDeleteObject.Call(bitmap)

	old, _, _ := procSelectObject.Call(memDC, bitmap)
	if r, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h),
		screenDC, uintptr(rect.Left), uintptr(rect.Top), srcCopy|captureBlt); r == 0 {
		procSelectObject.Call(memDC, old)
		return nil, errors.New("BitBlt 失败")
	}

	// 负高度 = 请求 top-down 的像素行，省掉翻转。
	bi := bitmapInfo{Header: bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(w),
		Height:      -int32(h),
		Planes:      1,
		BitCount:    32,
		Compression: 0, // BI_RGB
	}}
	pixels := make([]byte, w*h*4)
	if r, _, _ := procGetDIBits.Call(memDC, bitmap, 0, uintptr(h),
		uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors); r == 0 {
		procSelectObject.Call(memDC, old)
		return nil, errors.New("GetDIBits 失败")
	}
	procSelectObject.Call(memDC, old)

	img := bgraToNRGBA(pixels, w, h)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return &ShotFrame{
		PNG:    buf.Bytes(),
		Left:   int(rect.Left),
		Top:    int(rect.Top),
		Width:  w,
		Height: h,
	}, nil
}

// bgraToNRGBA converts the 32bpp BGRA buffer GetDIBits hands back.
//
// 屏幕上没有透明像素，但有的驱动会在 alpha 里放 0；一律当不透明，
// 否则截出来的图会在看图软件里变成全透明。
func bgraToNRGBA(pix []byte, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := y * w * 4
		dst := y * img.Stride
		for x := 0; x < w; x++ {
			img.Pix[dst+x*4+0] = pix[src+x*4+2]
			img.Pix[dst+x*4+1] = pix[src+x*4+1]
			img.Pix[dst+x*4+2] = pix[src+x*4+0]
			img.Pix[dst+x*4+3] = 0xFF
		}
	}
	return img
}

func createDisplayDC() (uintptr, error) {
	name, err := windows.UTF16PtrFromString("DISPLAY")
	if err != nil {
		return 0, err
	}
	dc, _, _ := procCreateDCW.Call(uintptr(unsafe.Pointer(name)), 0, 0, 0)
	if dc == 0 {
		return 0, errors.New("CreateDC(DISPLAY) 失败")
	}
	return dc, nil
}

// HideOverlayWindow hides the overlay again.
//
// 用 Win32 隐藏（而不是 换壳前 Wails 的 Hide）：窗口本来就是用 SetWindowPos 摆到物理像素
// 上的，Wails 对它的尺寸/位置并不知情，让两边各管一半只会互相打架。
func HideOverlayWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	procShowWindow.Call(hwnd, swHide)
}

// FocusOverlayWindow gives the keyboard to the overlay so Esc actually cancels.
//
// WebView2 的内容住在窗口的一个子窗口里，把焦点还给父窗口（甚至只是 SetForegroundWindow）
// 都不够：键会落到上一个前台程序，用户一按 Esc 可能把自己文档里的字删了。
// 所以这里显式去找 Chromium 的内容子窗口并 SetFocus，找不到就退回窗口本身。
func FocusOverlayWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	var target uintptr
	cb := windows.NewCallback(func(child uintptr, _ uintptr) uintptr {
		name := make([]uint16, 128)
		n, _, _ := procGetClassNameW.Call(child, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
		if n == 0 {
			return 1 // keep enumerating
		}
		class := windows.UTF16ToString(name[:n])
		if strings.HasPrefix(class, "Chrome_WidgetWin") || strings.HasPrefix(class, "Chrome_RenderWidgetHost") {
			if r, _, _ := procIsWindowVisible.Call(child); r != 0 {
				target = child
				return 0 // stop
			}
		}
		return 1
	})
	procEnumChildWindows.Call(hwnd, cb, 0)
	if target != 0 {
		procSetFocus.Call(target)
		return
	}
	procSetFocus.Call(hwnd)
}

// cursorMonitorRect returns the bounds (physical px) of the monitor holding the cursor.
func cursorMonitorRect() (rectT, error) {
	var pt pointT
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return rectT{}, errors.New("GetCursorPos 失败")
	}
	hmon, _, _ := procMonitorFromPoint.Call(packPoint(pt), monitorDefaultToNearest)
	if hmon == 0 {
		return rectT{}, errors.New("MonitorFromPoint 失败")
	}
	var mi monitorInfoT
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rectT{}, fmt.Errorf("GetMonitorInfo 失败")
	}
	return mi.RcMonitor, nil
}

// PlaceOverlayWindow puts the overlay window exactly over a monitor, on top of
// everything, and shows it — all in physical pixels. The caller activates the
// window afterwards (it must receive mouse drags and Esc).
func PlaceOverlayWindow(hwnd uintptr, left, top, width, height int) {
	if hwnd == 0 {
		return
	}
	procSetWindowPos.Call(hwnd, hwndTopmost,
		uintptr(int32(left)), uintptr(int32(top)),
		uintptr(int32(width)), uintptr(int32(height)),
		swpShowWindow)
}

