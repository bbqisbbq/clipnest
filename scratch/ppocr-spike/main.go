// ppocr-spike：在 Go 里跑一遍 PP-OCRv5 mobile（det + rec），用来判断"高精度 OCR 引擎"
// 到底值不值得做 —— 关键是拿同一张图和系统自带引擎的识别结果对比。
//
//	go run ./scratch/ppocr-spike -png scratch/ocr-spike/test-zh.png
//
// 依赖（都不进仓库，放 scratch/ppocr-assets/）：
//   ort132/runtimes/win-x64/native/onnxruntime.dll   onnxruntime 1.32（NuGet 包里抠出来的）
//   det.onnx / rec.onnx / ppocr_keys_ocrv5.txt    PP-OCRv5 mobile 模型与字典
//
// 管线照 PaddleOCR 的标准做法：det 出概率图 → 阈值二值化 → 连通域取框 → 按行排序 →
// 逐框裁剪到高 48 → rec 出 (T,C) 概率 → CTC 贪心解码。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	detLimitSide = 960 // 检测阶段最长边：再大只是慢，精度不涨
	detMean0     = 0.485
	detMean1     = 0.456
	detMean2     = 0.406
	detStd0      = 0.229
	detStd1      = 0.224
	detStd2      = 0.225
	detThresh    = 0.30 // 概率图二值化阈值（PaddleOCR 默认 0.3）
	boxThresh    = 0.50 // 框内平均概率下限
	unclipRatio  = 1.6  // 框外扩，把字的边角包进来
	minBoxSide   = 3
	recHeight    = 48
	recMaxWidth  = 2000
)

type box struct{ x0, y0, x1, y1 int }

