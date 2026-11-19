//go:build windows

package clip

import "testing"

// 「复制回剪贴板」的抑制 key 必须等于采集侧会算出来的那个哈希，
// 否则手写备忘一复制就会被当成新内容再采集一条（来源显示 clipnest）。
func TestEntryEchoHashMatchesCaptureHash(t *testing.T) {
	text := "手写的一条 #备忘\n第二行带点符号 -> 和 ==\r\n"
	if got, want := entryEchoHash(Entry{Content: text, Kind: KindText}), buildTextCapture(text).Hash; got != want {
		t.Errorf("文本 echo hash 不一致: %q != %q", got, want)
	}
	// 链接是同一套 text 口径（buildTextCapture 只是把 kind 改掉）
	link := "https://example.com/a?b=1"
	if got, want := entryEchoHash(Entry{Content: link, Kind: KindLink}), buildTextCapture(link).Hash; got != want {
		t.Errorf("链接 echo hash 不一致: %q != %q", got, want)
	}

	// 富文本：RTF 与 HTML 各一条路子
	plain, rtf := "粗体", `{\rtf1\ansi 粗体}`
	if got, want := entryEchoHash(Entry{Kind: KindRichText, Content: plain, RTF: rtf}), buildRTFCapture(plain, rtf).Hash; got != want {
		t.Errorf("RTF echo hash 不一致: %q != %q", got, want)
	}
	html := "<b>粗体</b>"
	if got, want := entryEchoHash(Entry{Kind: KindRichText, Content: plain, HTML: html}), buildHTMLCapture(plain, html).Hash; got != want {
		t.Errorf("HTML echo hash 不一致: %q != %q", got, want)
	}

	// 文件引用：按路径序列算
	files := []FileRef{{Path: `C:\a.txt`}, {Path: `C:\b.txt`}}
	if got, want := entryEchoHash(Entry{Kind: KindFiles, Files: files}), buildFilesCapture(files).Hash; got != want {
		t.Errorf("文件 echo hash 不一致: %q != %q", got, want)
	}

	// 自定义格式没有可重算的口径 → 空串（由 SaveCapture 的内容去重兜底）
	if got := entryEchoHash(Entry{Kind: KindCustom, FormatName: "Foo"}); got != "" {
		t.Errorf("自定义格式不该算出 echo hash: %q", got)
	}
}

// 图片：写回与采集用同一个 PNG 编码器，所以可以直接用条目的 hash 抑制回声。
func TestEntryEchoHashImageUsesEntryHash(t *testing.T) {
	e := Entry{Kind: KindImage, Content: "![](token)", Hash: "0123456789abcdef"}
	if got := entryEchoHash(e); got != e.Hash {
		t.Errorf("图片 echo hash = %q, want %q", got, e.Hash)
	}
}
