//go:build !windows || !cgo

package embed

// 降级实现：不在 Windows 上，或者构建时没开 cgo。
//
// 存在意义和 internal/ppocr 的 stub 一样：别让可选功能绑死编译链。
// internal/clip 只依赖这个包的公开 API，没有 cgo 也照样编译，只是语义搜索用不了
// （检索自动退回纯 FTS）。

func compiledIn() bool { return false }

// Engine 在降级构建里是空壳。
type Engine struct{ dir string }

// New 返回一个不可用的引擎。
func New(dir string) *Engine { return &Engine{dir: dir} }

// Init 永远失败。
func (e *Engine) Init() error { return ErrUnavailable }

// Encode 永远失败。
func (e *Engine) Encode(text string) (Vector, error) { return Vector{}, ErrUnavailable }

// Close 什么也不做。
func (e *Engine) Close() {}

// Loaded 永远是 false。
func (e *Engine) Loaded() bool { return false }
