package clip

import (
	"strings"
)

// 快捷输入框的"未保存草稿"。
//
// 为什么要落到库里而不是 localStorage：草稿是用户敲了一半的东西，跟着
// clipnest.db 走才靠得住——那份文件在备份/换机/多端同步里都是主角；WebView
// 的 localStorage 藏在 %APPDATA%\<exe>\EBWebView 底下，清缓存、换安装位置、
// 或者 Wails 改了数据目录就没了，而且用户根本不会想到去那里找。
//
// 写盘由前端防抖（见 hooks/useComposerDraft.ts）：这里不做节流，一次调用一次写。
// 存 meta 表而不是新表：只有一条键值，没有查询需求。

const (
	// metaComposerDraft 是草稿正文本身（Markdown，图片以 memoimg: 令牌引用）。
	metaComposerDraft = "composer.draft"
	// metaComposerDraftAt 记录最后一次落盘时间，排障时能看出"草稿是什么时候的"。
	metaComposerDraftAt = "composer.draftAt"

	// maxComposerDraftRunes 是草稿上限。超了只可能是"把整份文档粘进来了"，
	// 截断留着比整条丢掉好；100k 字约等于 Settings.MaxTextLength 的量级。
	maxComposerDraftRunes = 100_000
)

// GetComposerDraft returns the last unsaved draft (empty when there is none).
func (s *ClipService) GetComposerDraft() string {
	return s.store.Meta(metaComposerDraft)
}

// SetComposerDraft persists the unsaved draft. An empty (or blank) value clears
// it — that is what saving the entry does, so a stale draft never comes back
// after a restart.
func (s *ClipService) SetComposerDraft(text string) error {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = truncateRunes(text, maxComposerDraftRunes)
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
