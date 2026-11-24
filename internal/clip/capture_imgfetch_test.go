package clip

import (
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// 网页「复制图片」的补丁：认得"整段就是一张 <img>"，别的富文本不许误判。
//
// 这些用例里的 HTML 片段是照 Chrome/Edge 真实写进剪贴板的形状抄下来的
// （`go run ./scratch/clipfmt` 在真机上看过：HTML Format 里就一个 <img src>，
// 位图根本没出现）。

func TestImageOnlyFragmentPicksTheImage(t *testing.T) {
	cases := []struct {
		name string
		frag string
		want string
	}{
		{
			name: "chrome 图片复制（带内联样式）",
			frag: `<img id="pic" src="http://127.0.0.1:8765/pic.png" alt="probe image" width="400" height="300" style="display: block; width: 400px;">`,
			want: "http://127.0.0.1:8765/pic.png",
		},
		{
			name: "被链接包一层",
			frag: `<a href="https://example.com/post/1"><img src="https://cdn.example.com/a/b.jpg"></a>`,
			want: "https://cdn.example.com/a/b.jpg",
		},
		{
			name: "单引号 + meta 头 + 换行",
			frag: "<meta charset='utf-8'>\n<p><img alt='x' src='https://img.example.com/x.webp' /></p>\n",
			want: "https://img.example.com/x.webp",
		},
		{
			name: "无引号属性",
			frag: `<img src=https://example.com/raw.gif width=10>`,
			want: "https://example.com/raw.gif",
		},
		{
			name: "内联 data URI",
			frag: `<img src="data:image/png;base64,AAAA">`,
			want: "data:image/png;base64,AAAA",
		},
		{
			name: "片段里的注释不该干扰判断",
			frag: `<!--StartFragment--><img src="https://example.com/c.png"><!--EndFragment-->`,
			want: "https://example.com/c.png",
		},
	}

	for _, tc := range cases {
		got, ok := imageOnlyFragment(tc.frag)
		if !ok || got != tc.want {
			t.Errorf("%s: imageOnlyFragment(%q) = (%q, %v)，想要 (%q, true)", tc.name, tc.frag, got, ok, tc.want)
		}
	}
}

func TestImageOnlyFragmentLeavesRichTextAlone(t *testing.T) {
	cases := []struct{ name, frag string }{
		{"带字的图文", `<p>这张图说明了问题：</p><img src="https://example.com/a.png">`},
		{"两张图", `<img src="https://example.com/a.png"><img src="https://example.com/b.png">`},
		{"纯文字", `<span style="color: red">一段话</span>`},
		{"只有链接", `<a href="https://example.com">看这里</a>`},
		{"img 没有 src", `<img alt="没有地址">`},
		{"已有内部引用", `<img src="memoimg:abcdef">`},
		{"空片段", ``},
	}
	for _, tc := range cases {
		if src, ok := imageOnlyFragment(tc.frag); ok {
			t.Errorf("%s: 不该被当成图片，却解析出 %q", tc.name, src)
		}
	}
}

// 关掉自动下载（或下载失败）时，条目必须当场可用：降级成一条带地址的富文本。
func TestRichFallbackFromRemoteKeepsTheAddress(t *testing.T) {
	cap := Captured{
		Kind:           KindImage,
		RemoteImageURL: "https://example.com/pic.png",
		Text:           "图片说明",
		HTML:           `<img src="https://example.com/pic.png" alt="图片说明">`,
		SourceApp:      "msedge.exe",
	}
	fallback := richFallbackFromRemote(cap)
	if fallback.Kind != KindRichText {
		t.Fatalf("kind = %q，想要 rich", fallback.Kind)
	}
	if fallback.Text != "图片说明" {
		t.Errorf("text = %q，alt 文字应该保留", fallback.Text)
	}
	if !strings.Contains(fallback.HTML, "https://example.com/pic.png") {
		t.Errorf("html 丢了图片地址: %q", fallback.HTML)
	}
	if fallback.Hash != htmlHash(fallback.Text, fallback.HTML) {
		t.Errorf("降级条目必须用 htmlHash 口径（回声抑制与去重都靠它）")
	}
	if fallback.SourceApp != "msedge.exe" {
		t.Errorf("sourceApp 丢了：%q", fallback.SourceApp)
	}

	// 连 alt 都没有时，正文退回地址本身——空白条目等于把这次复制丢了。
	noAlt := richFallbackFromRemote(Captured{RemoteImageURL: "https://example.com/bare.png"})
	if noAlt.Text != "https://example.com/bare.png" {
		t.Errorf("没有 alt 时正文应该是地址，实际 %q", noAlt.Text)
	}
}

// data: URI 走本地解码（不需要网络），并且统一转成 PNG 入库。
func TestFetchRemoteImageFromDataURI(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var buf strings.Builder
	{
		var enc []byte
		w := &sliceWriter{&enc}
		if err := png.Encode(w, src); err != nil {
			t.Fatal(err)
		}
		buf.WriteString("data:image/png;base64,")
		buf.WriteString(base64.StdEncoding.EncodeToString(enc))
	}

	got, err := fetchRemoteImage(context.Background(), buf.String())
	if err != nil {
		t.Fatalf("fetchRemoteImage: %v", err)
	}
	if got.Kind != KindImage || len(got.PNG) == 0 {
		t.Fatalf("没有拿到图片字节：kind=%q png=%d", got.Kind, len(got.PNG))
	}
	if got.Width != 3 || got.Height != 2 {
		t.Errorf("尺寸 = %dx%d，想要 3x2", got.Width, got.Height)
	}
	if len(got.Hash) != 32 {
		t.Errorf("hash 应该是 32 位十六进制（采集口径），实际 %q", got.Hash)
	}
}

// sliceWriter 让测试不用再 import bytes（保持用例无依赖）。
type sliceWriter struct{ buf *[]byte }

func (w *sliceWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}
