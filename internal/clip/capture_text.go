package clip

// 采集侧的文本知识：哈希口径与预览生成。**故意放在中立文件里**——
// 这些是纯字符串运算，任何平台都要用（网页图片抓取、同步、前端预览都会走到），
// 以前它们住在 clipboard_*_windows.go 里，于是非 Windows 编译时整个 clip 包炸掉。
//
// 三个 hashXxx 是采集侧的唯一哈希口径：Captured.Hash 用它们算，写回剪贴板时
// （entryEchoHash）也用它们算，这样"自己刚写出去的内容"才能被认出来并丢掉。
// 千万不要在这里改用 contentHash()：entry 的 hash 列是另一套口径（同步配对用），
// 两者混用会让手写备忘复制回去时"抑制"失效、凭空多出一条采集条目。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func textHash(text string) string {
	sum := sha256.Sum256([]byte("text\x00" + text))
	return hex.EncodeToString(sum[:16])
}

func htmlHash(plain, fragment string) string {
	sum := sha256.Sum256([]byte("rich\x00" + plain + "\x00" + fragment))
	return hex.EncodeToString(sum[:16])
}

func rtfHash(plain, rtf string) string {
	sum := sha256.Sum256([]byte("rtf\x00" + plain + "\x00" + rtf))
	return hex.EncodeToString(sum[:16])
}

func filesHash(paths []string) string {
	sum := sha256.Sum256([]byte("files\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// makePreview 把正文压成单行摘要：折叠、去多余空格、超长截断加省略号。
func makePreview(s string, limit int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return string(r)
}

// humanBytes 是设置页/同步面板共用的体积文案（"1.5 MB"）。纯格式化，跨平台一样。
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
