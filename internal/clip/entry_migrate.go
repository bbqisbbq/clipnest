package clip

import (
	"strings"
)

// 把老的三张表（items / memos / snippets）搬进统一的 entries。
//
// 幂等：用 meta 键 entries.migrated 标记，只跑一次；老表原样保留，随时可回滚。
// 顺序很重要：先搬 items（量最大、字段最全），再 memos，最后 snippets
// （snippets 要覆盖同 id 的 auto_tag，所以放最后）。

const entriesMigratedKey = "entries.migrated"

// MigrateToEntries copies legacy rows into entries. Returns how many it added.
func (s *Store) MigrateToEntries() int {
	if s.Meta(entriesMigratedKey) != "" {
		return 0
	}
	added := 0
	added += s.migrateItemsToEntries()
	added += s.migrateMemosToEntries()
	added += s.migrateSnippetsToEntries()
	_ = s.SetMeta(entriesMigratedKey, "1")
	if added > 0 {
		Logf(s.dir, "unified entry migration: %d rows moved into entries", added)
	}
	return added
}

// migrateItemsToEntries: clipboard history → source=clipboard, auto tag 剪贴板,
// expires_at = created + 30 天（沿用原来的保留策略）。
func (s *Store) migrateItemsToEntries() int {
	rows, err := s.db.Query(selectItems)
	if err != nil {
		Logf(s.dir, "entry migration (items) query failed: %v", err)
		return 0
	}
	type legacy struct {
		it     Item
		blob   []byte
		ext    string
		hasRow bool
	}
	var list []legacy
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			break
		}
		list = append(list, legacy{it: it})
	}
	rows.Close()

	added := 0
	for _, l := range list {
		it := l.it
		expires := it.CreatedAt.AddDate(0, 0, 30)
		e := Entry{
			ID:        it.ID,
			Content:   it.Text,
			Kind:      it.Kind,
			Source:    SourceClipboard,
			AutoTags:  []string{AutoTagClipboard},
			Pinned:    it.Pinned,
			Hash:      it.Hash,
			SeenCount: it.SeenCount,
			CopyCount: it.CopyCount,
			SourceApp: it.SourceApp,
			Width:     it.Width,
			Height:    it.Height,
			HTML:      it.HTML,
			RTF:       it.RTF,
			FormatName: it.FormatName,
			FormatID:   it.FormatID,
			Files:      it.Files,
			ExpiresAt:  &expires,
			CreatedAt:  it.CreatedAt,
			UpdatedAt:  it.UpdatedAt,
		}
		// 图片/自定义格式的正文是空的，用一个占位让列表能显示
		if strings.TrimSpace(e.Content) == "" {
			e.Content = placeholderForKind(it.Kind)
		}
		if _, err := s.SaveEntry(e); err != nil {
			Logf(s.dir, "entry migration (item %s): %v", it.ID, err)
			continue
		}
		// 附件搬过去（内容寻址，天然去重）
		if data, err := s.LoadBlobByID(it.ID); err == nil && len(data) > 0 {
			_, _ = s.SaveEntryBlob(data, s.BlobExt(it.ID))
		}
		added++
	}
	return added
}

func placeholderForKind(k Kind) string {
	switch k {
	case KindImage:
		return "（图片）"
	case KindFiles:
		return "（文件）"
	case KindRichText:
		return "（富文本）"
	case KindCustom:
		return "（自定义格式）"
	default:
		return ""
	}
}

// migrateMemosToEntries: 手写备忘 → source=manual，永久（无 expires_at）。
func (s *Store) migrateMemosToEntries() int {
	rows, err := s.db.Query(`SELECT id, content, tags, pinned, archived, source, source_app, hash,
		created_at, updated_at FROM memos`)
	if err != nil {
		Logf(s.dir, "entry migration (memos) query failed: %v", err)
		return 0
	}
	type legacy struct {
		id, content, source, sourceApp, hash string
		pinned, archived                     bool
		createdAt, updatedAt                 string
	}
	var list []legacy
	for rows.Next() {
		var (
			l                    legacy
			tags                 string
			pinned, archived     int
		)
		if rows.Scan(&l.id, &l.content, &tags, &pinned, &archived, &l.source, &l.sourceApp,
			&l.hash, &l.createdAt, &l.updatedAt) != nil {
			break
		}
		l.pinned = pinned != 0
		l.archived = archived != 0
		list = append(list, l)
	}
	rows.Close()

	added := 0
	for _, l := range list {
		e := Entry{
			ID:        l.id,
			Content:   l.content,
			Kind:      KindText,
			Source:    SourceManual,
			Tags:      ExtractTags(l.content),
			Pinned:    l.pinned,
			Archived:  l.archived,
			Hash:      l.hash,
			SourceApp: l.sourceApp,
			CreatedAt: decodeTime(l.createdAt),
			UpdatedAt: decodeTime(l.updatedAt),
		}
		if e.Source == "" || Source(l.source) == SourceImport {
			e.Source = SourceManual
		}
		if _, err := s.SaveEntry(e); err != nil {
			Logf(s.dir, "entry migration (memo %s): %v", l.id, err)
			continue
		}
		added++
	}
	return added
}

// migrateSnippetsToEntries: 片段 → 手写条目 + auto tag 片段 + trigger 字段。
func (s *Store) migrateSnippetsToEntries() int {
	rows, err := s.db.Query(`SELECT id, kind, title, trigger_key, group_name, body, pinned,
		case_sensitive, keep_trigger, apps, exclude_apps, use_count, created_at, updated_at
		FROM snippets`)
	if err != nil {
		Logf(s.dir, "entry migration (snippets) query failed: %v", err)
		return 0
	}
	type legacy struct {
		id, kind, title, triggerKey, group, body string
		apps, excludeApps                        string
		pinned, caseSensitive, keepTrigger       int
		useCount                                 int
		createdAt, updatedAt                     string
	}
	var list []legacy
	for rows.Next() {
		var l legacy
		if rows.Scan(&l.id, &l.kind, &l.title, &l.triggerKey, &l.group, &l.body, &l.pinned,
			&l.caseSensitive, &l.keepTrigger, &l.apps, &l.excludeApps, &l.useCount,
			&l.createdAt, &l.updatedAt) != nil {
			break
		}
		list = append(list, l)
	}
	rows.Close()

	added := 0
	for _, l := range list {
		e := Entry{
			ID:            l.id,
			Content:       l.body,
			Kind:          Kind(l.kind),
			Source:        SourceManual,
			Tags:          ExtractTags(l.body),
			AutoTags:      []string{AutoTagSnippet},
			Pinned:        l.pinned != 0,
			TriggerKey:    l.triggerKey,
			CaseSensitive: l.caseSensitive != 0,
			KeepTrigger:   l.keepTrigger != 0,
			Apps:          splitList(l.apps),
			ExcludeApps:   splitList(l.excludeApps),
			GroupName:     l.group,
			UseCount:      l.useCount,
			CreatedAt:     decodeTime(l.createdAt),
			UpdatedAt:     decodeTime(l.updatedAt),
		}
		if _, err := s.SaveEntry(e); err != nil {
			Logf(s.dir, "entry migration (snippet %s): %v", l.id, err)
			continue
		}
		added++
	}
	return added
}
