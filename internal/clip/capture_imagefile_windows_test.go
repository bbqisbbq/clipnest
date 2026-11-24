//go:build windows

package clip

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 复制一个图片文件，走完整条采集链路：buildFilesCapture → SaveCapture → entry_blobs。
// 期望：条目变成 image（正文是 ![](memoimg:…)），字节原样存进附件，MIME 认得出 jpeg。
func TestCopyImageFileBecomesImageEntry(t *testing.T) {
	dir := t.TempDir()

	// 造一张 jpg 文件（模拟"在资源管理器里复制一张照片"）
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for x := 0; x < 32; x++ {
		img.Set(x, 3, image.Black)
	}
	path := filepath.Join(dir, "photo.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// 第一跳：CF_HDROP → Captured
	c := buildFilesCapture([]FileRef{statFile(FileRef{Path: path, Name: "photo.jpg"})})
	if c == nil {
		t.Fatal("buildFilesCapture 返回 nil")
	}
	if c.Kind != KindImage {
		t.Fatalf("单张图片文件应该采集为 %q，实际 %q", KindImage, c.Kind)
	}
	if c.Width != 32 || c.Height != 16 {
		t.Fatalf("尺寸 = %d×%d, want 32×16", c.Width, c.Height)
	}

	// 第二跳：Captured → 条目 + 附件
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e, isNew, err := st.SaveCapture(*c)
	if err != nil || !isNew {
		t.Fatalf("SaveCapture: isNew=%v err=%v", isNew, err)
	}
	if e.Kind != KindImage {
		t.Fatalf("条目 Kind = %q, want %q", e.Kind, KindImage)
	}
	token := memoImageToken(e.Content)
	if token == "" {
		t.Fatalf("正文里应该有 memoimg 令牌，实际 %q", e.Content)
	}
	payload, mime, err := st.LoadEntryBlob(token)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, raw) {
		t.Fatal("附件字节应与源文件完全一致（不重编码）")
	}
	if mime != "image/jpeg" {
		t.Fatalf("MIME = %q, want image/jpeg（出库要靠它决定 data URL 前缀）", mime)
	}
	if !strings.HasPrefix(token, memoImagePrefix) {
		t.Fatalf("令牌前缀不对：%q", token)
	}
}

// 多文件/非图片仍然按引用记：正文里必须是路径，别悄悄变成图片条目。
func TestCopyNonImageFilesStaysReferences(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(p, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := buildFilesCapture([]FileRef{statFile(FileRef{Path: p, Name: "report.pdf"})})
	if c == nil {
		t.Fatal("buildFilesCapture 返回 nil")
	}
	if c.Kind != KindFiles {
		t.Fatalf("非图片应该仍是 %q，实际 %q", KindFiles, c.Kind)
	}
	if !strings.Contains(c.Text, p) {
		t.Fatalf("正文里应该有路径，实际 %q", c.Text)
	}
}
