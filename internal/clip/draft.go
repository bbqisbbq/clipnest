package clip

import (
	"strings"
	"time"
)

// 快捷输入框的"未保存草稿"。
//
// 为什么要落到库里而不是 localStorage：草稿是用户敲了一半的东西，跟着
// clipnest.db 走才靠得住——那份文件在备份/换机/多端同步里都是主角；WebView
// 的 localStorage 藏在 %APPDATA%\<exe>\EBWebView 底下，清缓存、换安装位置、
// 或者 Wails 改了数据目录就没了，而且用户根本不会想到去那里找。
//
// 两次写入：前端每敲一个字就把正文送到这里（一次 Wails 调用，很便宜），内存里
// 立刻更新；真正的 SQLite 写由这里的 draftFlushDelay 防抖合并。这样做的原因是
// 2.8.1 及以前把防抖放在前端：前端那 400ms 窗口里进程被强杀/断电，最后几个字就
// 丢了，而且退出应用时那次 IPC 未必来得及落盘。现在"丢字"的窗口只剩一次按键的
// 时间，退出/SIGTERM 走 Shutdown 里的显式 flush（见 service.go）。
//
// 存 meta 表而不是新表：只有一条键值，没有查询需求。

const (
	// metaComposerDraft 是草稿正文本身（Markdown，图片以 memoimg: 令牌引用）。
	metaComposerDraft = "composer.draft"
	// metaComposerDraftAt 记录最后一次落盘时间，排障时能看出"草稿是什么时候的"。
	metaComposerDraftAt = "composer.draftAt"

	// maxComposerDraftRunes 是草稿上限。超了只可能是"把整份文档粘进来了"，
	// 截断留着比整条丢掉好；100k 字约等于 Settings.MaxTextLength 的量级。
	maxComposerDraftRunes = 100_000

	// draftFlushDelay 是"最后一次输入 → 落库"的静默期。
	draftFlushDelay = 400 * time.Millisecond
)

// GetComposerDraft returns the last unsaved draft (empty when there is none).
//
// 还挂在内存里没落库的那一份优先：同一进程里的读者（重启前的自检、以后可能的
// 多窗口）不该读到旧值。
func (s *ClipService) GetComposerDraft() string {
	s.draftMu.Lock()
	pending, dirty := s.draftPending, s.draftDirty
	s.draftMu.Unlock()
	if dirty {
		return pending
	}
	return s.store.Meta(metaComposerDraft)
}

// SetComposerDraft records the unsaved draft. An empty (or blank) value clears
// it — that is what saving the entry does, so a stale draft never comes back
// after a restart.
//
// 立即返回：内存更新 + 排一次防抖写盘。应用已经在关闭（Shutdown 已跑过）时直接
// 忽略：那时库可能已经关了，再排定时器只会写到一个关掉的 DB 上。
func (s *ClipService) SetComposerDraft(text string) error {
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return nil
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	s.draftPending = normalizeDraft(text)
	s.draftDirty = true
	if s.draftTimer == nil {
		s.draftTimer = time.AfterFunc(draftFlushDelay, func() { _ = s.flushComposerDraft() })
	} else {
		s.draftTimer.Reset(draftFlushDelay)
	}
	return nil
}

// FlushComposerDraft writes the pending draft right now. 前端在"窗口收起/退出前"
// 调一次，就不必赌防抖窗口还剩多少时间。
func (s *ClipService) FlushComposerDraft() error { return s.flushComposerDraft() }

// flushComposerDraft writes the pending draft to meta (no-op when nothing changed).
func (s *ClipService) flushComposerDraft() error {
	s.draftMu.Lock()
	if !s.draftDirty {
		s.draftMu.Unlock()
		return nil
	}
	text := s.draftPending
	s.draftDirty = false
	s.draftMu.Unlock()

	if strings.TrimSpace(text) == "" {
		if err := s.store.SetMeta(metaComposerDraft, ""); err != nil {
			return err
		}
		return s.store.SetMeta(metaComposerDraftAt, "")
	}
	if err := s.store.SetMeta(metaComposerDraft, text); err != nil {
		return err
	}
	return s.store.SetMeta(metaComposerDraftAt, nowUTC().Format("2006-01-02T15:04:05Z07:00"))
}

// normalizeDraft 统一换行（Vditor 只认 \n）、按上限截断，并把"只有空白"折成空串
// （等价于没有草稿：用户敲了几个空格再关窗口，不该被记住）。
func normalizeDraft(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return truncateRunes(text, maxComposerDraftRunes)
}
