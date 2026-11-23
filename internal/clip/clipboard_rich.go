package clip

// 图文混排条目"回填到剪贴板"的两种载荷：把正文里的 `memoimg:<hash>` 换成真字节。
//
// 背景：条目正文里的图片从来不是字节，而是一个内容寻址令牌（见 entry_blob.go）。
// 纯文本回填会把 `![](memoimg:ab12…)` 原样交给目标程序 —— 图片丢了，还多出一串垃圾。
// 修法不是"把图单独放剪贴板"（那样文字和图只能进去一样），而是同时放三份表示：
//
//	CF_HTML  —— 图片内联成 data URI（浏览器 / Electron / 富文本框认这个）
//	RTF      —— 图片嵌成 \pict（Word / WPS / Outlook / WordPad 认这个）
//	CF_UNICODETEXT —— 纯文本兜底（记事本、聊天框）
//
// 这个文件只做纯变换（HTML → data URI、HTML → RTF、令牌剥离），不碰剪贴板 API，
// 所以能在任何平台上跑单测。真正的写入在 clipboard_write_windows.go。

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image/png"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// memoTokenRE 与前端 lib/images.ts 的 MEMO_IMG_RE 同一形状（hash 至少 8 位）。
var memoTokenRE = regexp.MustCompile(`memoimg:([0-9a-f]{8,64})`)

// memoMarkdownRE 匹配 Markdown 里的整段图片语法 `![alt](memoimg:hash)`，
// 用来从纯文本兜底里整段删掉（而不是留下一个空括号）。
var memoMarkdownRE = regexp.MustCompile(`!\[[^\]]*\]\(\s*memoimg:[0-9a-f]{8,64}[^)]*\)`)

// memoImgTagRE 匹配 HTML 里的 <img ... src="memoimg:hash" ...>：
// 拿不到字节时整条删掉，免得目标程序显示一个破图。
var memoImgTagRE = regexp.MustCompile(`<img[^>]*memoimg:[0-9a-f]{8,64}[^>]*>`)

