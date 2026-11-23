package clip

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// 图文混排回填的纯变换用例：令牌剥离、data URI 内联、HTML → RTF。
// 剪贴板 API 那一层（WriteRichToClipboard）不在这里测，见文件末尾的 opt-in 用例。

func tinyRGBA(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

const testHash = "0123456789abcdef01234567"

func TestStripMemoTokens(t *testing.T) {
	md := "第一段文字\n\n![](memoimg:" + testHash + ")\n\n第二段文字\n"
	got := stripMemoTokens(md)
	if strings.Contains(got, "memoimg:") {
		t.Fatalf("纯文本里还留着图片令牌: %q", got)
	}
	if strings.Contains(got, "![](") {
		t.Fatalf("纯文本里还留着空的 Markdown 图片语法: %q", got)
	}
	for _, want := range []string{"第一段文字", "第二段文字"} {
		if !strings.Contains(got, want) {
			t.Errorf("纯文本丢了 %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("删掉图片后留下了连续空行: %q", got)
	}
	if !strings.HasPrefix(got, "第一段文字") {
		t.Errorf("首尾空白没清干净: %q", got)
	}
}

func TestEmbedMemoImages(t *testing.T) {
	pngData := tinyRGBA(t)
	html := `<p>看图</p><p><img src="memoimg:` + testHash + `" alt=""></p>`
	out := embedMemoImages(html, map[string][]byte{testHash: pngData})

	if strings.Contains(out, "memoimg:") {
		t.Fatalf("HTML 里还留着令牌: %s", out)
	}
	wantPrefix := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)
	if !strings.Contains(out, wantPrefix) {
		t.Errorf("图片没有内联成 data URI: %s", out)
	}
	if !strings.Contains(out, "看图") {
		t.Errorf("文字丢了: %s", out)
	}
}

// 附件被清理掉的条目：整条 <img> 删掉，别让目标程序显示破图图标。
func TestEmbedMemoImagesDropsMissing(t *testing.T) {
	html := `<p>前面</p><img src="memoimg:` + testHash + `"><p>后面</p>`
	out := embedMemoImages(html, nil)
	if strings.Contains(out, "<img") || strings.Contains(out, "memoimg:") {
		t.Fatalf("拿不到字节的图片应该整条删掉: %s", out)
	}
	if !strings.Contains(out, "前面") || !strings.Contains(out, "后面") {
		t.Errorf("文字不该被牵连: %s", out)
	}
}

func TestHTMLToRTFParagraphsAndEscaping(t *testing.T) {
	rtf := htmlToRTF("<p>甲</p><p>乙 &lt;丁&gt;</p>", nil)
	if !strings.HasPrefix(rtf, `{\rtf1`) {
		t.Fatalf("不是 RTF: %s", rtf)
	}
	if !strings.HasSuffix(rtf, "}") {
		t.Fatalf("RTF 没闭合: %s", rtf)
	}
	// RTF 的 \uN 收的是**十进制**码位（甲=U+7532=30002），后面那个 ? 由 \uc1 吃掉
	for _, want := range []string{`\u30002?`, `\u20057?`, `\u19969?`} {
		if !strings.Contains(rtf, want) {
			t.Errorf("中文字符没有转义成 %s: %s", want, rtf)
		}
	}
	if strings.Count(rtf, `\par`) < 2 {
		t.Errorf("两段应该产生两个 \\par: %s", rtf)
	}
	// HTML 实体要还原成真字符（&lt;丁&gt; → 丁），不能把 &lt; 原样带过去
	if strings.Contains(rtf, "&lt;") || strings.Contains(rtf, "&gt;") {
		t.Errorf("HTML 实体没有还原: %s", rtf)
	}
}

// RTF 的大括号必须配平：少一个，Word 会把后面整段当纯文本或者直接吞掉。
func TestHTMLToRTFBracesBalanced(t *testing.T) {
	pngData := tinyRGBA(t)
	rtf := htmlToRTF(`<h2>标题</h2><p><strong>粗</strong>与<em>斜</em>与<code>码</code></p>`+
		`<blockquote><p>引用</p></blockquote><hr><table><tr><td>甲</td><td>乙</td></tr></table>`+
		`<p><img src="memoimg:`+testHash+`"></p>`, map[string][]byte{testHash: pngData})

	// 大括号与反斜杠转义都要算进去：\|{ \} 不算括号
	depth := 0
	escaped := false
	for _, r := range rtf {
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				t.Fatalf("大括号提前闭合: %s", rtf)
			}
		}
	}
	if depth != 0 {
		t.Fatalf("大括号不配平（差 %d）: %s", depth, rtf)
	}
	// <hr> 与表格不该把控制字漏成正文；引用/编号/等宽这些该有的控制字要在
	for _, want := range []string{`\brdrb`, `\li720`, `\f1`, `\tab`} {
		if !strings.Contains(rtf, want) {
			t.Errorf("缺少 %s: %s", want, rtf)
		}
	}
}

