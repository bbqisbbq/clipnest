//go:build windows && cgo

package embed

// 真正的引擎：onnxruntime + bge-small-zh-v1.5（INT8）。
//
// cgo 只出现在这个文件里；没有 cgo 或不在 Windows 上时 engine_stub.go 提供同签名
// 的降级实现。**发布包必须 CGO_ENABLED=1**，否则编进来的是 stub。

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

func compiledIn() bool { return true }

// Engine 持有一个推理会话与词表。
//
// 一次只跑一条（编码 2.2ms，不值得为并发引入批次与额外内存），所以整个 Encode
// 串行在一把锁上。会话建立约 0.25 秒、驻留约 43MB —— 调用方（internal/clip）
// 有责任做懒加载与闲置释放，别让它常驻。
type Engine struct {
	dir     string
	vocab   *Vocab
	session *ort.DynamicAdvancedSession
	hasType bool // 模型要不要 token_type_ids（bge 的导出通常要）
	mu      sync.Mutex
	initErr error
}

// New 准备一个引擎实例（不立刻加载模型，第一次 Encode 时懒加载）。
func New(dir string) *Engine { return &Engine{dir: dir} }

// onnxruntime 的"环境"是**进程级全局**的：InitializeEnvironment 只能成功一次。
// PP-OCR 和这里共用同一个运行时，谁先初始化都行 —— 所以第二次的
// "already initialized" 要当成功处理，不能当成错误（踩过：ppocr 那条路径
// 就是这样把设置页的状态检查变成"引擎加载失败"的）。
var (
	envOnce sync.Once
	envErr  error
)

func initEnvironment(lib string) error {
	envOnce.Do(func() {
		ort.SetSharedLibraryPath(lib)
		if err := ort.InitializeEnvironment(); err != nil {
			// 已经被 ppocr 初始化过：运行时是好的，继续用
			if !isAlreadyInitialized(err) {
				envErr = err
			}
		}
	})
	return envErr
}

func isAlreadyInitialized(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already initialized") || strings.Contains(msg, "already been initialized")
}

// Close 释放会话（把 43MB 还回去）。之后再 Encode 会重新加载。
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		_ = e.session.Destroy()
		e.session = nil
	}
}

// Loaded 报告会话是否已经建立（调用方据此决定要不要按闲置释放）。
func (e *Engine) Loaded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.session != nil
}

// Init 直接加载（给设置页的"能不能用"体检用；成功返回 nil）。
func (e *Engine) Init() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.init()
}

// init 懒初始化（调用者持锁）。
func (e *Engine) init() error {
	if e.session != nil {
		return nil
	}
	if e.initErr != nil {
		return e.initErr
	}
	if err := Ready(e.dir); err != nil {
		e.initErr = err
		return err
	}
	lib := LibraryPath(e.dir)
	if lib == "" {
		e.initErr = fmt.Errorf("找不到 onnxruntime.dll")
		return e.initErr
	}
	if err := initEnvironment(lib); err != nil {
		e.initErr = fmt.Errorf("onnxruntime 初始化失败: %w", err)
		return e.initErr
	}
	vocab, err := LoadVocab(filepath.Join(e.dir, FileVocab))
	if err != nil {
		e.initErr = err
		return err
	}
	modelPath := filepath.Join(e.dir, FileModel)
	infos, outs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		e.initErr = fmt.Errorf("读模型输入输出失败: %w", err)
		return e.initErr
	}
	names := make([]string, 0, len(infos))
	for _, in := range infos {
		names = append(names, in.Name)
		if in.Name == "token_type_ids" {
			e.hasType = true
		}
	}
	outNames := make([]string, 0, len(outs))
	for _, o := range outs {
		outNames = append(outNames, o.Name)
	}
	if len(outNames) == 0 {
		e.initErr = fmt.Errorf("模型没有输出")
		return e.initErr
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		e.initErr = err
		return e.initErr
	}
	// 单线程：一次只编一条，多开的线程只会抢 CPU（而且实测 2.2ms 已经够快）
	if err := opts.SetIntraOpNumThreads(1); err != nil {
		_ = err // 老版本没有这个选项：不影响正确性
	}
	s, err := ort.NewDynamicAdvancedSession(modelPath, names, outNames, opts)
	if err != nil {
		e.initErr = fmt.Errorf("加载模型失败: %w", err)
		return e.initErr
	}
	e.vocab, e.session = vocab, s
	return nil
}

// Encode 把文本变成 512 维单位向量（CLS 池化 + L2 归一）。
func (e *Engine) Encode(text string) (Vector, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.init(); err != nil {
		return Vector{}, err
	}
	ids, mask := e.vocab.Encode(text, MaxLen)
	seq := int64(len(ids))
	shape := ort.NewShape(1, seq)

	idT, err := ort.NewTensor(shape, ids)
	if err != nil {
		return Vector{}, err
	}
	defer idT.Destroy()
	maskT, err := ort.NewTensor(shape, mask)
	if err != nil {
		return Vector{}, err
	}
	defer maskT.Destroy()

	inputs := []ort.Value{idT, maskT}
	if e.hasType {
		types := make([]int64, len(ids))
		ttT, err := ort.NewTensor(shape, types)
		if err != nil {
			return Vector{}, err
		}
		defer ttT.Destroy()
		inputs = append(inputs, ttT)
	}

	outs := []ort.Value{nil}
	if err := e.session.Run(inputs, outs); err != nil {
		return Vector{}, fmt.Errorf("推理失败: %w", err)
	}
	out, ok := outs[0].(*ort.Tensor[float32])
	if !ok || out == nil {
		return Vector{}, fmt.Errorf("输出类型异常")
	}
	defer out.Destroy()
	dims := []int64(out.GetShape())
	if len(dims) != 3 || int(dims[2]) != Dim {
		return Vector{}, fmt.Errorf("输出维度异常: %v（期望 (1, L, %d)）", dims, Dim)
	}
	// CLS 池化：取第一个 token 的隐状态（bge 系列的官方作法，不是 mean pooling）
	cls := make([]float32, Dim)
	copy(cls, out.GetData()[:Dim])
	return Quantize(normalize(cls)), nil
}

// normalize 原地 L2 归一（零向量保持零）。
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum <= 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}
