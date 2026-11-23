//go:build !windows || !cgo

package ppocr

// 降级实现：不在 Windows 上，或者构建时没开 cgo（没装 gcc 的机器）。
//
// 存在意义是"别让可选功能绑死编译链"：internal/clip 只依赖这个包的公开 API，
// 没有 cgo 也照样编译，只是高精度引擎用不了（设置里会显示原因，自动退回系统引擎）。

func compiledIn() bool { return false }

// Engine 在降级构建里是空壳，Recognize 永远返回 ErrUnavailable。
type Engine struct{}

// New 返回一个不可用的引擎。
func New(dir string, opts Options) *Engine { return &Engine{} }

// Init 永远失败（这个构建里没有引擎）。
func (e *Engine) Init() error { return ErrUnavailable }

// Close 什么也不做。
func (e *Engine) Close() {}

// Recognize 永远失败。
func (e *Engine) Recognize(png []byte) (string, error) { return "", ErrUnavailable }
