package clip

// OCR 队列：采集/导入之后把图片丢进队列，后台**一个** worker 串行识别。
//
// 为什么要队列而不是当场识别：一次往返约 1.2 秒，采集路径上绝不能挡（剪贴板监听
// 卡住就等于整个应用卡住）。串行也是为了别在用户复制十张截图时把 CPU 打满。

import (
	"errors"
	"time"
)

var (
	errEntryMissing   = errors.New("条目不存在")
	errOCRUnavailable = errors.New("这台机器没有可用的 OCR 引擎")
)

// ocr_state 的取值（entries.ocr_state）。
const (
	// OcrStatePending 已入队，还没结果。
	OcrStatePending = "pending"
	// OcrStateDone 识别完成（ocr_text 可能是空串：图里就是没有文字）。
	OcrStateDone = "done"
	// OcrStateFailed 试过了但失败（引擎报错）。
	OcrStateFailed = "failed"
	// OcrStateUnsupported 这台机器没有可用引擎（没语言包 / 非 Windows / PowerShell 被禁）。
	OcrStateUnsupported = "unsupported"
	// OcrStateSkipped 不是图片或没有可识别的字节，不必试。
	OcrStateSkipped = "skipped"
)

// ocrQueueSize 是队列长度上限。满了就丢——少认几张图远好过把内存堆起来。
const ocrQueueSize = 64

// startOCRWorker 起后台 worker 并在空闲时补扫历史图片。由 NewClipService 调一次。
func (s *ClipService) startOCRWorker() {
	if !ocrAvailable() {
		Logf(s.store.Dir(), "ocr: no engine on this machine, image entries will not be recognized")
		return
	}
	s.ocrOnce.Do(func() {
		s.ocrQueue = make(chan string, ocrQueueSize)
		s.bg.Add(1)
		go s.ocrWorker()
		go s.backfillOCR()
	})
}

// enqueueOCR asks the worker to recognize one entry (non-blocking).
func (s *ClipService) enqueueOCR(id string) {
	if s.ocrQueue == nil || id == "" {
		return
	}
	select {
	case s.ocrQueue <- id:
	default:
		Logf(s.store.Dir(), "ocr: queue full, skipping entry %s", id)
	}
}

func (s *ClipService) ocrWorker() {
	defer s.bg.Done()
	for {
		select {
		case <-s.bgCtx.Done():
			return
		case id, ok := <-s.ocrQueue:
			if !ok {
				return
			}
			s.runOCRForEntry(id, false)
		}
	}
}

// backfillOCR 启动后把历史图片条目补扫一遍（后台，慢点没关系）。
func (s *ClipService) backfillOCR() {
	// 等一会儿再开工：刚启动时界面还在抢 CPU 与磁盘。
	if !sleepCtx(s.bgCtx, 8*time.Second) {
		return
	}
	ids := s.store.EntryIDsNeedingOCR(200)
	if len(ids) == 0 {
		return
	}
	Logf(s.store.Dir(), "ocr: backfilling %d image entries", len(ids))
	for _, id := range ids {
		if s.bgCtx.Err() != nil {
			return
		}
		s.enqueueOCR(id)
	}
}

// OcrEntry 对单条条目跑一次 OCR（右键菜单/界面按钮用）。
//
// force=false 时已有结果直接返回缓存；force=true 重跑。
func (s *ClipService) OcrEntry(id string, force bool) (string, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return "", errEntryMissing
	}
	if !force && e.OcrState == OcrStateDone {
		return e.OcrText, nil
	}
	return s.runOCRForEntry(id, force)
}

// runOCRForEntry 是队列与手动触发共用的执行体：取字节 → 识别 → 落库（含索引）。
func (s *ClipService) runOCRForEntry(id string, force bool) (string, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return "", errEntryMissing
	}
	if !force && e.OcrState == OcrStateDone {
		return e.OcrText, nil
	}
	key := entryBlobKey(e)
	if key == "" {
		// 不是图片（也不带附件）：标一下就不再来烦它。
		_ = s.store.SetEntryOCR(id, "", OcrStateSkipped)
		return "", nil
	}
	if !ocrAvailable() {
		_ = s.store.SetEntryOCR(id, "", OcrStateUnsupported)
		return "", errOCRUnavailable
	}

	png, _, err := s.store.LoadEntryBlob(key)
	if err != nil || len(png) == 0 {
		_ = s.store.SetEntryOCR(id, "", OcrStateFailed)
		return "", err
	}
	if err := s.store.SetEntryOCR(id, "", OcrStatePending); err != nil {
		return "", err
	}

	text, err := runSystemOCR(png)
	if err != nil {
		state := OcrStateFailed
		if !ocrAvailable() {
			state = OcrStateUnsupported
		}
		_ = s.store.SetEntryOCR(id, "", state)
		Logf(s.store.Dir(), "ocr: entry %s failed: %v", id, err)
		return "", err
	}
	if err := s.store.SetEntryOCR(id, text, OcrStateDone); err != nil {
		return "", err
	}
	// 结果进了搜索索引，界面要能看见新徽章
	s.emit("clip:updated", e)
	s.emit("clip:stats", s.store.Stats())
	return text, nil
}
