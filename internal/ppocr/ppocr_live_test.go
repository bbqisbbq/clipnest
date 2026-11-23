//go:build windows && cgo

package ppocr

// 真引擎用例：默认跳过（要 42MB 资源、要 cgo、跑一次约 1 秒）。
//
//	$env:CLIPNEST_PPOCR_LIVE=1
//	$env:CLIPNEST_PPOCR_DIR=E:\default\clipnest\scratch\ppocr-assets\engine
//	$env:CLIPNEST_PPOCR_IMAGE=E:\default\clipnest\scratch\ocr-spike\test-zh.png
//	go test ./internal/ppocr -run TestLiveRecognize -v
//
// 断言的是"这条路通不通、中文有没有读对"，不追求逐字精确 ——
// 系统引擎与 PP-OCR 的精度对比记在 docs/HISTORY.md。

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveRecognize(t *testing.T) {
	if os.Getenv("CLIPNEST_PPOCR_LIVE") != "1" {
		t.Skip("设 CLIPNEST_PPOCR_LIVE=1 才跑（需要 cgo + 42MB 的 OCR 资源）")
	}
	dir := os.Getenv("CLIPNEST_PPOCR_DIR")
	if dir == "" {
		t.Skip("没设 CLIPNEST_PPOCR_DIR")
	}
	if err := Ready(dir); err != nil {
		t.Fatalf("资源没准备好: %v", err)
	}
	imgPath := os.Getenv("CLIPNEST_PPOCR_IMAGE")
	if imgPath == "" {
		t.Skip("没设 CLIPNEST_PPOCR_IMAGE")
	}
	png, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatalf("读图: %v", err)
	}

	eng := New(dir, DefaultOptions())
	defer eng.Close()

	start := time.Now()
	text, err := eng.Recognize(png)
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	first := time.Since(start)

	// 第二次是"热"的：模型已加载，只剩推理
	start = time.Now()
	if _, err := eng.Recognize(png); err != nil {
		t.Fatalf("Recognize(第二次): %v", err)
	}
	warm := time.Since(start)
	t.Logf("冷 %d ms / 热 %d ms，%d 字", first.Milliseconds(), warm.Milliseconds(), len([]rune(text)))
	t.Logf("识别结果:\n%s", text)

	for _, want := range []string{"坚果云", "WebDAV", "dav.jianguoyun.com"} {
		if !strings.Contains(text, want) {
			t.Errorf("结果里没有 %q", want)
		}
	}
	// 系统引擎在同样这张图上读成"坚 果 云"，PP-OCR 不该再被塞空格
	if strings.Contains(text, "坚 果 云") {
		t.Errorf("中文字之间不该有空格: %q", text)
	}
}
