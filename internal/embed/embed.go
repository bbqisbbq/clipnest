// Package embed 是可选的「本地语义向量」引擎：把一段文本变成 512 维单位向量，
// 用来给剪贴板历史做"按意思找"（而不是只能按字面）。
//
// 为什么另开一个包（和 internal/ppocr 同一个理由）：它需要 cgo（onnxruntime 的 Go
// 绑定）。放在 internal/clip 里会让整个应用离不开 cgo；单独成包之后
// engine_stub.go 用构建标签提供同签名的降级实现 —— 没有 cgo 的机器照样编译，
// 只是语义搜索用不了（界面退回纯 FTS）。
//
// 模型：BAAI/bge-small-zh-v1.5（ONNX INT8）。选型与实测数字见
// docs/design/SEMANTIC-SEARCH.md，结论摘要：
//
//	单条编码 2.2ms（p90 3.4ms）  1 万条暴力扫描 4.8ms  边际内存 +43MB
//	CLS 池化、不加检索指令；int8 量化对召回无可测影响（只买 4 倍空间）
//
// 资产（都不入库，放数据目录下的 embed/）：
//
//	onnxruntime.dll     ~21MB   和 PP-OCR 共用的那份（有就复制，别下第二遍）
//	embed_model.onnx    ~23MB   bge-small-zh-v1.5 INT8
//	embed_vocab.txt     ~107KB  BERT 词表（21128 行，行号就是 id）
package embed

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 资产文件名（固定：下载器与手动放置都按这套名字找）。
const (
	FileLibrary = "onnxruntime.dll"
	FileModel   = "embed_model.onnx"
	FileVocab   = "embed_vocab.txt"
)

// Dim 是向量维度（bge-small-zh-v1.5 的 hidden_size）。
const Dim = 512

// MaxLen 是喂给模型的最大 token 数（模型的 max_position_embeddings）。
const MaxLen = 512

// ErrUnavailable 表示这个构建里没有引擎（非 Windows 或没开 cgo）。
var ErrUnavailable = errors.New("这个构建不支持语义搜索（需要 Windows + cgo）")

// CompiledIn 报告这个构建是否带上了真引擎。
func CompiledIn() bool { return compiledIn() }

// ErrAssetsMissing 表示引擎代码在，但模型/词表/动态库不全。
type ErrAssetsMissing struct {
	Dir     string
	Missing []string
}

func (e *ErrAssetsMissing) Error() string {
	return fmt.Sprintf("语义搜索资源不全（%s 下缺 %s）", e.Dir, joinNames(e.Missing))
}

// Ready 检查资源目录是否齐全；返回 nil 表示可以尝试初始化。
func Ready(dir string) error {
	if !CompiledIn() {
		return ErrUnavailable
	}
	var missing []string
	for _, name := range []string{FileModel, FileVocab} {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.Size() == 0 {
			missing = append(missing, name)
		}
	}
	if LibraryPath(dir) == "" {
		missing = append(missing, FileLibrary)
	}
	if len(missing) > 0 {
		return &ErrAssetsMissing{Dir: dir, Missing: missing}
	}
	return nil
}

// LibraryPath 找 onnxruntime.dll：优先本目录，其次共享的 OCR 目录（sibling）。
//
// 为什么这样找：onnxruntime 是**进程级共享**的运行时，装两份没有意义，而且
// 22MB 的重复下载很蠢。用户如果已经装了 PP-OCR 资源，这里直接复用那一份。
func LibraryPath(dir string) string {
	if st, err := os.Stat(filepath.Join(dir, FileLibrary)); err == nil && st.Size() > 0 {
		return filepath.Join(dir, FileLibrary)
	}
	shared := filepath.Join(filepath.Dir(dir), "ocr", FileLibrary)
	if st, err := os.Stat(shared); err == nil && st.Size() > 0 {
		return shared
	}
	return ""
}

func joinNames(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "、"
		}
		out += x
	}
	return out
}
