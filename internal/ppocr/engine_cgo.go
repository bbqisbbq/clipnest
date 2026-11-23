//go:build windows && cgo

package ppocr

// 真正的引擎：onnxruntime + PP-OCRv5 mobile（det + rec）。
//
// cgo 只出现在这个文件里 —— 没开 cgo 或不在 Windows 上时，engine_stub.go 提供
// 同签名的降级实现，整个应用照常编译。

/*
#cgo LDFLAGS: -lstdc++
*/
import "C"

import (
	"bytes"
	"fmt"
	"image/png"
	"path/filepath"
	"sync"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

func compiledIn() bool { return true }

// Engine 持有一对会话（det/rec）与字典。初始化一次约 0.5 秒，之后每张图 0.3 秒左右。
type Engine struct {
	dir     string
	opts    Options
	dict    []string
	det     *onnxSession
	rec     *onnxSession
	mu      sync.Mutex
	initErr error
}

type onnxSession struct {
	s   *ort.DynamicAdvancedSession
	out ort.Shape // 模型声明的输出形状（动态维是 -1）
}

// New 准备一个引擎实例（不会立刻加载模型，第一次 Recognize 时懒加载）。
func New(dir string, opts Options) *Engine {
	if opts.DetLimitSide == 0 {
		opts = DefaultOptions()
	}
	return &Engine{dir: dir, opts: opts}
}

// Close 释放 onnxruntime 会话。
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.det != nil {
		_ = e.det.s.Destroy()
		e.det = nil
	}
	if e.rec != nil {
		_ = e.rec.s.Destroy()
		e.rec = nil
	}
}

// onnxruntime 的"环境"是**进程级全局**的：InitializeEnvironment 只能成功一次，
// 再调就报 "The onnxruntime has already been initialized"（踩过：设置页做一次状态
// 检查先把环境初始化了，真正识别时再初始化就直接失败、悄悄退回系统引擎）。
// 所以这里用 sync.Once 兜住，动态库路径也只在第一次生效（资源目录本来就只有一个）。
var (
	envOnce sync.Once
	envErr  error
)

func initEnvironment(libraryPath string) error {
	envOnce.Do(func() {
		ort.SetSharedLibraryPath(libraryPath)
		envErr = ort.InitializeEnvironment()
	})
	return envErr
}

// Init 加载 onnxruntime、两个模型和字典（重复调用是空操作）。
func (e *Engine) Init() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.init()
}

// init 懒初始化：加载 onnxruntime、两个模型和字典。
func (e *Engine) init() error {
	if e.rec != nil && e.det != nil {
		return nil
	}
	if e.initErr != nil {
		return e.initErr
	}
	start := time.Now()
	if err := initEnvironment(filepath.Join(e.dir, FileLibrary)); err != nil {
		e.initErr = fmt.Errorf("onnxruntime 初始化失败: %w", err)
		return e.initErr
	}
	dict, err := loadDict(filepath.Join(e.dir, FileKeys))
	if err != nil {
		e.initErr = fmt.Errorf("读字典失败: %w", err)
		return e.initErr
	}
	det, err := openSession(filepath.Join(e.dir, FileDet))
	if err != nil {
		e.initErr = fmt.Errorf("加载检测模型: %w", err)
		return e.initErr
	}
	rec, err := openSession(filepath.Join(e.dir, FileRec))
	if err != nil {
		e.initErr = fmt.Errorf("加载识别模型: %w", err)
		return e.initErr
	}
	e.dict, e.det, e.rec = dict, det, rec
	_ = start
	return nil
}

func openSession(path string) (*onnxSession, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	inputs, outputs, err := ort.GetInputOutputInfo(path)
	if err != nil {
		return nil, err
	}
	if len(inputs) != 1 || len(outputs) != 1 {
		return nil, fmt.Errorf("%s: 期望 1 进 1 出，实际 %d/%d", filepath.Base(path), len(inputs), len(outputs))
	}
	s, err := ort.NewDynamicAdvancedSession(path, []string{inputs[0].Name}, []string{outputs[0].Name}, opts)
	if err != nil {
		return nil, err
	}
	return &onnxSession{s: s, out: outputs[0].Dimensions}, nil
}

