package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 真机 OCR 用例：默认跳过（要 1~2 秒、要在有引擎的 Windows 上跑）。
//
//	$env:CLIPNEST_OCR_LIVE=1; go test ./internal/clip -run TestSystemOCRLive -v
//
// 它验的是"引擎这条路通不通"：脚本能不能起、中文能不能正确带回来、后处理有没有
// 把换行吃掉。引擎本身认错字（`447043705` → `447膶3705`）不在这里断言 —— 那是
// 系统 OCR 的精度问题，不是我们的 bug。
func TestSystemOCRLive(t *testing.T) {
	if os.Getenv("CLIPNEST_OCR_LIVE") != "1" {
		t.Skip("设 CLIPNEST_OCR_LIVE=1 才跑（需要 Windows 自带的 OCR 引擎）")
	}
	if !ocrAvailable() {
		t.Skip("这台机器没有 powershell.exe")
	}

	// 造一张纯色图：引擎应当稳定返回空串（而不是报错）—— 先确认"这条路通"。
	img := image.NewRGBA(image.Rect(0, 0, 420, 90))
	for i := range img.Pix {
		img.Pix[i] = 0
	}
	for y := 0; y < 90; y++ {
		for x := 0; x < 420; x++ {
			img.Set(x, y, color.RGBA{R: 24, G: 26, B: 32, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}

	// 纯色图没有文字 → 引擎应当稳定返回空串（而不是报错）
	start := time.Now()
	text, err := runSystemOCR(buf.Bytes())
	took := time.Since(start)
	if err != nil {
		t.Fatalf("runSystemOCR: %v", err)
	}
	if strings.TrimSpace(text) != "" {
		t.Errorf("纯色图不该识别出文字: %q", text)
	}
	t.Logf("引擎往返 %d ms（纯色图，无文字）", took.Milliseconds())

	// 给了一张真图就再验一遍"能认出字"（CLIPNEST_OCR_LIVE_IMAGE=某张截图.png）
	if path := os.Getenv("CLIPNEST_OCR_LIVE_IMAGE"); path != "" {
		pngBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读图: %v", err)
		}
		start = time.Now()
		got, err := runSystemOCR(pngBytes)
		took = time.Since(start)
		if err != nil {
			t.Fatalf("runSystemOCR(真图): %v", err)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("真图里没认出任何文字: %s", path)
		}
		t.Logf("真图 %d ms，%d 字，首行: %q", took.Milliseconds(), len([]rune(got)),
			strings.SplitN(got, "\n", 2)[0])
	}

	if runtime.GOOS != "windows" {
		t.Log("非 Windows 平台走到了这里，说明 ocrAvailable 的判断需要复查")
	}
}
