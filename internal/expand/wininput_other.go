//go:build !windows

package expand

// 非 Windows 平台：没有键盘钩子，引擎保持惰性（ClipNest 目前只发布 Windows）。

func sendUnicodeText(string) error { return nil }

func sendBackspaces(int) {}

func sendLeftArrows(int) {}

func foregroundWindow() uintptr { return 0 }

func imeBusy() bool { return false }

func vkToRune(int) (rune, bool) { return 0, false }

func (e *Engine) install() error {
	return errors.New("当前平台不支持缩写自动展开")
}

func (e *Engine) uninstall() {}

func (e *Engine) placeCursor(int) {}