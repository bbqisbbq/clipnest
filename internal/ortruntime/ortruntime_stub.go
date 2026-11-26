//go:build !windows || !cgo

package ortruntime

// 降级实现：不在 Windows 上，或者构建时没开 cgo。
//
// 有它的意义和 ppocr/embed 各自的 stub 一样：让调用方只依赖一个稳定的 API，
// 而不是到处写构建标签。

import "errors"

// ErrUnavailable 表示这个构建没有 onnxruntime。
var ErrUnavailable = errors.New("这个构建没有 onnxruntime（需要 Windows + cgo）")

// Ensure 永远失败：这个构建里没有运行时。
func Ensure(string) error { return ErrUnavailable }

// InitializedBy 在降级构建里恒为空。
func InitializedBy() string { return "" }