func main() {
	pngPath := flag.String("png", "scratch/ocr-spike/test-zh.png", "要识别的图片")
	assets := flag.String("assets", "scratch/ppocr-assets", "模型与 onnxruntime.dll 所在目录")
	flag.Parse()

	start := time.Now()
	ort.SetSharedLibraryPath(filepath.Join(*assets, "ort132", "onnxruntime", "capi", "onnxruntime.dll"))
	if err := ort.InitializeEnvironment(); err != nil {
		fail("onnxruntime 初始化失败: %v", err)
	}
	defer func() { _ = ort.DestroyEnvironment() }()
	initMs := time.Since(start).Milliseconds()

	img := loadImage(*pngPath)
	dict := loadDict(filepath.Join(*assets, "ppocr_keys_ocrv5.txt"))
	fmt.Printf("图片 %d×%d，字典 %d 字，ort 初始化 %d ms\n", img.Bounds().Dx(), img.Bounds().Dy(), len(dict), initMs)

	det, err := newSession(filepath.Join(*assets, "det.onnx"))
	if err != nil {
		fail("加载 det 模型: %v", err)
	}
	defer det.Destroy()

	rec, err := newSession(filepath.Join(*assets, "rec.onnx"))
	if err != nil {
		fail("加载 rec 模型: %v", err)
	}
	defer rec.Destroy()

	// ---- 检测
	t0 := time.Now()
	boxes, detScale := detect(det, img)
	detMs := time.Since(t0).Milliseconds()
	fmt.Printf("检测：%d 个文字块，%d ms\n", len(boxes), detMs)

	// ---- 识别
	t1 := time.Now()
	var lines []string
	for _, b := range boxes {
		text := recognize(rec, img, b, detScale, dict)
		fmt.Printf("  [%d,%d %dx%d] %q\n", b.x0, b.y0, b.x1-b.x0+1, b.y1-b.y0+1, text)
		if text != "" {
			lines = append(lines, text)
		}
	}
	recMs := time.Since(t1).Milliseconds()
	fmt.Printf("识别：%d ms（%d 块）\n", recMs, len(boxes))
	fmt.Printf("合计 %d ms\n--- 识别结果 ---\n%s\n", time.Since(start).Milliseconds(), strings.Join(lines, "\n"))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func loadImage(path string) *image.RGBA {
	f, err := os.Open(path)
	if err != nil {
		fail("打开图片: %v", err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		fail("解码 png: %v", err)
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func loadDict(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		fail("读字典: %v", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	// PaddleOCR 的字典末尾补一个空格（识别空格字符），索引 0 留给 CTC blank
	out = append(out, " ")
	return out
}

type session struct {
	s   *ort.DynamicAdvancedSession
	in  string
	out string
	// outDims 是模型声明的输出形状（动态维是 -1）；classes 是最后一维（rec 的字符集大小）
	outDims ort.Shape
	classes int
}

func newSession(modelPath string) (*session, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	inputs, outputs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, err
	}
	if len(inputs) != 1 || len(outputs) != 1 {
		return nil, fmt.Errorf("%s: 期望 1 进 1 出，实际 %d/%d", filepath.Base(modelPath), len(inputs), len(outputs))
	}
	s, err := ort.NewDynamicAdvancedSession(modelPath, []string{inputs[0].Name}, []string{outputs[0].Name}, opts)
	if err != nil {
		return nil, err
	}
	classes := 0
	if d := outputs[0].Dimensions; len(d) > 0 && d[len(d)-1] > 0 {
		classes = int(d[len(d)-1])
	}
	return &session{s: s, in: inputs[0].Name, out: outputs[0].Name, outDims: outputs[0].Dimensions, classes: classes}, nil
}

func (s *session) Destroy() { _ = s.s.Destroy() }

// ---- 检测 -----------------------------------------------------------------

// detect returns the text boxes in **original image** coordinates, plus the scale
// factor used for the detector (needed when cropping).
func detect(s *session, img *image.RGBA) ([]box, float64) {
	w0, h0 := img.Bounds().Dx(), img.Bounds().Dy()
	scale := 1.0
	if maxInt(w0, h0) > detLimitSide {
		scale = float64(detLimitSide) / float64(maxInt(w0, h0))
	}
	// 32 的倍数（网络有 5 次下采样）
	w := roundTo32(int(math.Round(float64(w0) * scale)))
	h := roundTo32(int(math.Round(float64(h0) * scale)))
	scaleX := float64(w) / float64(w0)
	scaleY := float64(h) / float64(h0)

	resized := resizeBilinear(img, w, h)
	data := make([]float32, 3*w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := resized.At(x, y).RGBA()
			i := y*w + x
			data[i] = (float32(r>>8)/255 - detMean0) / detStd0
			data[w*h+i] = (float32(g>>8)/255 - detMean1) / detStd1
			data[2*w*h+i] = (float32(b>>8)/255 - detMean2) / detStd2
		}
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, int64(h), int64(w)), data)
	if err != nil {
		fail("det 输入张量: %v", err)
	}
	defer in.Destroy()
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, int64(h), int64(w)))
	if err != nil {
		fail("det 输出张量: %v", err)
	}
	defer out.Destroy()
	if err := s.s.Run([]ort.Value{in}, []ort.Value{out}); err != nil {
		fail("det 推理: %v", err)
	}
	prob := out.GetData()
	if os.Getenv("PPOCR_DEBUG") == "1" {
		mn, mx, sum := prob[0], prob[0], float64(0)
		for _, v := range prob {
			if v < mn {
				mn = v
			}
			if v > mx {
				mx = v
			}
			sum += float64(v)
		}
		fmt.Printf("  det 输出 %dx%d，min=%.3f max=%.3f mean=%.4f\n", w, h, mn, mx, sum/float64(len(prob)))
		// 粗看一下概率图长什么样：每格取块内最大
		sx, sy := w/76, h/22
		if sx < 1 {
			sx = 1
		}
		if sy < 1 {
			sy = 1
		}
		for by := 0; by < h; by += sy {
			line := ""
			for bx := 0; bx < w; bx += sx {
				best := float32(0)
				for y := by; y < by+sy && y < h; y++ {
					for x := bx; x < bx+sx && x < w; x++ {
						if v := prob[y*w+x]; v > best {
							best = v
						}
					}
				}
				if best > 0.3 {
					line += "#"
				} else {
					line += "."
				}
			}
			fmt.Println("  " + line)
		}
	}

	// 二值化 → 连通域 → 框
	visited := make([]bool, w*h)
	var boxes []box
	stack := make([]int, 0, 1024)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if visited[idx] || prob[idx] < detThresh {
				continue
			}
			// BFS 一个连通块
			x0, y0, x1, y1 := x, y, x, y
			sum, n := float64(0), 0
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
						if visited[ni] || prob[ni] < detThresh {
							continue
						}
						visited[ni] = true
						stack = append(stack, ni)
					}
				}
			}
			if n == 0 || sum/float64(n) < boxThresh {
				continue
			}
			bw, bh := x1-x0+1, y1-y0+1
			if bw < minBoxSide || bh < minBoxSide {
				continue
			}
			// 外扩（unclip）：照 PaddleOCR 的算式 —— 用"面积/周长"推一个**统一**外扩距离。
			// 之前按最长边乘比例外扩，一条 600×20 的行会被撑成 200 高，切出来全是背景，
			// rec 自然什么都读不出（也解释了那些 0,0 起步的巨大框）。
			dist := float64(bw*bh) * unclipRatio / float64(2*(bw+bh))
			if lim := 0.5 * float64(minInt(bw, bh)); dist > lim {
				dist = lim
			}
			bx0 := math.Max(0, float64(x0)-dist)
			by0 := math.Max(0, float64(y0)-dist)
			bx1 := math.Min(float64(w-1), float64(x1)+dist)
			by1 := math.Min(float64(h-1), float64(y1)+dist)
			boxes = append(boxes, box{
				x0: int(bx0 / scaleX), y0: int(by0 / scaleY),
				x1: int(bx1 / scaleX), y1: int(by1 / scaleY),
			})
		}
	}
	// 读数顺序：先上后下，同一行内从左到右
	sort.Slice(boxes, func(i, j int) bool {
		ci, cj := (boxes[i].y0+boxes[i].y1)/2, (boxes[j].y0+boxes[j].y1)/2
		if absInt(ci-cj) < 10 {
			return boxes[i].x0 < boxes[j].x0
		}
		return ci < cj
	})
	return boxes, scale
}