// Recognize 识别 PNG 里的文字，按行返回（每行一个文字块，从上到下）。
func (e *Engine) Recognize(pngBytes []byte) (string, error) {
	if err := Ready(e.dir); err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.init(); err != nil {
		return "", err
	}
	src, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return "", fmt.Errorf("解码图片: %w", err)
	}
	img := NewRGBImage(src)

	boxes, err := e.detect(img)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(boxes))
	for _, b := range boxes {
		if text, err := e.recognizeBox(img, b); err == nil && text != "" {
			lines = append(lines, text)
		}
	}
	return joinLines(lines), nil
}

// detect 跑检测模型，得到原图坐标下的文字块。
func (e *Engine) detect(img *RGBImage) ([]Box, error) {
	scale := 1.0
	if maxInt(img.W, img.H) > e.opts.DetLimitSide {
		scale = float64(e.opts.DetLimitSide) / float64(maxInt(img.W, img.H))
	}
	w := roundTo32(int(float64(img.W)*scale + 0.5))
	h := roundTo32(int(float64(img.H)*scale + 0.5))
	resized := resizeBilinear(img, w, h)

	// 检测的归一化与识别不同：ImageNet 的 mean/std
	const (
		m0, m1, m2 = 0.485, 0.456, 0.406
		s0, s1, s2 = 0.229, 0.224, 0.225
	)
	data := make([]float32, 3*w*h)
	for i := 0; i < w*h; i++ {
		data[i] = (float32(resized.Pix[i*3])/255 - m0) / s0
		data[w*h+i] = (float32(resized.Pix[i*3+1])/255 - m1) / s1
		data[2*w*h+i] = (float32(resized.Pix[i*3+2])/255 - m2) / s2
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, int64(h), int64(w)), data)
	if err != nil {
		return nil, fmt.Errorf("检测输入: %w", err)
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	if err := e.det.s.Run([]ort.Value{in}, outs); err != nil {
		return nil, fmt.Errorf("检测推理: %w", err)
	}
	out, ok := outs[0].(*ort.Tensor[float32])
	if !ok || out == nil {
		return nil, fmt.Errorf("检测输出类型异常")
	}
	defer out.Destroy()
	return boxesFromProbMap(out.GetData(), w, h, img.W, img.H, e.opts), nil
}

// recognizeBox 把一个文字块缩到高 48 后跑识别模型，CTC 解码成文字。
func (e *Engine) recognizeBox(img *RGBImage, b Box) (string, error) {
	crop := img.Crop(b.X0, b.Y0, b.X1, b.Y1)
	tw := int(float64(crop.W)*float64(e.opts.RecHeight)/float64(crop.H) + 0.5)
	if tw < 4 {
		tw = 4
	}
	if tw > 2000 {
		tw = 2000
	}
	scaled := resizeBilinear(crop, tw, e.opts.RecHeight)

	n := e.opts.RecHeight * tw
	data := make([]float32, 3*n)
	for i := 0; i < n; i++ {
		// 识别的归一化：x/127.5 - 1（等价于 (x/255-0.5)/0.5）
		data[i] = float32(scaled.Pix[i*3])/127.5 - 1
		data[n+i] = float32(scaled.Pix[i*3+1])/127.5 - 1
		data[2*n+i] = float32(scaled.Pix[i*3+2])/127.5 - 1
	}
	in, err := ort.NewTensor(ort.NewShape(1, 3, int64(e.opts.RecHeight), int64(tw)), data)
	if err != nil {
		return "", err
	}
	defer in.Destroy()
	// 输出是 (1, T, C) 的动态形状：传 nil 让 ORT 自己分配，跑完读实际形状
	outs := []ort.Value{nil}
	if err := e.rec.s.Run([]ort.Value{in}, outs); err != nil {
		return "", fmt.Errorf("识别推理: %w", err)
	}
	out, ok := outs[0].(*ort.Tensor[float32])
	if !ok || out == nil {
		return "", fmt.Errorf("识别输出类型异常")
	}
	defer out.Destroy()
	dims := []int64(out.GetShape())
	if len(dims) != 3 {
		return "", fmt.Errorf("识别输出维度异常: %v", dims)
	}
	return ctcDecode(out.GetData(), int(dims[1]), int(dims[2]), e.dict), nil
}

// joinLines 把逐块结果拼成多行文本。
func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
