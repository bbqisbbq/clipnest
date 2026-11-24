//go:build windows

package clip

import (
	"unsafe"
)

// 窗口背后的 DWM 背景层（Acrylic）。
//
// 为什么需要它：快速输入窗是透明窗口（WS_EX_NOREDIRECTIONBITMAP），页面里 alpha=0
// 的像素确实能让桌面透出来 —— 所以"四角真透明"只改 BackgroundType 就够了。但**半透明**
// 的像素（卡片用 color-mix(…, transparent) 铺的那层底）不会被合成到桌面上，而是被
// 合成到一层黑底：实测 alpha=0.7 的卡片量到 rgb(13,14,19)，比实心的 rgb(14,16,21) 还暗。
// 给窗口加一层 DWM backdrop 之后，页面里的半透明像素才与它混合（同一张卡片量到
// rgb(54,55,59)，能看见背后的窗口边缘）。所以「弹窗透明」这项设置在 >0 时要把这层打开，
// 在 0 时关掉（关掉才有无锯齿的圆角切边，也不会为一条看着不出的效果付合成开销）。
//
// 参考实测记录：docs/HISTORY.md「2026-11-20 快速输入窗透明」。

const (
	dwmwaSystemBackdropType        = 38
	dwmSystemBackdropNone    int32 = 1
	dwmSystemBackdropAcrylic int32 = 3 // DWMSBT_TRANSIENTWINDOW

	// Windows 10 1803+ 的退路：SetWindowCompositionAttribute + ACCENT_POLICY。
	wcaAccentPolicy         = 19
	accentEnableAcrylicBlur = 4
)

type accentPolicy struct {
	State         int32
	Flags         int32
	GradientColor uint32
	AnimationID   int32
}

// WINDOWCOMPOSITIONATTRIBDATA { DWORD Attrib; PVOID PvData; SIZE_T CbData; }
type windowCompositionAttribData struct {
	Attrib int32
	_      int32
	PvData unsafe.Pointer
	CbData uintptr
}

// SetWindowBackdrop 打开/关闭窗口背后的 Acrylic 层，返回值表示"现在确实是开着"，
// 让调用方能在老系统上退回不透明卡片（否则半透明会被合到黑底上，卡片反而更脏）。
//

func SetWindowBackdrop(hwnd uintptr, acrylic bool) bool {
	if hwnd == 0 {
		return false
	}
	procDwm := dwmapi.NewProc("DwmSetWindowAttribute")

	if !acrylic {
		mode := dwmSystemBackdropNone
		procDwm.Call(hwnd, uintptr(dwmwaSystemBackdropType),
			uintptr(unsafe.Pointer(&mode)), unsafe.Sizeof(mode))
		// 顺手把 Win10 那条退路也关掉（幂等）。2.9.1 起一直是这么做的。
		policy := accentPolicy{State: 0 /* ACCENT_DISABLED */}
		data := windowCompositionAttribData{
			Attrib: wcaAccentPolicy,
			PvData: unsafe.Pointer(&policy),
			CbData: unsafe.Sizeof(policy),
		}
		user32.NewProc("SetWindowCompositionAttribute").Call(hwnd, uintptr(unsafe.Pointer(&data)))
		return false
	}

	mode := dwmSystemBackdropAcrylic
	if r, _, _ := procDwm.Call(hwnd, uintptr(dwmwaSystemBackdropType),
		uintptr(unsafe.Pointer(&mode)), unsafe.Sizeof(mode)); r == 0 {
		return true
	}

	// Windows 11 22621 之前没有 DWMWA_SYSTEMBACKDROP_TYPE：退到 Win10 的亚克力。
	// GradientColor 是 AABBGGRR，这里给一层近黑的薄雾，让我们的卡片配色说了算。
	policy := accentPolicy{State: accentEnableAcrylicBlur, GradientColor: 0xB4000000}
	data := windowCompositionAttribData{
		Attrib: wcaAccentPolicy,
		PvData: unsafe.Pointer(&policy),
		CbData: unsafe.Sizeof(policy),
	}
	r, _, _ := user32.NewProc("SetWindowCompositionAttribute").Call(hwnd, uintptr(unsafe.Pointer(&data)))
	return r != 0
}
