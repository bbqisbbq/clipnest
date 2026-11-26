package clip

import (
	"strings"
	"sync"
	"time"
)

// 未保存草稿（两个编辑器各一份）。
//
// 为什么要落到库里而不是 localStorage：草稿是用户敲了一半的东西，跟着
// clipnest.db 走才靠得住——那份文件在备份/换机/多端同步里都是主角；WebView
// 的 localStorage 藏在 %APPDATA%\<exe>\EBWebView 底下，清缓存、换安装位置就没了，
// 而且用户根本不会想到去那里找。
//
// 两次写入：前端每敲一个字就把正文送到这里（一次 IPC，很便宜），内存里立刻更新；
// 真正的 SQLite 写由 draftFlushDelay 防抖合并。这样"丢字"的窗口只剩一次按键的
// 时间，退出/SIGTERM 走 Shutdown 里的显式 flush（见 service.go）。
//
// **为什么是两个槽而不是一个**（2.12.6）：
//
//	composer —— 主窗口底部的「记一条」，窗口一直在，草稿只是跨重启用；
//	capture  —— 快速输入窗「记一笔」，窗口**闲置会被销毁**（idle.go），页面连同
//	            React state 一起没了。以前这个窗口的正文只活在 useState 里，于是
//	            "打了一段字没保存 → 关窗 → 再打开就是空的"（实测日志里
//	            `idle: capture window destroyed` 一次接一次）。
//
// 两者不共用一把键：两个窗口可以同时开着，共用一份会互相覆盖（在 A 里打字，
// B 的输入框跟着变）。

const (
	// metaComposerDraft 是主窗口输入框的草稿正文（Markdown，图片以 memoimg: 令牌引用）。
	metaComposerDraft = "composer.draft"
	// metaComposerDraftAt 记录最后一次落盘时间，排障时能看出"草稿是什么时候的"。
	metaComposerDraftAt = "composer.draftAt"

	// metaCaptureDraft / metaCaptureDraftAt 是「记一笔」浮层的同一组键。
	metaCaptureDraft   = "capture.draft"
	metaCaptureDraftAt = "capture.draftAt"

	// maxComposerDraftRunes 是草稿上限。超了只可能是"把整份文档粘进来了"，
	// 截断留着比整条丢掉好；100k 字约等于 Settings.MaxTextLength 的量级。
	maxComposerDraftRunes = 100_000

	// draftFlushDelay 是"最后一次输入 → 落库"的静默期。
	draftFlushDelay = 400 * time.Millisecond
)

// draftSlot 是一份草稿的防抖落盘状态（内存待写值 + 定时器）。
//
// 每个槽自带一把锁：主窗口和浮层是两个独立的 WebView 进程上下文，谁都可能
// 在对方写盘的那一瞬间来一次输入，共用一个锁只会让它们互相等。
type draftSlot struct {
	// metaKey / atKey 是它在 meta 表里的两把键
	metaKey string
	atKey   string

	mu      sync.Mutex
	pending string
	dirty   bool
	timer   *time.Timer
}

// newDraftSlot 造一个槽（不做任何 IO）。
func newDraftSlot(metaKey, atKey string) *draftSlot {
	return &draftSlot{metaKey: metaKey, atKey: atKey}
}

// get 返回草稿：还挂在内存里没落库的那一份优先。
//
// 同一进程里的读者（界面重新读回来、重启前的自检）不该读到旧值 —— 用户刚敲完
// 就切走，库里可能还落后 400ms。
func (s *ClipService) getDraft(d *draftSlot) string {
	d.mu.Lock()
	pending, dirty := d.pending, d.dirty
	d.mu.Unlock()
	if dirty {
		return pending
	}
	return s.store.Meta(d.metaKey)
}

// setDraft records the unsaved draft. An empty (or blank) value clears it —
// that is what saving the entry does, so a stale draft never comes back.
//
// 立即返回：内存更新 + 排一次防抖写盘。应用已经在关闭（Shutdown 已跑过）时直接
// 忽略：那时库可能已经关了，再排定时器只会写到一个关掉的 DB 上。
func (s *ClipService) setDraft(d *draftSlot, text string) error {
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = normalizeDraft(text)
	d.dirty = true
	if d.timer == nil {
		d.timer = time.AfterFunc(draftFlushDelay, func() { _ = s.flushDraft(d) })
	} else {
		d.timer.Reset(draftFlushDelay)
	}
	return nil
}

// flushDraft writes the pending draft to meta (no-op when nothing changed).
func (s *ClipService) flushDraft(d *draftSlot) error {
	d.mu.Lock()
	if !d.dirty {
		d.mu.Unlock()
		return nil
	}
	text := d.pending
	d.dirty = false
	d.mu.Unlock()

	if strings.TrimSpace(text) == "" {
		if err := s.store.SetMeta(d.metaKey, ""); err != nil {
			return err
		}
		return s.store.SetMeta(d.atKey, "")
	}
	if err := s.store.SetMeta(d.metaKey, text); err != nil {
		return err
	}
	return s.store.SetMeta(d.atKey, nowUTC().Format("2006-01-02T15:04:05Z07:00"))
}

// stopDraftTimer 停掉防抖定时器（Shutdown 用：它必须在 store.Close 之前跑完，
// 否则定时器可能在库关掉之后再触发一次写）。
func stopDraftTimer(d *draftSlot) {
	d.mu.Lock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.mu.Unlock()
}

// ---- 主窗口底部「记一条」-----------------------------------------------

// GetComposerDraft returns the last unsaved draft (empty when there is none).
func (s *ClipService) GetComposerDraft() string { return s.getDraft(s.composerDraft) }

// SetComposerDraft records the unsaved draft ("" clears it).
func (s *ClipService) SetComposerDraft(text string) error { return s.setDraft(s.composerDraft, text) }

// FlushComposerDraft writes the pending draft right now.
func (s *ClipService) FlushComposerDraft() error { return s.flushDraft(s.composerDraft) }

// ---- 快速输入窗「记一笔」------------------------------------------------
//
// 这个窗口闲置会被销毁（idle.go 默认 1.5 秒后 Close），页面一销毁它就什么都不剩，
// 所以草稿是它唯一活下来的可能。

// GetCaptureDraft returns the last unsaved draft of the quick-capture window.
func (s *ClipService) GetCaptureDraft() string { return s.getDraft(s.captureDraft) }

// SetCaptureDraft records the unsaved draft of the quick-capture window.
func (s *ClipService) SetCaptureDraft(text string) error { return s.setDraft(s.captureDraft, text) }

// FlushCaptureDraft writes the pending quick-capture draft right now.
func (s *ClipService) FlushCaptureDraft() error { return s.flushDraft(s.captureDraft) }

// normalizeDraft 统一换行（Vditor 只认 \n）、按上限截断，并把"只有空白"折成空串
// （等价于没有草稿：用户敲了几个空格再关窗口，不该被记住）。
func normalizeDraft(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return truncateRunes(text, maxComposerDraftRunes)
}
