//go:build windows && cgo

package ortruntime_test

// 回归：**两个引擎抢同一个进程级运行时**。
//
// 2.13.2 用户报的现象：设置页写着
//
//	引擎加载失败：onnxruntime 初始化失败: The onnxruntime has already been initialized
//
// 也就是 OCR 说"引擎坏了"。真实原因是顺序：语义搜索（internal/embed）在后台补扫时
// 先起了 onnxruntime 环境，PP-OCR（internal/ppocr）随后再初始化，把它当成了硬错误。
//
// 这个包（ortruntime_test）里没有任何别的东西碰 onnxruntime，所以下面"先 embed 再
// ppocr"的顺序是确定的 —— 正好复刻出事的那条路径。
//
// 资源不全就跳过：这套断言只有在本机装了模型时才有意义。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bbqisbbq/clipnest/internal/embed"
	"github.com/bbqisbbq/clipnest/internal/ortruntime"
	"github.com/bbqisbbq/clipnest/internal/ppocr"
)

func dataDir() string { return filepath.Join(os.Getenv("APPDATA"), "ClipNest") }

func haveAll(paths ...string) bool {
	for _, p := range paths {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

// 这条是真正的回归：embed 先起环境，PP-OCR 仍然必须能初始化。
//
// **它必须排在这个文件里的第一个**：Go 在同一个包里按声明顺序跑测试，而"谁先起
// onnxruntime 环境"正是这个 bug 的全部内容。放在 Ensure 那条之后的话，环境已经被
// ocr 的 dll 起过了，等于没复刻出事顺序 —— 第一版就是这么假绿的。
func TestPPOCRWorksAfterEmbedInitializedRuntime(t *testing.T) {
	embedDir := filepath.Join(dataDir(), "embed")
	ocrDir := filepath.Join(dataDir(), "ocr")
	if !haveAll(filepath.Join(embedDir, "embed_model.onnx"), filepath.Join(embedDir, "embed_vocab.txt")) {
		t.Skip("本机没有语义模型资源")
	}
	if !haveAll(filepath.Join(ocrDir, "det.onnx"), filepath.Join(ocrDir, "rec.onnx"), filepath.Join(ocrDir, "ppocr_keys.txt")) {
		t.Skip("本机没有 PP-OCR 资源")
	}

	// 1. 语义引擎先跑一次 —— 它会把 onnxruntime 环境起来
	em := embed.New(embedDir)
	defer em.Close()
	if _, err := em.Encode("先让语义引擎把 onnxruntime 环境起来"); err != nil {
		t.Fatalf("语义引擎初始化失败（这一步本身不该失败）: %v", err)
	}
	who := ortruntime.InitializedBy()
	t.Logf("onnxruntime 已由 %s 起", who)
	if !strings.Contains(who, string(filepath.Separator)+"embed"+string(filepath.Separator)) {
		t.Fatalf("环境不是 embed 起的（是 %q）—— 这次没复刻到出事顺序，断言不算数", who)
	}

	// 2. PP-OCR 现在再初始化：必须成功。
	//    2.13.2 时这里返回的是 "The onnxruntime has already been initialized"。
	pe := ppocr.New(ocrDir, ppocr.DefaultOptions())
	defer pe.Close()
	if err := pe.Init(); err != nil {
		t.Fatalf("PP-OCR 在语义引擎之后初始化失败（2.13.2 的 bug 又回来了）: %v", err)
	}
}

// Ensure 重复调用必须都是成功（幂等）。
func TestEnsureIsIdempotent(t *testing.T) {
	lib := filepath.Join(dataDir(), "ocr", "onnxruntime.dll")
	if !haveAll(lib) {
		t.Skip("本机没有 onnxruntime.dll")
	}
	for i := 0; i < 3; i++ {
		if err := ortruntime.Ensure(lib); err != nil {
			t.Fatalf("第 %d 次 Ensure 失败: %v", i+1, err)
		}
	}
	if ortruntime.InitializedBy() == "" {
		t.Error("InitializedBy 应该记下第一次用的路径")
	}
}

