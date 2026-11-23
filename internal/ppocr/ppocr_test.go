package ppocr

// 纯逻辑测试：不加载 onnxruntime、不需要模型文件，任何平台都能跑。
// （真引擎那条路在 ppocr_live_test.go，靠环境变量打开。）

import (
	"os"
	"testing"
)

// ctcDecode：blank 跳过、重复折叠、index-1 映射到字典。
func TestCTCDecode(t *testing.T) {
	dict := []string{"a", "b", "c", " "} // 索引 1..4
	// 三字符概率矩阵：(T=6, C=5)，C = blank + 4 个字典字
	build := func(picks []int) []float32 {
		probs := make([]float32, len(picks)*5)
		for i, p := range picks {
			probs[i*5+p] = 0.9
		}
		return probs
	}
	cases := []struct {
		name  string
		picks []int
		want  string
	}{
		{"普通三连", []int{1, 2, 3}, "abc"},
		{"重复折叠", []int{1, 1, 2, 2, 3, 3}, "abc"},
		{"blank 分隔的重复要保留", []int{1, 0, 1}, "aa"},
		{"全是 blank", []int{0, 0, 0}, ""},
		{"带空格", []int{1, 4, 2}, "a b"},
	}
	for _, c := range cases {
		got := ctcDecode(build(c.picks), len(c.picks), 5, dict)
		if got != c.want {
			t.Errorf("%s: ctcDecode = %q, want %q", c.name, got, c.want)
		}
	}
}

// boxesFromProbMap：两条明显分开的横条 → 两个框，且按先上后下排。
func TestBoxesFromProbMap(t *testing.T) {
	const w, h = 100, 40
	prob := make([]float32, w*h)
	paint := func(x0, y0, x1, y1 int) {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				prob[y*w+x] = 0.9
			}
		}
	}
	paint(10, 5, 60, 12)  // 上面一行
	paint(10, 25, 80, 32) // 下面一行

	opts := DefaultOptions()
	boxes := boxesFromProbMap(prob, w, h, w, h, opts)
	if len(boxes) != 2 {
		t.Fatalf("期望 2 个框，得到 %d 个: %+v", len(boxes), boxes)
	}
	if boxes[0].Y1 > boxes[1].Y0 {
		t.Errorf("排序应该是先上后下: %+v", boxes)
	}
	// 外扩后仍应包住原始文字区域，且不能把整张图都吞掉
	if boxes[0].X0 > 10 || boxes[0].X1 < 60 {
		t.Errorf("外扩后没包住第一行: %+v", boxes[0])
	}
	if boxes[0].Height() > 20 {
		t.Errorf("外扩过头（行高被撑成 %d）: %+v", boxes[0].Height(), boxes[0])
	}
}

// 概率低于框内均值下限的块要被丢掉（噪点）。
func TestBoxesFromProbMapDropsNoise(t *testing.T) {
	const w, h = 50, 20
	prob := make([]float32, w*h)
	for y := 5; y <= 8; y++ {
		for x := 5; x <= 30; x++ {
			prob[y*w+x] = 0.35 // 过阈值，但均值低于 0.5
		}
	}
	if boxes := boxesFromProbMap(prob, w, h, w, h, DefaultOptions()); len(boxes) != 0 {
		t.Errorf("低均值噪点不该成框: %+v", boxes)
	}
}

// 缩放保持尺寸正确，且颜色大致不跑偏（纯色图缩放后还是那个颜色）。
func TestResizeBilinearKeepsColor(t *testing.T) {
	src := &RGBImage{W: 2, H: 2, Pix: make([]uint8, 12)}
	for i := 0; i < 12; i += 3 {
		src.Pix[i] = 200
		src.Pix[i+1] = 100
		src.Pix[i+2] = 50
	}
	out := resizeBilinear(src, 7, 5)
	if out.W != 7 || out.H != 5 {
		t.Fatalf("尺寸不对: %dx%d", out.W, out.H)
	}
	if out.Pix[0] != 200 || out.Pix[1] != 100 || out.Pix[2] != 50 {
		t.Errorf("颜色跑偏: %v", out.Pix[:3])
	}
}

// Crop 的坐标要裁剪到图内（框可能被外扩推成负数）。
func TestCropClampsBounds(t *testing.T) {
	src := &RGBImage{W: 10, H: 10, Pix: make([]uint8, 300)}
	out := src.Crop(-5, -5, 100, 100)
	if out.W != 10 || out.H != 10 {
		t.Fatalf("越界裁剪没有夹住: %dx%d", out.W, out.H)
	}
}

// 字典读取：空行丢掉，末尾补空格字符。
func TestLoadDictAddsTrailingSpace(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/keys.txt"
	if err := os.WriteFile(path, []byte("a\nb\n\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dict, err := loadDict(path)
	if err != nil {
		t.Fatalf("loadDict: %v", err)
	}
	if len(dict) != 4 || dict[3] != " " {
		t.Errorf("字典应是 3 个字 + 末尾空格，实际 %d 项: %v", len(dict), dict)
	}
}
