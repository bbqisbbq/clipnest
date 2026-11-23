//go:build windows

package clip

import (
	"strings"
	"testing"
	"time"
)

// 「图文混排能一起粘贴出去」的真机证据：真的往系统剪贴板写一次，再按目标程序的
// 读法把它读回来 —— 断言三件事同时成立：
//
//	CF_HTML 里有内联的 data URI（浏览器/Electron 走这条）
//	RTF 里有 \pict 嵌图（Word/WPS/Outlook 走这条）
//	纯文本是干净的正文（不含 memoimg: 令牌）
//
// 这条用例会短暂改写剪贴板（跑之前先停掉 clipnest.exe，见 requireIdleClipboard），
// 结束时按写之前的样子恢复。
func TestWriteRichClipboardCarriesThreeFormats(t *testing.T) {
	requireIdleClipboard(t)

	saved, _ := captureClipboard()
	defer restoreClipboardAfterTest(t, saved)

	pngData := tinyRGBA(t)
	pngs := map[string][]byte{testHash: pngData}
	html := `<p>图文混排</p><p><img src="memoimg:` + testHash + `"></p>`
	plain := "图文混排"
	fragment := embedMemoImages(html, pngs)
	// 与 writeBodyEntry 一致：RTF 用还带令牌的那一版 HTML（\pict 要按令牌取字节）
	rtf := htmlToRTF(html, pngs)

	if err := WriteRichToClipboard(fragment, rtf, plain); err != nil {
		t.Fatalf("WriteRichToClipboard: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // 等系统把剪贴板更新广播完

	got, err := captureClipboard()
	if err != nil {
		t.Fatalf("读回剪贴板: %v", err)
	}
	if got == nil {
		t.Fatal("剪贴板上什么都没有")
	}
	if got.Kind != KindRichText {
		t.Fatalf("读回来的类型是 %q，want %q（说明 CF_HTML/RTF 没写进去）", got.Kind, KindRichText)
	}
	if got.Text != plain {
		t.Errorf("纯文本 = %q, want %q", got.Text, plain)
	}
	if strings.Contains(got.Text, "memoimg:") {
		t.Errorf("纯文本里漏出了图片令牌: %q", got.Text)
	}
	if !strings.Contains(got.HTML, "data:image/png;base64,") {
		t.Errorf("CF_HTML 里没有内联图片: %q", got.HTML)
	}
	if !strings.Contains(got.HTML, "图文混排") {
		t.Errorf("CF_HTML 里没有正文: %q", got.HTML)
	}

	// RTF 不能靠 captureClipboard 验：它优先取 HTML（见 clipboard_windows.go 的顺序）。
	// 这里直接按格式号读回来 —— 目标程序（Word/WPS）看的就是这份。
	initFormats()
	if formats.rtf == 0 {
		t.Fatal("系统没有 RTF 剪贴板格式")
	}
	raw := readClipboardFormat(t, formats.rtf)
	if !strings.Contains(string(raw), `\pict\pngblip`) {
		t.Errorf("RTF 里没有 \\pict 嵌图（长度 %d）: %q", len(raw), string(raw))
	}
	if !strings.Contains(string(raw), rtfEscape("图文混排")) {
		t.Errorf("RTF 里没有正文: %q", string(raw))
	}
}

// readClipboardFormat 按格式号把剪贴板里的某一份内容读出来（测试用）。
func readClipboardFormat(t *testing.T, id uint32) []byte {
	t.Helper()
	if err := openClipboard(); err != nil {
		t.Fatalf("打开剪贴板: %v", err)
	}
	defer closeClipboard()
	b, err := readFormat(id)
	if err != nil {
		t.Fatalf("读格式 %d: %v", id, err)
	}
	return b
}

// restoreClipboardAfterTest 把剪贴板恢复成跑测试之前的样子。
// 私有格式（自定义）没法原样写回，这种情况下只记一条日志，不做假承诺。
func restoreClipboardAfterTest(t *testing.T, c *Captured) {
	t.Helper()
	if c == nil {
		return
	}
	var err error
	switch c.Kind {
	case KindRichText:
		if c.RTF != "" {
			err = WriteRTFToClipboard(c.RTF, c.Text)
		} else {
			err = WriteHTMLToClipboard(c.HTML, c.Text)
		}
	case KindImage:
		err = WriteImageToClipboard(c.PNG)
	case KindFiles:
		paths := make([]string, 0, len(c.Files))
		for _, f := range c.Files {
			paths = append(paths, f.Path)
		}
		err = WriteFilesToClipboard(paths)
	case KindCustom:
		t.Logf("原剪贴板是自定义格式 %q，无法原样恢复", c.FormatName)
		return
	default:
		err = WriteTextToClipboard(c.Text)
	}
	if err != nil {
		t.Logf("恢复剪贴板失败: %v", err)
	}
}
