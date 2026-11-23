// Package ppocr 是可选的「高精度 OCR」引擎：PaddleOCR 的 PP-OCRv5 mobile
// 检测 + 识别模型，跑在 onnxruntime 上。
//
// 为什么另开一个包：它需要 cgo（onnxruntime 的 Go 绑定）。放在 internal/clip 里
// 会让整个应用离不开 cgo；单独成包之后，`engine_stub.go` 用构建标签提供同签名
// 的降级实现 —— 没有 cgo 的机器照样编译、照样能用系统自带引擎。
//
// 资产（都不入库，放数据目录下的 ocr/）：
//
//	onnxruntime.dll        ~21MB  onnxruntime 1.32（Windows x64）
//	det.onnx               ~4.6MB PP-OCRv5 mobile 文本检测
//	rec.onnx               ~16MB  PP-OCRv5 mobile 文本识别
//	ppocr_keys.txt         ~74KB  识别字典（18384 字）
//
// 精度对比见 docs/HISTORY.md：同一张深色界面截图，系统自带引擎把"坚果云"读成
// 「坚 果 云」（每个字之间塞空格）、把 URL 读成 https.//…；PP-OCR 这两处都是对的。
package ppocr

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 资源文件名（固定，下载器与手动放置都按这套名字找）。
const (
	FileLibrary = "onnxruntime.dll"
	FileDet     = "det.onnx"
	FileRec     = "rec.onnx"
	FileKeys    = "ppocr_keys.txt"
)

// ErrUnavailable 表示这个构建里没有 PP-OCR（非 Windows 或没开 cgo）。
var ErrUnavailable = errors.New("这个构建不支持高精度 OCR（需要 Windows + cgo）")

// ErrAssetsMissing 表示引擎代码在，但模型/动态库文件不全。
type ErrAssetsMissing struct {
	Dir     string
	Missing []string
}

func (e *ErrAssetsMissing) Error() string {
	return fmt.Sprintf("高精度 OCR 资源不全（%s 下缺 %s）", e.Dir, strings.Join(e.Missing, "、"))
}

// Ready 检查资源目录是否齐全；返回 nil 表示可以尝试初始化。
func Ready(dir string) error {
	if !compiledIn() {
		return ErrUnavailable
	}
	var missing []string
	for _, name := range []string{FileLibrary, FileDet, FileRec, FileKeys} {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.Size() == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return &ErrAssetsMissing{Dir: dir, Missing: missing}
	}
	return nil
}

// Options 是一次识别的可调参数（默认值就是 PaddleOCR 的常规取值）。
type Options struct {
	// DetLimitSide 检测阶段的最长边：再大只是慢，精度不涨
	DetLimitSide int
	// DetThresh 概率图二值化阈值
	DetThresh float64
	// BoxThresh 框内平均概率下限
	BoxThresh float64
	// UnclipRatio 框外扩比例（把字的边角包进来）
	UnclipRatio float64
	// MinBoxSide 小于这个边长的块直接丢（噪点）
	MinBoxSide int
	// RecHeight 识别输入高度（PP-OCR mobile 固定 48）
	RecHeight int
}

// DefaultOptions 返回 PaddleOCR 的常规取值。
func DefaultOptions() Options {
	return Options{
		DetLimitSide: 960,
		DetThresh:    0.30,
		BoxThresh:    0.50,
		UnclipRatio:  1.6,
		MinBoxSide:   3,
		RecHeight:    48,
	}
}

// RGBImage is a plain 8-bit RGB buffer: 解析出的图先归一到这里，后面的纯逻辑
// （缩放、切框、归一化）都不再依赖 image 包的具体实现。
type RGBImage struct {
	W, H int
	Pix  []uint8 // R,G,B 三个一组，行优先
}

// NewRGBImage 把任意 image.Image 摊平成 RGB 缓冲（丢 alpha，OCR 不需要）。
func NewRGBImage(src image.Image) *RGBImage {
	b := src.Bounds()
	out := &RGBImage{W: b.Dx(), H: b.Dy(), Pix: make([]uint8, 3*b.Dx()*b.Dy())}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*out.W + x) * 3
			out.Pix[i] = uint8(r >> 8)
			out.Pix[i+1] = uint8(g >> 8)
			out.Pix[i+2] = uint8(bl >> 8)
		}
	}
	return out
}

