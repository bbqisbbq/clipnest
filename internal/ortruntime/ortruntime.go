//go:build windows && cgo

// Package ortruntime 只负责一件事：把 onnxruntime 的**进程级环境**起来。
//
// 为什么非要单独一个包：`ort.InitializeEnvironment()` 是**进程级全局**的，一个进程
// 只能成功一次；而我们有两条独立的调用链 —— PP-OCR（internal/ppocr）和语义向量
// （internal/embed）。两个包各自写一份 sync.Once，互相并不知道对方干了什么：
//
//	谁先初始化谁赢，后到的那个拿到 "The onnxruntime has already been initialized"。
//
// 2.13.2 就翻在这儿：embed 在后台补扫时先起了环境，PP-OCR 随后初始化失败，设置页
// 直接显示「引擎加载失败：onnxruntime 初始化失败: The onnxruntime has already been
// initialized」—— 用户看到的是"OCR 坏了"，其实是顺序问题。
//
// 收成一处之后，"已经初始化过"只可能是**我们自己**干的，于是当成功处理；
// 谁先起的也记下来，排障时能看出"这次是 ppocr 还是 embed 起的环境"。
package ortruntime

import (
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	once    sync.Once
	initErr error
	firstBy string
)

// Ensure 保证环境已经初始化（重复调用是空操作，永远返回 nil 当且仅当环境可用）。
//
// libPath 只在**第一次**生效：onnxruntime 的动态库路径是进程级设置，后到的值会被忽略。
// 我们的两份资产（`<data>/ocr/` 与 `<data>/embed/`）是同名同版本的 dll，所以没关系。
func Ensure(libPath string) error {
	once.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		err := ort.InitializeEnvironment()
		if err == nil {
			firstBy = libPath
			return
		}
		// 只可能是"同一个进程里别人先起过"（比如测试里先建了另一个引擎）。
		// 这与"加载失败"是两回事，不能当错误上报 —— 那个错误会让上层以为引擎坏了。
		if isAlreadyInitialized(err) {
			firstBy = "(already initialized)"
			return
		}
		initErr = err
	})
	return initErr
}

// InitializedBy 返回第一次初始化用的是哪个动态库路径（空 = 还没初始化过）。
// 只给日志与排障用。
func InitializedBy() string {
	return firstBy
}

func isAlreadyInitialized(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already initialized") ||
		strings.Contains(msg, "already been initialized")
}