// ---- 识别 -----------------------------------------------------------------

func recognize(s *session, img *image.RGBA, b box, _ float64, dict []string) string {
	cw, ch := b.x1-b.x0+1, b.y1-b.y0+1
	if cw <= 0 || ch <= 0 {
		return ""
	}
	crop := image.NewRGBA(image.Rect(0, 0, cw, ch))
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			crop.Set(x, y, img.At(b.x0+x, b.y0+y))
		}
	}
	// 统一到高 48，宽度按比例（PaddleOCR 的 mobile rec 输入）
	tw := int(math.Round(float64(cw) * float64(recHeight) / float64(ch)))
	if tw < 4 {
		tw = 4
	}
	if tw > recMaxWidth {
		tw = recMaxWidth
	}
	scaled := resizeBilinear(crop, tw, recHeight)

	data := make([]float32, 3*recHeight*tw)
	for y := 0; y < recHeight; y++ {
		for x := 0; x < tw; x++ {
			r, g, b2, _ := scaled.At(x, y).RGBA()
			i := y*tw + x
			n := recHeight * tw
			data[i] = float32(r>>8)/127.5 - 1
			data[n+i] = float32(g>>8)/127.5 - 1
			data[2*n+i] = float32(b2>>8)/127.5 - 1
		}
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, recHeight, int64(tw)), data)
	if err != nil {
		fail("rec 输入张量: %v", err)
	}
	defer in.Destroy()

	// 输出形状是动态的 (1, T, C)：T 随宽度变。传 nil 让 ORT 自己分配，跑完再读实际形状。
	outs := []ort.Value{nil}
	if err := s.s.Run([]ort.Value{in}, outs); err != nil {
		fail("rec 推理: %v", err)
	}
	out, ok := outs[0].(*ort.Tensor[float32])
	if !ok || out == nil {
		return ""
	}
	defer out.Destroy()
	return ctcDecode(out.GetData(), out.GetShape(), dict)
}



func ctcDecode(data []float32, shape ort.Shape, dict []string) string {
	dims := []int64(shape)
	if len(dims) != 3 {
		return ""
	}
	t, c := int(dims[1]), int(dims[2])
	var sb strings.Builder
	prev := -1
	for i := 0; i < t; i++ {
		best, bestP := 0, float32(-1)
		for j := 0; j < c; j++ {
			if v := data[i*c+j]; v > bestP {
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

// ---- 图像工具 -------------------------------------------------------------

func resizeBilinear(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*float64(sh)/float64(h) - 0.5
		y0 := int(math.Floor(fy))
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
			x0 := int(math.Floor(fx))
			wx := fx - float64(x0)
			if x0 < 0 {
				x0, wx = 0, 0
			}
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			r00, g00, b00, _ := src.At(x0, y0).RGBA()
			r10, g10, b10, _ := src.At(x1, y0).RGBA()
			r01, g01, b01, _ := src.At(x0, y1).RGBA()
			r11, g11, b11, _ := src.At(x1, y1).RGBA()
			blend := func(v00, v10, v01, v11 uint32) uint8 {
				top := float64(v00>>8)*(1-wx) + float64(v10>>8)*wx
				bot := float64(v01>>8)*(1-wx) + float64(v11>>8)*wx
				return uint8(math.Round(top*(1-wy) + bot*wy))
			}
			dst.Set(x, y, color.RGBA{
				R: blend(r00, r10, r01, r11),
				G: blend(g00, g10, g01, g11),
				B: blend(b00, b10, b01, b11),
				A: 255,
			})
		}
	}
	return dst
}

func roundTo32(v int) int {
	if v < 32 {
		return 32
	}
	return (v + 31) / 32 * 32
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

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
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