// Crop 切一个矩形（坐标已做边界裁剪）。
func (im *RGBImage) Crop(x0, y0, x1, y1 int) *RGBImage {
	x0, y0 = clampInt(x0, 0, im.W-1), clampInt(y0, 0, im.H-1)
	x1, y1 = clampInt(x1, x0, im.W-1), clampInt(y1, y0, im.H-1)
	w, h := x1-x0+1, y1-y0+1
	out := &RGBImage{W: w, H: h, Pix: make([]uint8, 3*w*h)}
	for y := 0; y < h; y++ {
		copy(out.Pix[(y*w)*3:(y*w+w)*3], im.Pix[((y0+y)*im.W+x0)*3:((y0+y)*im.W+x0+w)*3])
	}
	return out
}

// Box 是一个文字块（原图坐标，闭区间）。
type Box struct{ X0, Y0, X1, Y1 int }

// Width/Height 便于测试与日志。
func (b Box) Width() int  { return b.X1 - b.X0 + 1 }
func (b Box) Height() int { return b.Y1 - b.Y0 + 1 }

// ---- 纯逻辑（不依赖 onnxruntime，能单独跑测试）----------------------------

// boxesFromProbMap 把检测模型的概率图变成文字块。
//
// 做法是最朴素的连通域：阈值二值化 → 8 邻域 BFS → 框内平均概率过滤 → 外扩。
// PaddleOCR 官方用 OpenCV 的轮廓 + DB 后处理，这里用连通域已经够用（都是横排文本），
// 而且没有 cgo 之外的依赖，纯逻辑可以直接断言。
func boxesFromProbMap(prob []float32, w, h, origW, origH int, o Options) []Box {
	if w <= 0 || h <= 0 || len(prob) < w*h {
		return nil
	}
	scaleX := float64(w) / float64(origW)
	scaleY := float64(h) / float64(origH)
	visited := make([]bool, w*h)
	stack := make([]int, 0, 1024)
	var boxes []Box

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if visited[idx] || float64(prob[idx]) < o.DetThresh {
				continue
			}
			x0, y0, x1, y1 := x, y, x, y
			sum, n := 0.0, 0
			visited[idx] = true
			stack = append(stack[:0], idx)
			for len(stack) > 0 {
				cur := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				cx, cy := cur%w, cur/w
				sum += float64(prob[cur])
				n++
				if cx < x0 {
					x0 = cx
				}
				if cy < y0 {
					y0 = cy
				}
				if cx > x1 {
					x1 = cx
				}
				if cy > y1 {
					y1 = cy
				}
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						nx, ny := cx+dx, cy+dy
						if nx < 0 || ny < 0 || nx >= w || ny >= h {
							continue
						}
						ni := ny*w + nx
						if visited[ni] || float64(prob[ni]) < o.DetThresh {
							continue
						}
						visited[ni] = true
						stack = append(stack, ni)
					}
				}
			}
			if n == 0 || sum/float64(n) < o.BoxThresh {
				continue
			}
			bw, bh := x1-x0+1, y1-y0+1
			if bw < o.MinBoxSide || bh < o.MinBoxSide {
				continue
			}
			// 外扩距离照 PaddleOCR 的算式：面积×ratio/周长。按最长边乘比例会把
			// 一条 600×20 的行撑成 200 高，切出来全是背景（踩过）。
			dist := float64(bw*bh) * o.UnclipRatio / float64(2*(bw+bh))
			if lim := 0.5 * float64(minInt(bw, bh)); dist > lim {
				dist = lim
			}
			bx0 := maxFloat(0, float64(x0)-dist)
			by0 := maxFloat(0, float64(y0)-dist)
			bx1 := minFloat(float64(w-1), float64(x1)+dist)
			by1 := minFloat(float64(h-1), float64(y1)+dist)
			boxes = append(boxes, Box{
				X0: int(bx0 / scaleX), Y0: int(by0 / scaleY),
				X1: int(bx1 / scaleX), Y1: int(by1 / scaleY),
			})
		}
	}
	sortBoxes(boxes)
	return boxes
}