// memoTokensIn lists the distinct image hashes referenced by an HTML fragment or a
// Markdown body, in first-appearance order.
func memoTokensIn(s string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, m := range memoTokenRE.FindAllStringSubmatch(s, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

// stripMemoTokens turns a Markdown body into plain text: image references disappear
// (a missing image is better than `![](memoimg:…)` dumped into someone's chat box).
func stripMemoTokens(md string) string {
	out := memoMarkdownRE.ReplaceAllString(md, "")
	out = strings.ReplaceAll(out, "\r\n", "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

// embedMemoImages replaces every `memoimg:<hash>` in the HTML with a data URI so the
// fragment stands on its own (the clipboard has no access to our blob store).
//
// pngs 里没有的 hash（附件已被清理）整条 <img> 删掉：留着 src 只会让目标显示破图。
func embedMemoImages(fragment string, pngs map[string][]byte) string {
	out := memoTokenRE.ReplaceAllStringFunc(fragment, func(token string) string {
		hash := strings.TrimPrefix(token, "memoimg:")
		data, ok := pngs[hash]
		if !ok || len(data) == 0 {
			return token // 先留着，下面整条 <img> 删
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	})
	return memoImgTagRE.ReplaceAllString(out, "")
}

// ---- HTML → RTF ---------------------------------------------------------

// rtfFontTable 用系统自带字体，不引外部依赖：中文字体给 fcharset134（GB2312）。
const rtfHeader = `{\rtf1\ansi\ansicpg936\deff0\uc1` +
	`{\fonttbl{\f0\fnil\fcharset134 Microsoft YaHei;}{\f1\fmodern\fcharset0 Consolas;}}` +
	`\viewkind4\pard\f0\fs21 ` + "\n"

// htmlToRTF renders a small HTML fragment as RTF with embedded PNG pictures.
//
// 只覆盖我们自己会产出的那些标签（body 来自 marked：p / br / strong / em / code /
// pre / ul / ol / li / h1-h6 / blockquote / hr / a / img / table 的常见形状）。
// 不认识的标签按行内处理（只要文字），宁可丢一点样式，也不能丢内容。
func htmlToRTF(fragment string, pngs map[string][]byte) string {
	doc, err := html.Parse(strings.NewReader("<div id=\"clipnest-root\">" + fragment + "</div>"))
	if err != nil {
		// 解析失败也要给点东西：把标记当纯文本
		return rtfHeader + rtfEscape(stripTagsLoose(fragment)) + "}"
	}
	root := findByID(doc, "clipnest-root")
	if root == nil {
		return rtfHeader + rtfEscape(stripTagsLoose(fragment)) + "}"
	}

	w := &rtfWriter{out: &strings.Builder{}, pngs: pngs}
	w.out.WriteString(rtfHeader)
	w.walk(root, rtfStyle{})
	w.out.WriteString("\n}")
	return w.out.String()
}

type rtfStyle struct {
	bold, italic, strike, mono bool
	listDepth                  int
	listOrdered                bool
	listIndex                  int
}

type rtfWriter struct {
	out  *strings.Builder
	pngs map[string][]byte
}

func (w *rtfWriter) walk(n *html.Node, st rtfStyle) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c, st)
	}
}

func (w *rtfWriter) node(n *html.Node, st rtfStyle) {
	if n.Type == html.TextNode {
		w.out.WriteString(rtfEscape(collapseWS(n.Data)))
		return
	}
	if n.Type != html.ElementNode {
		return
	}
	switch n.Data {
	case "p", "div", "section", "article", "header", "footer":
		w.block(st, func(en rtfStyle) { w.walk(n, en) })
	case "h1", "h2", "h3", "h4", "h5", "h6":
		size := map[string]int{"h1": 34, "h2": 30, "h3": 26, "h4": 24, "h5": 22, "h6": 21}[n.Data]
		w.out.WriteString(fmt.Sprintf(`\pard\sb120\sa60\b\fs%d `, size))
		w.walk(n, st)
		w.out.WriteString(`\b0\fs21\par` + "\n")
	case "br":
		w.out.WriteString(`\line `)
	case "hr":
		w.out.WriteString(`\pard\brdrb\brdrs\brdrw10\brsp20\par` + "\n")
	case "strong", "b":
		en := st
		en.bold = true
		w.walk(n, en)
	case "em", "i":
		en := st
		en.italic = true
		w.walk(n, en)
	case "del", "s", "strike":
		en := st
		en.strike = true
		w.walk(n, en)
	case "code", "kbd", "samp":
		en := st
		en.mono = true
		w.walk(n, en)
	case "pre":
		w.out.WriteString(`\pard\sb60\sa60\f1\fs18 `)
		w.out.WriteString(rtfEscape(textOf(n)))
		w.out.WriteString(`\f0\fs21\par` + "\n")
	case "blockquote":
		w.out.WriteString(`\pard\li720\sa60 `)
		w.walk(n, st)
		w.out.WriteString(`\pard\li0\par` + "\n")
	case "ul", "ol":
		en := st
		en.listDepth++
		en.listOrdered = n.Data == "ol"
		en.listIndex = 0
		w.walk(n, en)
	case "li":
		w.out.WriteString(fmt.Sprintf(`\pard\fi-360\li%d `, 360*(st.listDepth+1)))
		if st.listOrdered {
			st.listIndex++
			w.out.WriteString(fmt.Sprintf(`%d.\tab `, st.listIndex))
		} else {
			w.out.WriteString(`\bullet\tab `)
		}
		w.walk(n, st)
		w.out.WriteString("\n")
	case "table":
		w.walk(n, st)
	case "tr":
		w.out.WriteString(`\pard `)
		w.walk(n, st)
		w.out.WriteString(`\par` + "\n")
	case "td", "th":
		w.walk(n, st)
		w.out.WriteString(`\tab `)
	case "a":
		// 只保留文字：链接在 RTF 里要 \field 结构，收益不值这个复杂度
		w.walk(n, st)
	case "img":
		w.image(n)
	default:
		w.walk(n, st)
	}
}

// block emits a paragraph wrapper around inline content.
func (w *rtfWriter) block(st rtfStyle, render func(rtfStyle)) {
	w.out.WriteString(`\pard\sa60 `)
	w.style(st, true)
	render(st)
	w.style(st, false)
	w.out.WriteString(`\par` + "\n")
}

// style turns inline emphasis on/off around a run.
func (w *rtfWriter) style(st rtfStyle, on bool) {
	toggle := func(cond bool, onWord, offWord string) {
		if !cond {
			return
		}
		if on {
			w.out.WriteString(onWord + " ")
		} else {
			w.out.WriteString(offWord + " ")
		}
	}
	toggle(st.bold, `\b`, `\b0`)
	toggle(st.italic, `\i`, `\i0`)
	toggle(st.strike, `\strike`, `\strike0`)
	toggle(st.mono, `\f1`, `\f0`)
}

func (w *rtfWriter) image(n *html.Node) {
	src := ""
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "src") {
			src = a.Val
		}
	}
	data := w.pngFor(src)
	if len(data) == 0 {
		return // 字节不在了：整张图跳过，别让目标显示破图
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return
	}
	const twipsPerPixel = 15 // 96 DPI：1px = 1/96 inch = 15 twips
	fmt.Fprintf(w.out, `{\pict\pngblip\picw%d\pich%d\picwgoal%d\pichgoal%d`+"\n",
		cfg.Width, cfg.Height, cfg.Width*twipsPerPixel, cfg.Height*twipsPerPixel)
	w.out.WriteString(hex.EncodeToString(data))
	w.out.WriteString("}\n")
}

// pngFor resolves an <img src> to PNG bytes: either a `memoimg:<hash>` token (the
// normal case) or an already-inlined data URI (when the caller inlined first).
func (w *rtfWriter) pngFor(src string) []byte {
	if strings.HasPrefix(src, "data:image/") {
		if i := strings.IndexByte(src, ','); i > 0 && strings.Contains(src[:i], ";base64") {
			if raw, err := base64.StdEncoding.DecodeString(src[i+1:]); err == nil {
				return raw
			}
		}
		return nil
	}
	return w.pngs[strings.TrimPrefix(src, "memoimg:")]
}

// rtfEscape writes text safely: braces and backslashes escaped, non-ASCII as \uN?
// (uc1 means the trailing "?" is dropped by the reader).
func rtfEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '{':
			b.WriteString(`\{`)
		case r == '}':
			b.WriteString(`\}`)
		case r == '\n':
			b.WriteString(`\line `)
		case r == '\t':
			b.WriteString(`\tab `)
		case r < 0x20:
			// 其他控制字符丢掉
		case r < 0x7f:
			b.WriteRune(r)
		default:
			b.WriteString(fmt.Sprintf(`\u%d?`, r))
		}
	}
	return b.String()
}

// collapseWS squashes runs of whitespace the way HTML rendering does, so pasted
// text does not inherit `<pre>`-style line wrapping from the source HTML.
func collapseWS(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// stripTagsLoose is the last-resort text extraction (only used when HTML parsing
// itself fails).
func stripTagsLoose(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// findByID finds the element with the given id (the wrapper we inject).
func findByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if strings.EqualFold(a.Key, "id") && a.Val == id {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findByID(c, id); found != nil {
			return found
		}
	}
	return nil
}

// textOf concatenates every descendant text node (used for <pre>, where whitespace
// and newlines must survive verbatim instead of being collapsed).
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}