package clip

// 资源包解压的边界：白名单、路径穿越、缺文件、半个文件不留。
// 不联网 —— 用的都是测试里现造的 zip。

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/bbqisbbq/clipnest/internal/ppocr"
)

// makeZip 在 dir 里造一个 zip，内容是 name → 内容。
func makeZip(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, "assets.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("建 zip: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip: %v", err)
	}
	return path
}

func allAssets() map[string]string {
	return map[string]string{
		ppocr.FileLibrary: "fake-dll",
		ppocr.FileDet:     "fake-det",
		ppocr.FileRec:     "fake-rec",
		ppocr.FileKeys:    "fake-keys",
	}
}

// 四个文件齐了才允许装，装完 Ready 通过。
func TestExtractOCRZipInstallsWhitelist(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	dir := t.TempDir()
	zipPath := makeZip(t, t.TempDir(), allAssets())

	n, err := svc.extractOCRZip(zipPath, dir)
	if err != nil {
		t.Fatalf("extractOCRZip: %v", err)
	}
	if n != 4 {
		t.Errorf("解出 %d 个文件，期望 4 个", n)
	}
	for name, want := range allAssets() {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读 %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s 内容不对: %q", name, got)
		}
	}
	if err := ppocr.Ready(dir); err != nil {
		t.Errorf("装完 Ready 仍失败: %v", err)
	}
	// 不该留下 .tmp
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("留下了临时文件: %v", left)
	}
}

// 夹带的东西一概不落地（白名单），路径穿越也进不来。
func TestExtractOCRZipIgnoresExtrasAndTraversal(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	dir := t.TempDir()
	files := allAssets()
	files["evil.exe"] = "nope"
	files["sub/notes.txt"] = "nope"
	zipPath := makeZip(t, t.TempDir(), files)

	if _, err := svc.extractOCRZip(zipPath, dir); err != nil {
		t.Fatalf("extractOCRZip: %v", err)
	}
	for _, bad := range []string{"evil.exe", "notes.txt", "sub/notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, bad)); err == nil {
			t.Errorf("白名单外的文件被写进去了: %s", bad)
		}
	}
}

// 少一个文件要报错，而且不能把已解出来的当成成功。
func TestExtractOCRZipMissingFileFails(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	dir := t.TempDir()
	files := allAssets()
	delete(files, ppocr.FileRec)
	zipPath := makeZip(t, t.TempDir(), files)

	if _, err := svc.extractOCRZip(zipPath, dir); err == nil {
		t.Fatal("缺文件却成功了")
	}
	if err := ppocr.Ready(dir); err == nil {
		t.Error("缺文件时 Ready 不该通过")
	}
}

// 不是 zip 时要给清楚的错误，而不是 panic。
func TestExtractOCRZipRejectsGarbage(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(path, []byte("not a zip at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.extractOCRZip(path, dir); err == nil {
		t.Fatal("垃圾数据却当成 zip 解开了")
	}
}

// 下载体积/哈希是写死的常量：改资源包却忘了改常量，必须在下载时就拦住。
func TestOCRAssetsPinnedConstantsSane(t *testing.T) {
	if ocrAssetsSize < 10<<20 {
		t.Errorf("体积常量看着不对: %d", ocrAssetsSize)
	}
	if len(ocrAssetsSHA) != 64 {
		t.Errorf("SHA256 应该是 64 个十六进制字符，实际 %d: %q", len(ocrAssetsSHA), ocrAssetsSHA)
	}
	if ocrAssetsURL == "" {
		t.Error("资源 URL 为空")
	}
}
