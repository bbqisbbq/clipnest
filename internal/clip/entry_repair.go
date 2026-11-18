package clip

import (
	"strings"
)

// 修正与收尾：两件事
//
// 1. 图片条目迁移时只写了「（图片）」占位，没把图片引用写进正文 → 永远显示不出图。
//    这里把 blob 搬进 entry_blobs 并把正文改成 ![](memoimg:<hash>)。
// 2. 采集条目不再自动过期（用户要求「以保留为主，手动删除」）→ 清掉历史 expires_at。

const entriesImageRepairKey = "entries.imageRepair"
const entriesExpiryClearedKey = "entries.expiryCleared"

// RepairEntryImages rewrites image entries so their content actually references
// the stored blob. Idempotent.
func (s *Store) RepairEntryImages() int {
	if s.Meta(entriesImageRepairKey) != "" {
		return 0
	}
	rows, err := s.db.Query(`SELECT id, content FROM entries WHERE kind = 'image'`)
	if err != nil {
		return 0
	}
	type pending struct{ id, content string }
	var list []pending
	for rows.Next() {
		var p pending
		if rows.Scan(&p.id, &p.content) == nil {
			list = append(list, p)
		}
	}
	rows.Close()

	fixed := 0
	for _, p := range list {
		// 已经有图片引用就不用管
		if strings.Contains(p.content, memoImagePrefix) {
			continue
		}
		data, err := s.LoadBlobByID(p.id)
		if err != nil || len(data) == 0 {
			Logf(s.dir, "image entry %s: no blob to link", p.id)
			continue
		}
		token, err := s.SaveEntryBlob(data, s.BlobExt(p.id))
		if err != nil {
			Logf(s.dir, "image entry %s: %v", p.id, err)
			continue
		}
		content := "![](" + token + ")"
		if _, err := s.db.Exec(`UPDATE entries SET content = ? WHERE id = ?`, content, p.id); err != nil {
			continue
		}
		if e, ok := s.GetEntry(p.id); ok {
			_ = s.indexEntry(e)
		}
		fixed++
	}
	_ = s.SetMeta(entriesImageRepairKey, "1")
	if fixed > 0 {
		Logf(s.dir, "repaired %d image entries so they reference their blob", fixed)
	}
	return fixed
}

// ClearEntryExpiry drops every expires_at: collected entries are kept until the
// user deletes them. Nothing in the app prunes entries any more.
func (s *Store) ClearEntryExpiry() int {
	if s.Meta(entriesExpiryClearedKey) != "" {
		return 0
	}
	res, err := s.db.Exec(`UPDATE entries SET expires_at = '' WHERE expires_at <> ''`)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	_ = s.SetMeta(entriesExpiryClearedKey, "1")
	if n > 0 {
		Logf(s.dir, "cleared expiry on %d entries (keep-forever policy)", n)
	}
	return int(n)
}

const entriesActionBackfillKey = "entries.actionBackfill"

// BackfillEntryActions derives the actions column for rows written by the older
// model, which expressed capabilities as auto_tags ("剪贴板"/"片段") and a
// trigger_key. One-shot and idempotent.
func (s *Store) BackfillEntryActions() int {
	if s.Meta(entriesActionBackfillKey) != "" {
		return 0
	}
	// 列还不存在时直接返回，且不写完成标记——否则会留下"已完成"的假状态，
	// 等列补上后回填就再也不会跑了（这个坑真踩过）。
	if !s.hasColumn("entries", "actions") {
		Logf(s.dir, "actions column missing, backfill deferred")
		return 0
	}
	rows, err := s.db.Query(`SELECT id, auto_tags, trigger_key, source FROM entries`)
	if err != nil {
		return 0
	}
	type row struct {
		id, autoTags, trigger, source string
	}
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.autoTags, &r.trigger, &r.source) == nil {
			list = append(list, r)
		}
	}
	rows.Close()

	fixed := 0
	for _, r := range list {
		var actions []string
		if strings.Contains(r.autoTags, "剪贴板") || r.source == string(SourceClipboard) {
			actions = append(actions, ActionClipboard)
		}
		if strings.Contains(r.autoTags, "片段") || strings.TrimSpace(r.trigger) != "" {
			actions = append(actions, ActionSnippet)
		}
		if len(actions) == 0 {
			continue
		}
		if _, err := s.db.Exec(`UPDATE entries SET actions = ? WHERE id = ?`,
			actionSpec(actions), r.id); err != nil {
			continue
		}
		fixed++
	}
	_ = s.SetMeta(entriesActionBackfillKey, "1")
	if fixed > 0 {
		Logf(s.dir, "backfilled actions on %d entries", fixed)
	}
	return fixed
}

// hasColumn reports whether a table already has the given column.
func (s *Store) hasColumn(table, column string) bool {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, typ        string
			dflt             any
		)
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == column {
			return true
		}
	}
	return false
}
