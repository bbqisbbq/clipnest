package clip

// OCR 引擎的选择与调用：默认用 Windows 自带的，用户在设置里可以切到 PP-OCR
// （高精度，但要先下 42MB 资源）。两条路都不通时状态记 unsupported，界面会说明原因。

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/bbqisbbq/clipnest/internal/ppocr"
)

// 引擎名（settings.ocrEngine）。
const (
	// OcrEngineSystem 是 Windows 自带的 Windows.Media.Ocr：零依赖、50~500ms。
	OcrEngineSystem = "system"
	// OcrEnginePPOCR 是 PP-OCRv5 + onnxruntime：准确率高，需要额外资源。
	OcrEnginePPOCR = "ppocr"
)

// ocrEngineState 缓存 PP-OCR 引擎实例（模型加载一次约 0.6 秒，不能每次识别都加载）。
type ocrEngineState struct {
	once   sync.Once
	engine *ppocr.Engine
	dir    string
}

// OcrAssetsDir 返回本机放 OCR 资源（onnxruntime.dll + 模型）的目录：数据目录下的 ocr/。
func (s *ClipService) OcrAssetsDir() string {
	return filepath.Join(s.store.Dir(), "ocr")
}

// ocrEngine 按设置挑引擎并跑一次识别。
//
// 选择逻辑（也是界面上的三条状态）：
//   - 设的是 ppocr 且资源齐全 → PP-OCR；
//   - 设的是 ppocr 但资源不全/这个构建没有 cgo → **退回系统引擎**并记一条日志
//     （宁可识别得糙一点，也别让"开了高精度反而什么都没识别"）；
//   - 系统引擎也没有（非 Windows / 没语言包）→ 返回 errOCRUnavailable。
func (s *ClipService) ocrEngine() (*ppocr.Engine, bool) {
	st := s.store.Settings()
	if st.OcrEngine != OcrEnginePPOCR {
		return nil, false
	}
	s.ocrPPOCR.once.Do(func() {
		dir := s.OcrAssetsDir()
		if err := ppocr.Ready(dir); err != nil {
			Logf(s.store.Dir(), "ocr: ppocr 不可用，退回系统引擎（%v）", err)
			return
		}
		s.ocrPPOCR.engine = ppocr.New(dir, ppocr.DefaultOptions())
		s.ocrPPOCR.dir = dir
		Logf(s.store.Dir(), "ocr: 已启用 PP-OCR（资源目录 %s）", dir)
	})
	if s.ocrPPOCR.engine == nil {
		return nil, false
	}
	return s.ocrPPOCR.engine, true
}

// runOCR 是两条引擎的统一入口：返回识别出来的文字。
func (s *ClipService) runOCR(png []byte) (string, error) {
	if eng, ok := s.ocrEngine(); ok {
		text, err := eng.Recognize(png)
		if err == nil {
			return text, nil
		}
		Logf(s.store.Dir(), "ocr: PP-OCR 失败，本次退回系统引擎（%v）", err)
	}
	return runSystemOCR(png)
}

// OpenOcrAssetsDir 打开 OCR 资源目录（没有就建一个），方便把 42MB 资源放进去。
func (s *ClipService) OpenOcrAssetsDir() error {
	dir := s.OcrAssetsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return revealInExplorer(dir)
}

// OcrEngineStatus 给设置页用的状态：当前选了哪个、PP-OCR 能不能用、为什么不能用。
func (s *ClipService) OcrEngineStatus() (chosen string, ppocrReady bool, reason string) {
	st := s.store.Settings()
	chosen = st.OcrEngine
	dir := s.OcrAssetsDir()
	if err := ppocr.Ready(dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			reason = fmt.Sprintf("资源还没下载（%s）", dir)
		} else {
			reason = err.Error()
		}
		return chosen, false, reason
	}
	// 资源齐全：真的加载一遍，避免"文件在但加载不了"这种情况被界面说成能用。
	eng := ppocr.New(dir, ppocr.DefaultOptions())
	defer eng.Close()
	if err := eng.Init(); err != nil {
		reason = fmt.Sprintf("引擎加载失败：%v", err)
		return chosen, false, reason
	}
	return chosen, true, ""
}