func TestHTMLToRTFImageEmitsPict(t *testing.T) {
	pngData := tinyRGBA(t)
	rtf := htmlToRTF(`<p>图：</p><p><img src="memoimg:`+testHash+`"></p>`,
		map[string][]byte{testHash: pngData})

	if !strings.Contains(rtf, `{\pict\pngblip`) {
		t.Fatalf("图片没有变成 \\pict 组: %s", rtf)
	}
	// 4×3 像素 → goal 尺寸是 4*15 / 3*15 twips
	if !strings.Contains(rtf, `\picw4\pich3\picwgoal60\pichgoal45`) {
		t.Errorf("图片尺寸不对（应当是 4×3 像素 = 60×45 twips）: %s", rtf)
	}
	if !strings.Contains(rtf, base64ToHex(base64.StdEncoding.EncodeToString(pngData))) {
		t.Error("\\pict 里没有 PNG 的十六进制数据")
	}
}

// 正文里的字要原样出现在 RTF 里（\u 转义之后），图片是 \pict 组 —— 且**没有**
// 把 Markdown/HTML 的残留（`![](`, `<img`, `memoimg:`）带进去。
func TestHTMLToRTFHasNoMarkupLeftovers(t *testing.T) {
	pngData := tinyRGBA(t)
	html := "<p>第一段 &amp; 符号</p><ul><li>一项</li></ul><p><img src=\"memoimg:" + testHash + "\"></p><p>最后一段</p>"
	rtf := htmlToRTF(html, map[string][]byte{testHash: pngData})

	if strings.Contains(rtf, "<p>") || strings.Contains(rtf, "&amp;") || strings.Contains(rtf, "memoimg:") {
		t.Fatalf("RTF 里还留着标记或令牌: %s", rtf)
	}
	for _, want := range []string{"第一段", "符号", "一项", "最后一段"} {
		if !strings.Contains(rtf, rtfEscape(want)) {
			t.Errorf("RTF 里找不到 %q: %s", want, rtf)
		}
	}
	if !strings.Contains(rtf, `{\pict\pngblip`) {
		t.Errorf("图片没有嵌进 RTF: %s", rtf)
	}
}

func TestHTMLToRTFListsAndCode(t *testing.T) {
	rtf := htmlToRTF("<ul><li>甲</li></ul><ol><li>乙</li></ol><pre>func main() {}</pre>", nil)
	if !strings.Contains(rtf, `\bullet`) {
		t.Errorf("无序列表没有项目符号: %s", rtf)
	}
	if !strings.Contains(rtf, `1.\tab`) {
		t.Errorf("有序列表没有编号: %s", rtf)
	}
	if !strings.Contains(rtf, `\f1`) {
		t.Errorf("代码块没有等宽字体: %s", rtf)
	}
}

// memoTokensIn 要按出现顺序去重（同样的图在正文里出现两次只取一次）。
func TestMemoTokensInDedupes(t *testing.T) {
	html := `<img src="memoimg:` + testHash + `"><p>中间</p><img src="memoimg:` + testHash + `">`
	got := memoTokensIn(html)
	if len(got) != 1 || got[0] != testHash {
		t.Fatalf("got %v, want [%s]", got, testHash)
	}
}

// base64ToHex 把 base64 串转成 RTF 里的十六进制写法（测试用）。
func base64ToHex(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, len(raw)*2)
	for _, b := range raw {
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}
