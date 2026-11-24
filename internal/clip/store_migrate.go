package clip

// SQLite 的**迁移与一次性修复**：老库也要能跑到最新 schema。
//
// 为什么单独一个文件：`CREATE TABLE IF NOT EXISTS` 只对新建的库有用，已有表不会长出新列、
// 也不会换主键。所以每个 "老库也要能跑" 的结构变更都必须在这里写一条显式迁移 ——
// 而且必须**有专门的测试造一个老 schema 的库来跑**（`store_migrate_test.go`），
// 新建库的测试永远是最新 schema，抓不到这一类问题（见 HISTORY「ON CONFLICT 的冲突目标」）。

import (
	"database/sql"
	"strings"
)

// ---- schema 迁移 --------------------------------------------------------

// ensureColumn adds a column to an existing table when a build introduces one.
//
// SQLite has no "ADD COLUMN IF NOT EXISTS", so the column list is read first.
// The declaration is code-supplied (never user input), which is why it can be
// spliced into the statement.
func ensureColumn(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    any
			pk      int
		)
		if rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk) != nil {
			continue
		}
		if strings.EqualFold(name, column) {
			found = true
		}
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	return err
}


// ---- 一次性数据修复 ----------------------------------------------------

// clipboardCapabilityBackfillKey marks the one-shot repair below as done.
const clipboardCapabilityBackfillKey = "entries.clipboardCapabilityBackfill"

// backfillClipboardCapability gives collected entries back their
// "出现在快速粘贴列表" capability when the `actions` column lost it.
//
// 为什么要修：快速粘贴窗就是按 `action = clipboard` 取列表的（Picker.tsx），
// 这些行在窗口里根本不出现——用户复制过的东西"在列表里找不到"。实测一台真机上有
// 3 条 `source=clipboard` 却是空 actions 的条目（老模型写进去的行，或曾被整行回写覆盖）。
// 一次性、幂等：规格统一用 actionSpec 重写（`,a,b,` 形式），不手工拼字符串。
func backfillClipboardCapability(db *sql.DB) {
	var done string
	if err := db.QueryRow(`SELECT v FROM meta WHERE k = ?`, clipboardCapabilityBackfillKey).Scan(&done); err == nil && done != "" {
		return
	}
	rows, err := db.Query(`SELECT id, actions FROM entries WHERE source = ?`, string(SourceClipboard))
	if err != nil {
		Logf("", "clipboard capability backfill: scan failed (will retry next start): %v", err)
		return // 不写完成标记：下次启动再试
	}
	type fix struct{ id, spec string }
	var list []fix
	for rows.Next() {
		var id, spec string
		if rows.Scan(&id, &spec) != nil {
			continue
		}
		acts := splitActionSpec(spec)
		has := false
		for _, a := range acts {
			if a == ActionClipboard {
				has = true
				break
			}
		}
		if !has {
			list = append(list, fix{id, actionSpec(append(acts, ActionClipboard))})
		}
	}
	rows.Close()

	tx, err := db.Begin()
	if err != nil {
		Logf("", "clipboard capability backfill: begin failed: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range list {
		if _, err := tx.Exec(`UPDATE entries SET actions = ? WHERE id = ?`, f.spec, f.id); err != nil {
			Logf("", "clipboard capability backfill: update %s failed: %v", f.id, err)
			return
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO meta (k, v) VALUES (?,?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
		clipboardCapabilityBackfillKey, "1"); err != nil {
		Logf("", "clipboard capability backfill: meta write failed: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		Logf("", "clipboard capability backfill: commit failed: %v", err)
		return
	}
	if len(list) > 0 {
		Logf("", "restored clipboard capability on %d entries (they were missing from the quick-paste list)", len(list))
	}
}

// migrateTombstoneSchema rebuilds the tombstones table when it still carries the
// old primary key (hash alone).
//
// 为什么必须迁：写墓碑用的是 `ON CONFLICT(hash, scope)` 的 upsert。老库里主键只有
// `hash`，SQLite 会直接拒绝整条语句（"ON CONFLICT clause does not match any PRIMARY
// KEY or UNIQUE constraint"），而当时错误被 `_, _ =` 吞掉了 —— 于是**删除永远不产生
// 墓碑**：本机删掉的东西不会被对端跟着删，对端旧包里的同一份内容还会被当成"新增"
// 传回来。2026-11-23 在一台真机上就是这么丢的：界面上删了 17 条，`tombstones` 一条
// 都没多，远端包里也没有对应的 deleted 项。
//
// `CREATE TABLE IF NOT EXISTS` 不会改已有表，所以只能显式重建一次；老行的
// `scope = 'item'`（统一模型之前的作用域名）一并改成 `entry`，否则同步侧按
// `scope='entry'` 查不到它们，等于白存。
func migrateTombstoneSchema(db *sql.DB) {
	var ddl string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'tombstones'`).Scan(&ddl); err != nil {
		return // 还没有这张表：coreSchema 会按新定义建出来
	}
	flat := strings.ToLower(strings.Join(strings.Fields(ddl), " "))
	if strings.Contains(flat, "primary key (hash, scope") || strings.Contains(flat, "primary key(hash, scope") ||
		strings.Contains(flat, "unique (hash, scope") {
		return
	}

	steps := []string{
		`ALTER TABLE tombstones RENAME TO tombstones_legacy`,
		`CREATE TABLE tombstones (
			hash       TEXT NOT NULL,
			deleted_at TEXT NOT NULL,
			device     TEXT NOT NULL DEFAULT '',
			scope      TEXT NOT NULL DEFAULT 'entry',
			PRIMARY KEY (hash, scope)
		)`,
		`INSERT OR REPLACE INTO tombstones (hash, deleted_at, device, scope)
		 SELECT hash, deleted_at, device,
		        CASE WHEN scope IS NULL OR scope = '' OR scope = 'item' THEN 'entry' ELSE scope END
		 FROM tombstones_legacy`,
		`DROP TABLE tombstones_legacy`,
		// 索引跟着被 RENAME 走的旧表一起没了，这里补回来（coreSchema 的
		// CREATE INDEX IF NOT EXISTS 此时已经跑过了）。
		`CREATE INDEX IF NOT EXISTS idx_tombstones_scope ON tombstones(scope, deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tomb_deleted ON tombstones(deleted_at)`,
	}

	tx, err := db.Begin()
	if err != nil {
		Logf("", "tombstone schema migration: begin failed: %v", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range steps {
		if _, err := tx.Exec(q); err != nil {
			Logf("", "tombstone schema migration failed (%s): %v", firstSQL(q), err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		Logf("", "tombstone schema migration: commit failed: %v", err)
		return
	}
	Logf("", "tombstone schema migrated: PRIMARY KEY (hash) -> (hash, scope), scope 'item' -> 'entry'")
}

// firstSQL is for migration error messages only.
func firstSQL(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// dropLegacyTables removes the tables the unified model replaced. They are dead
// weight: nothing reads or writes them any more, and leaving them behind makes
// the database (and its backups) carry two schemas.
func dropLegacyTables(db *sql.DB) {
	for _, name := range []string{
		"items", "blobs",
		"memos", "memos_fts", "memos_fts_config", "memos_fts_content",
		"memos_fts_data", "memos_fts_docsize", "memos_fts_idx", "memo_blobs",
		"snippets", "snippet_blobs",
	} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			Logf("", "drop legacy table %s: %v", name, err)
		}
	}
}
