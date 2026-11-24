//go:build windows

package clip

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 剪贴板格式的知识：格式 id ↔ 名字、哪些格式能当自定义数据保存（跳过清单），
// 以及格式载荷与展示用的小工具（RTF/HTML 片段、去标签、预览、URL 判定、字节数格式化）。
// 不负责：枚举/读取剪贴板本身（clipboard_windows.go）、写回（clipboard_write_windows.go）。

// clipFormat is one entry of the clipboard format enumeration.
type clipFormat struct {
	id   uint32
	name string
}

// enumerateFormats lists every format currently on the open clipboard.
func enumerateFormats() []clipFormat {
	out := make([]clipFormat, 0, 16)
	var next uint32
	for {
		id, _, _ := procEnumClipboardFormats.Call(uintptr(next))
		if id == 0 {
			break
		}
		next = uint32(id)
		out = append(out, clipFormat{id: next, name: formatName(next)})
	}
	return out
}

// formatName resolves a clipboard format id to its name where possible.
func formatName(id uint32) string {
	if id < 0xC000 {
		switch id {
		case cfText:
			return "CF_TEXT"
		case cfBitmap:
			return "CF_BITMAP"
		case 3:
			return "CF_METAFILEPICT"
		case 8:
			return "CF_DIB"
		case 9:
			return "CF_PALETTE"
		case cfUnicodeText:
			return "CF_UNICODETEXT"
		case 14:
			return "CF_ENHMETAFILE"
		case cfHDrop:
			return "CF_HDROP"
		case cfDIBV5:
			return "CF_DIBV5"
		}
		return ""
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetClipboardFormatNameW.Call(uintptr(id), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// isStorableCustomFormat filters out handles that are not HGLOBAL payloads
// (GlobalSize on a HBITMAP/HMETAFILE corrupts the heap) and clipboard plumbing.
func isStorableCustomFormat(f clipFormat) bool {
	switch f.id {
	case cfBitmap, 3 /*CF_METAFILEPICT*/, 9 /*CF_PALETTE*/, 14, /*CF_ENHMETAFILE*/
		cfText, cfUnicodeText, cfHDrop, cfDIB, cfDIBV5:
		return false
	}
	if formats.png != 0 && f.id == formats.png {
		return false
	}
	if formats.html != 0 && f.id == formats.html {
		return false
	}
	if formats.rtf != 0 && f.id == formats.rtf {
		return false
	}
	if formats.uriList != 0 && f.id == formats.uriList {
		return false
	}
	switch strings.ToLower(f.name) {
	case "", "dataobject", "ole private data", "dataobjectattributes",
		"dataobjectattributesrequiringelevation", "shell idlist array",
		"preferred dropeffect", "dropdescription", "zoneidentifier",
		"filename", "filenamew", "filegroupdescriptor", "filegroupdescriptorw",
		"untrusteddragdrop", "logical file name", "file contents":
		return false
	}
	return true
}

// rtfToText is a small RTF-to-plain converter: enough for previews and for the
// text fallback, not a full RTF parser.
func rtfToText(rtf string) string {
	var b strings.Builder
	depth := 0
	skipDepth := 0 // depth of the group we are currently ignoring (0 = not skipping)

	for i := 0; i < len(rtf); i++ {
		c := rtf[i]
		switch c {
		case '{':
			depth++
			if i+1 < len(rtf) {
				head := rtf[i+1:]
				for _, dest := range []string{
					"\\fonttbl", "\\colortbl", "\\stylesheet", "\\info", "\\*", "\\pict",
					"\\header", "\\footer", "\\object", "\\themedata", "\\datastore",
				} {
					if strings.HasPrefix(head, dest) {
						if skipDepth == 0 {
							skipDepth = depth
						}
						break
					}
				}
			}
		case '}':
			if skipDepth != 0 && depth <= skipDepth {
				skipDepth = 0
			}
			depth--
		case '\\':
			j := i + 1
			for j < len(rtf) && ((rtf[j] >= 'a' && rtf[j] <= 'z') || (rtf[j] >= 'A' && rtf[j] <= 'Z')) {
				j++
			}
			word := rtf[i+1 : j]
			for j < len(rtf) && (rtf[j] == '-' || (rtf[j] >= '0' && rtf[j] <= '9')) {
				j++
			}
			if j < len(rtf) && rtf[j] == ' ' {
				j++
			}
			if skipDepth == 0 {
				switch word {
				case "par", "line", "sect", "row":
					b.WriteString("\n")
				case "tab", "cell":
					b.WriteString("\t")
				case "emdash":
					b.WriteString("—")
				case "endash":
					b.WriteString("–")
				case "lquote":
					b.WriteString("‘")
				case "rquote":
					b.WriteString("’")
				case "ldblquote":
					b.WriteString("“")
				case "rdblquote":
					b.WriteString("”")
				case "bullet":
					b.WriteString("•")
				}
			}
			i = j - 1
		default:
			if skipDepth == 0 && c != '\r' && c != '\n' {
				b.WriteByte(c)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// humanBytes 在 capture_text.go（中立文件）：设置页/同步面板的文案要用同一份。

// htmlFragment extracts the clipboard HTML fragment using the byte offsets in
// its header.
func htmlFragment(data []byte) string {
	headerEnd := bytes.Index(data, []byte("<!--StartFragment-->"))
	if headerEnd >= 0 {
		start := headerEnd + len("<!--StartFragment-->")
		if end := bytes.Index(data[start:], []byte("<!--EndFragment-->")); end >= 0 {
			return string(data[start : start+end])
		}
	}
	startOff, endOff := -1, -1
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "StartFragment:"):
			if v, err := parseInt(strings.TrimPrefix(line, "StartFragment:")); err == nil {
				startOff = v
			}
		case strings.HasPrefix(line, "EndFragment:"):
			if v, err := parseInt(strings.TrimPrefix(line, "EndFragment:")); err == nil {
				endOff = v
			}
		}
	}
	if startOff > 0 && endOff > startOff && endOff <= len(data) {
		return string(data[startOff:endOff])
	}
	return ""
}

func parseInt(s string) (int, error) {
	var out int
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			if out > 0 {
				return out, nil
			}
			return 0, errors.New("not a number")
		}
		out = out*10 + int(r-'0')
	}
	if out == 0 {
		return 0, errors.New("empty number")
	}
	return out, nil
}

// ---- small helpers ------------------------------------------------------

// makePreview 在 capture_text.go（中立文件）：网页图片抓取与同步也要用同一条规则。

func isSingleURL(s string) bool {
	if s == "" || len(s) > 2048 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "ftps", "file", "mailto":
		return true
	}
	return false
}

func decodeANSI(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	r := make([]rune, 0, len(b))
	for _, c := range b {
		r = append(r, rune(c))
	}
	return string(r)
}

func stripANSI(s string) string { return strings.TrimRight(s, "\x00") }

func stripTags(html string) string {
	var out strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	text := out.String()
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	return strings.TrimSpace(text)
}

func utf16SliceToString(u []uint16) string {
	return windows.UTF16ToString(u)
}