// sortBoxes 按"先上后下、同一行内从左到右"排 —— 这是人读图的顺序。
func sortBoxes(boxes []Box) {
	sort.Slice(boxes, func(i, j int) bool {
		ci, cj := (boxes[i].Y0+boxes[i].Y1)/2, (boxes[j].Y0+boxes[j].Y1)/2
		if absInt(ci-cj) < 10 {
			return boxes[i].X0 < boxes[j].X0
		}
		return ci < cj
	})
}

// ctcDecode 把识别模型的 (T, C) 概率按 CTC 贪心解码成字符串。
//
// 规则：每步取 argmax → 跳过 blank(0) → 跳过与上一步相同的（CTC 的 collapse）→
// 用 index-1 去字典取字（字典第 0 位对应 index 1）。
func ctcDecode(probs []float32, t, c int, dict []string) string {
	if t <= 0 || c <= 0 || len(probs) < t*c {
		return ""
	}
	var sb strings.Builder
	prev := -1
	for i := 0; i < t; i++ {
		best, bestP := 0, float32(-1)
		row := probs[i*c : (i+1)*c]
		for j, v := range row {
			if v > bestP {
				best, bestP = j, v
			}
		}
		if best == 0 || best == prev {
			prev = best
			continue
		}
		prev = best
		if best-1 < len(dict) {
			sb.WriteString(dict[best-1])
		}
	}
	return strings.TrimSpace(sb.String())
}

// resizeBilinear 双线性缩放（检测阶段整图缩放、识别阶段把每个框缩到高 48）。
func resizeBilinear(src *RGBImage, w, h int) *RGBImage {
	sw, sh := src.W, src.H
	dst := &RGBImage{W: w, H: h, Pix: make([]uint8, 3*w*h)}
	if sw == 0 || sh == 0 || w == 0 || h == 0 {
		return dst
	}
	at := func(x, y int) (float64, float64, float64) {
		i := (y*sw + x) * 3
		return float64(src.Pix[i]), float64(src.Pix[i+1]), float64(src.Pix[i+2])
	}
	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*float64(sh)/float64(h) - 0.5
		y0 := int(floor(fy))
		wy := fy - float64(y0)
		if y0 < 0 {
			y0, wy = 0, 0
		}
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		for x := 0; x < w; x++ {
			fx := (float64(x)+0.5)*float64(sw)/float64(w) - 0.5
			x0 := int(floor(fx))
			wx := fx - float64(x0)
			if x0 < 0 {
				x0, wx = 0, 0
			}
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			r00, g00, b00 := at(x0, y0)
			r10, g10, b10 := at(x1, y0)
			r01, g01, b01 := at(x0, y1)
			r11, g11, b11 := at(x1, y1)
			mix := func(v00, v10, v01, v11 float64) uint8 {
				top := v00*(1-wx) + v10*wx
				bot := v01*(1-wx) + v11*wx
				return uint8(top*(1-wy) + bot*wy + 0.5)
			}
			i := (y*w + x) * 3
			dst.Pix[i] = mix(r00, r10, r01, r11)
			dst.Pix[i+1] = mix(g00, g10, g01, g11)
			dst.Pix[i+2] = mix(b00, b10, b01, b11)
		}
	}
	return dst
}

// loadDict 读 PaddleOCR 的字典文件，并在末尾补一个空格字符（PP-OCR 会把空格当
// 普通字符识别；索引 0 留给 CTC blank）。
func loadDict(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("字典是空的: %s", path)
	}
	return append(out, " "), nil
}

func roundTo32(v int) int {
	if v < 32 {
		return 32
	}
	return (v + 31) / 32 * 32
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func floor(v float64) float64 {
	f := float64(int64(v))
	if v < 0 && f != v {
		return f - 1
	}
	return f
}
