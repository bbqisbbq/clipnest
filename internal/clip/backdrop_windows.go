//go:build windows

package clip

import (
	"os"
	"unsafe"
)

// 窗口背后的合成层（"弹窗半透明"用）。
//
// 结论先说（2.12.2 实测）：**WebView2 上做不到"真透明但不磨砂"**。
//
//   - 页面里 alpha=0 的像素能让桌面透出来（窗口是 WS_EX_NOREDIRECTIONBITMAP），
//     所以"四角真圆角"一直是好的；
//   - 但**半透明**像素会被合到一层黑底上，而不是桌面：alpha=0.7 的卡片量到 rgb(13,14,19)，
//     比实心的 rgb(14,16,21) 还暗。要让半透明真的与桌面混合，只有 DWM 那层合成可用，
//     而它自带模糊 ⇒ 看起来就是毛玻璃。
//   - 试过并否掉的两条路（都用像素量过，白底 249 作对照）：
//       ① DwmEnableBlurBehindWindow + 空模糊区域（老牌"只混合不模糊"技巧）：卡片停在 rgb(14,16,21)，不动；
//       ② WS_EX_LAYERED + SetLayeredWindowAttributes：同样不动（WebView2 走自己的合成面，
//          GDI 的分层 alpha 管不到它）。
//
// 所以现在默认**不开任何合成层**：弹窗是一块实心卡片 + 真圆角，没有任何磨砂。
// 想要"毛玻璃"的用户可以设 CLIPNEST_CAPTURE_ACRYLIC=1 回到旧行为（DWMSBT_TRANSIENTWINDOW）。
// 真·半透明要等这个弹窗改写成原生窗口（Gate 3 backlog，见 docs/design/MYGO-GATE3-NATIVE-UI.md）。

const (
	dwmwaSystemBackdropType        = 38
	dwmSystemBackdropNone    int32 = 1
	dwmSystemBackdropAcrylic int32 = 3 // DWMSBT_TRANSIENTWINDOW
)

// SetWindowBackdrop 打开/关闭窗口背后的合成层，返回值表示"现在确实是开着的"。
//
// 返回 false 时前端会把卡片画回不透明（半透明在没有合成层的窗口里会被合到黑底上，
// 反而比实心更脏）——这正是我们要的：没有磨砂，也没有一块脏底。
func SetWindowBackdrop(hwnd uintptr, wanted bool) bool {
	if hwnd == 0 {
		return false
	}
	procDwmSetWindowAttribute := dwmapi.NewProc("DwmSetWindowAttribute")

	acrylic := wanted && os.Getenv("CLIPNEST_CAPTURE_ACRYLIC") == "1"
	mode := dwmSystemBackdropNone
	if acrylic {
		mode = dwmSystemBackdropAcrylic
	}
	if r, _, _ := procDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaSystemBackdropType),
		uintptr(unsafe.Pointer(&mode)), unsafe.Sizeof(mode)); r != 0 {
		return false
	}
	return acrylic
}
