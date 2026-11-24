package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// 「复制图片文件」的判定规则：能存就存成图，存不了（多选/超限/解不开）就退回路径引用。
// 这组用例不碰剪贴板，任何平台都能跑。

func writeTestImage(t *testing.T, dir, name string, encode func(*os.File, image.Image) error) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: uint8(y * 10), B: 120, A: 255})
		}
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	defer f.Close()
	if err := encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	return path
}

func refFor(t *testing.T, path string) FileRef {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return FileRef{Path: path, Name: filepath.Base(path), Size: fi.Size()}
}

func TestImageFileCapturePNG(t *testing.T) {
	dir := t.TempDir()
	path := writeTestImage(t, dir, "shot.png", func(f *os.File, img image.Image) error {
		return png.Encode(f, img)
	})
	raw, _ := os.ReadFile(path)

	c := imageFileCapture([]FileRef{refFor(t, path)})
	if c == nil {
		t.Fatal("单张 png 应该存成图片条目，实际退回了引用")
	}
	if c.Kind != KindImage {
		t.Fatalf("Kind = %q, want %q", c.Kind, KindImage)
	}
	if c.Width != 40 || c.Height != 24 {
		t.Fatalf("尺寸 = %d×%d, want 40×24", c.Width, c.Height)
	}
	if !bytes.Equal(c.PNG, raw) {
		t.Fatal("存进去的字节应该与源文件完全一致（不重编码）")
	}
	if len(c.Files) != 1 || c.Files[0].Path != path {
		t.Fatal("文件引用要保留，详情页才能查到来源路径")
	}
	if c.Hash == "" {
		t.Fatal("Hash 不能为空：同图重复复制要去重")
	}
}

func TestImageFileCaptureJPEGAndGIF(t *testing.T) {
	dir := t.TempDir()
	jpg := writeTestImage(t, dir, "photo.JPG", func(f *os.File, img image.Image) error {
		return jpeg.Encode(f, img, nil)
	})
	gifPath := writeTestImage(t, dir, "anim.gif", func(f *os.File, img image.Image) error {
		return gif.Encode(f, img, nil)
	})

	for _, p := range []string{jpg, gifPath} {
		c := imageFileCapture([]FileRef{refFor(t, p)})
		if c == nil || c.Kind != KindImage {
			t.Fatalf("%s（扩展名大小写不敏感）应该存成图片条目", filepath.Base(p))
		}
		if c.Width != 40 || c.Height != 24 {
			t.Fatalf("%s 尺寸 = %d×%d", filepath.Base(p), c.Width, c.Height)
		}
	}
}

func TestImageFileCaptureFallsBackToReferences(t *testing.T) {
	dir := t.TempDir()
	pngPath := writeTestImage(t, dir, "a.png", func(f *os.File, img image.Image) error {
		return png.Encode(f, img)
	})
	both := []FileRef{refFor(t, pngPath), refFor(t, pngPath)}

	notImage := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notImage, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.png")
	if err := os.WriteFile(broken, []byte("这不是一张图"), 0o644); err != nil {
		t.Fatal(err)
	}
	huge := refFor(t, pngPath)
	huge.Size = imageFileMaxBytes + 1

	cases := []struct {
		name  string
		files []FileRef
	}{
		{"多选两个文件", both},
		{"零个文件", nil},
		{"不是图片扩展名", []FileRef{refFor(t, notImage)}},
		{"扩展名是图片但内容不是", []FileRef{refFor(t, broken)}},
		{"目录", []FileRef{{Path: dir, Name: "dir", IsDir: true}}},
		{"超过 24MB", []FileRef{huge}},
		{"文件不存在", []FileRef{{Path: filepath.Join(dir, "gone.png"), Name: "gone.png", Size: 100}}},
	}
	for _, tc := range cases {
		if c := imageFileCapture(tc.files); c != nil {
			t.Errorf("%s：应该退回路径引用，实际返回了 %q", tc.name, c.Kind)
		}
	}
}
