package clip

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// entries 表的结构与读写：schema、列清单、行 ↔ Entry 映射、保存（含 FTS 索引）、
// 取单条、按 hash 查找、删除，以及「原样合并远端条目」（不重写时间戳）。
// 不负责：查询语言（entry_query.go）、采集入库（entry.go）、附件字节（entry_blob.go）。

// execer 由 *sql.DB 与 *sql.Tx 共同满足：写路径要「条目行 + FTS 行 + 墓碑」在
// 同一个事务里，否则索引失败会留下一条永远搜不到的条目，删除半途失败会留下
// 「本机还在、对端已删」的不一致。
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// ---- schema -------------------------------------------------------------

const entrySchema = `
CREATE TABLE IF NOT EXISTS entries (
  id           TEXT PRIMARY KEY,
  content      TEXT NOT NULL DEFAULT '',
  kind         TEXT NOT NULL DEFAULT 'text',
  source       TEXT NOT NULL DEFAULT 'manual',
  title        TEXT NOT NULL DEFAULT '',
  tags         TEXT NOT NULL DEFAULT '',
  actions      TEXT NOT NULL DEFAULT '',
  auto_tags    TEXT NOT NULL DEFAULT '',
  pinned       INTEGER NOT NULL DEFAULT 0,
  archived     INTEGER NOT NULL DEFAULT 0,

  hash         TEXT NOT NULL DEFAULT '',
  seen_count   INTEGER NOT NULL DEFAULT 0,
  copy_count   INTEGER NOT NULL DEFAULT 0,
  source_app   TEXT NOT NULL DEFAULT '',
  width        INTEGER NOT NULL DEFAULT 0,
  height       INTEGER NOT NULL DEFAULT 0,
  html         TEXT NOT NULL DEFAULT '',
  rtf          TEXT NOT NULL DEFAULT '',
  format_name  TEXT NOT NULL DEFAULT '',
  format_id    INTEGER NOT NULL DEFAULT 0,
  files        TEXT NOT NULL DEFAULT '',

  trigger_key    TEXT NOT NULL DEFAULT '',
  case_sensitive INTEGER NOT NULL DEFAULT 0,
  keep_trigger   INTEGER NOT NULL DEFAULT 0,
  apps           TEXT NOT NULL DEFAULT '',
  exclude_apps   TEXT NOT NULL DEFAULT '',
  group_name     TEXT NOT NULL DEFAULT '',
  use_count      INTEGER NOT NULL DEFAULT 0,
  -- 片段的"按类型执行"语义（text/password/image/file/url/app/command/html）。
  -- 不能塞进 kind：kind 回答的是"正文是什么"，两者不是一一对应（见 snippet.go）。
  snippet_kind   TEXT NOT NULL DEFAULT '',

  expires_at   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  -- 待办派生列（见 todo.go）：正文里任务行的汇总状态，'' = 没有任务行
  todo_state    TEXT NOT NULL DEFAULT '',
  todo_done_at  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_entries_created ON entries(created_at);
-- expander 每次按键都查这个索引，命中的永远是极少数条目
CREATE INDEX IF NOT EXISTS idx_entries_trigger ON entries(trigger_key) WHERE trigger_key <> '';
CREATE INDEX IF NOT EXISTS idx_entries_hash    ON entries(hash) WHERE hash <> '';

-- 统一附件：内容寻址，小图进库、大图落盘
CREATE TABLE IF NOT EXISTS entry_blobs (
  hash    TEXT PRIMARY KEY,
  ext     TEXT NOT NULL DEFAULT 'png',
  mime    TEXT NOT NULL DEFAULT 'image/png',
  data    BLOB,
  storage TEXT NOT NULL DEFAULT 'db',
  size    INTEGER NOT NULL DEFAULT 0,
  path    TEXT NOT NULL DEFAULT ''
);

-- 中文可搜：trigram 分词，子串命中
CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
  id UNINDEXED, content, tags, tokenize='trigram'
);

-- 用户保存的筛选器（自定义入口）
CREATE TABLE IF NOT EXISTS entry_filters (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  icon       TEXT NOT NULL DEFAULT '',
  query      TEXT NOT NULL DEFAULT '{}',
  sort_order INTEGER NOT NULL DEFAULT 0
);
`

const entryColumns = `id, content, kind, source, title, tags, actions, pinned, archived,
	hash, seen_count, copy_count, source_app, width, height, html, rtf, format_name, format_id, files,
	trigger_key, case_sensitive, keep_trigger, apps, exclude_apps, group_name, use_count, snippet_kind,
	expires_at, created_at, updated_at, todo_state, todo_done_at`

// entryPlaceholders must stay in sync with entryColumns (INSERT 的 ? 个数)。
const entryPlaceholders = `?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?`

func scanEntry(sc scannable) (Entry, error) {
	var (
		e                          Entry
		kind, source               string
		tags, actionsSpec          string
		pinned, archived           int
		formatID                   uint32
		files                      string
		caseSensitive, keepTrigger int
		apps, excludeApps          string
		expiresAt                  string
		createdAt, updatedAt       string
		todoState, todoDoneAt      string
	)
	err := sc.Scan(&e.ID, &e.Content, &kind, &source, &e.Title, &tags, &actionsSpec, &pinned, &archived,
		&e.Hash, &e.SeenCount, &e.CopyCount, &e.SourceApp, &e.Width, &e.Height,
		&e.HTML, &e.RTF, &e.FormatName, &formatID, &files,
		&e.TriggerKey, &caseSensitive, &keepTrigger, &apps, &excludeApps, &e.GroupName, &e.UseCount,
		&e.SnippetKind,
		&expiresAt, &createdAt, &updatedAt, &todoState, &todoDoneAt)
	if err != nil {
		return Entry{}, err
	}
	e.Kind = Kind(kind)
	e.Source = Source(source)
	e.Tags = splitTagSpec(tags)
	e.Actions = splitActionSpec(actionsSpec)
	e.Pinned = pinned != 0
	e.Archived = archived != 0
	e.FormatID = formatID
	e.CaseSensitive = caseSensitive != 0
	e.KeepTrigger = keepTrigger != 0
	e.Apps = splitList(apps)
	e.ExcludeApps = splitList(excludeApps)
	e.Files = decodeFiles(files)
	e.CreatedAt = decodeTime(createdAt)
	e.UpdatedAt = decodeTime(updatedAt)
	if expiresAt != "" {
		t := decodeTime(expiresAt)
		e.ExpiresAt = &t
	}
	// 待办：状态读列（写入时维护），计数现算（放内存里，给卡片上的「2/5」）
	e.TodoState = todoState
	if todoDoneAt != "" {
		t := decodeTime(todoDoneAt)
		e.TodoDoneAt = &t
	}
	e.TodoTotal, e.TodoOpen = todoCounts(ParseTodos(e.ID, e.Content))
	return e, nil
}

func decodeFiles(raw string) []FileRef {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []FileRef
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// ---- store --------------------------------------------------------------

// SaveEntry inserts or updates an entry (a real upsert), refreshing tags and the
// FTS index.
func (s *Store) SaveEntry(in Entry) (Entry, error) {
	in.Content = strings.TrimSpace(in.Content)
	in.Title = truncateRunes(strings.TrimSpace(in.Title), 60)
	if in.Source == "" {
		in.Source = SourceManual
	}
	if in.Kind == "" {
		in.Kind = KindText
	}
	if in.Content == "" && in.Kind == KindText {
		return Entry{}, errors.New("内容不能为空")
	}
	// 手写/导入的内容才有 #tag；采集内容保持原样
	if in.Source == SourceManual || in.Source == SourceImport {
		in.Tags = ExtractTags(in.Content)
	}
	now := nowUTC()
	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	if in.Hash == "" && in.Content != "" {
		in.Hash = contentHash(in.Content)
	}
	// 待办派生列由正文算出（不信任调用方传来的值）
	deriveTodo(&in, now)

	tx, err := s.db.Begin()
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = tx.Rollback() }() // 提交后的 Rollback 是空操作
	if err := upsertEntry(tx, in); err != nil {
		return Entry{}, err
	}
	if err := indexEntryTx(tx, in); err != nil {
		// 索引写不进去就整条回滚：宁可这次保存失败，也不要留下一条搜不到的条目。
		return Entry{}, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, err
	}
	return in, nil
}

// upsertEntry writes one row: insert, or update every content-bearing column on
// conflict. Shared by SaveEntry (which stamps updated_at = now) and mergeEntry
// (which preserves the peer's timestamps) so the two can never drift apart.
func upsertEntry(db execer, e Entry) error {
	_, err := db.Exec(`
		INSERT INTO entries (`+entryColumns+`)
		VALUES (`+entryPlaceholders+`)
		ON CONFLICT(id) DO UPDATE SET
			content=excluded.content, kind=excluded.kind, source=excluded.source,
			title=excluded.title,
			tags=excluded.tags, actions=excluded.actions,
			pinned=excluded.pinned, archived=excluded.archived,
			hash=excluded.hash, source_app=excluded.source_app,
			width=excluded.width, height=excluded.height,
			html=excluded.html, rtf=excluded.rtf,
			format_name=excluded.format_name, format_id=excluded.format_id,
			files=excluded.files,
			trigger_key=excluded.trigger_key, case_sensitive=excluded.case_sensitive,
			keep_trigger=excluded.keep_trigger, apps=excluded.apps,
			exclude_apps=excluded.exclude_apps, group_name=excluded.group_name,
			expires_at=excluded.expires_at, updated_at=excluded.updated_at,
			todo_state=excluded.todo_state, todo_done_at=excluded.todo_done_at`,
		e.ID, e.Content, string(e.Kind), string(e.Source), e.Title,
		TagSpec(e.Tags), actionSpec(e.Actions), boolInt(e.Pinned), boolInt(e.Archived),
		e.Hash, e.SeenCount, e.CopyCount, e.SourceApp, e.Width, e.Height,
		e.HTML, e.RTF, e.FormatName, e.FormatID, encodeFiles(e.Files),
		e.TriggerKey, boolInt(e.CaseSensitive), boolInt(e.KeepTrigger),
		joinList(e.Apps), joinList(e.ExcludeApps), e.GroupName, e.UseCount, e.SnippetKind,
		encodeTimePtr(e.ExpiresAt), encodeTime(e.CreatedAt), encodeTime(e.UpdatedAt),
		e.TodoState, encodeTimePtr(e.TodoDoneAt),
	)
	return err
}

func encodeTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return encodeTime(*t)
}

// indexEntryTx rewrites the FTS rows for one entry inside the caller's
// transaction, so the index can never disagree with the row itself.
func indexEntryTx(db execer, e Entry) error {
	if _, err := db.Exec(`DELETE FROM entries_fts WHERE id = ?`, e.ID); err != nil {
		return err
	}
	// 标题也进索引：`@随记@` 这类目标条目要靠名字搜得到。
	fields := strings.Join(e.AllTags(), " ")
	if t := strings.TrimSpace(e.Title); t != "" {
		fields = strings.TrimSpace(fields + " " + t)
	}
	_, err := db.Exec(`INSERT INTO entries_fts (id, content, tags) VALUES (?,?,?)`,
		e.ID, e.Content, fields)
	return err
}

// GetEntry returns one entry.
func (s *Store) GetEntry(id string) (Entry, bool) {
	row := s.db.QueryRow(`SELECT `+entryColumns+` FROM entries WHERE id = ?`, id)
	e, err := scanEntry(row)
	if err != nil {
		return Entry{}, false
	}
	return e, true
}

// findEntryByHash looks an entry up by content hash (used for snippet dedupe).
func (s *Store) findEntryByHash(hash string) (Entry, bool) {
	if hash == "" {
		return Entry{}, false
	}
	row := s.db.QueryRow(`SELECT `+entryColumns+` FROM entries WHERE hash = ? LIMIT 1`, hash)
	e, err := scanEntry(row)
	if err != nil {
		return Entry{}, false
	}
	return e, true
}

// DeleteEntry removes an entry, its index row and a tombstone for peers — all in
// one transaction, in that order. 先删后写墓碑：反过来的话，删除语句失败会留下
// 一条「本机还在、对端已经删掉」的分叉。
func (s *Store) DeleteEntry(id string) bool {
	e, ok := s.GetEntry(id)
	if !ok {
		return false
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM entries WHERE id = ?`, id)
	if err != nil {
		Logf(s.dir, "delete entry %s: %v", id, err)
		return false
	}
	if _, err := tx.Exec(`DELETE FROM entries_fts WHERE id = ?`, id); err != nil {
		Logf(s.dir, "delete entry fts %s: %v", id, err)
		return false
	}
	if e.Hash != "" {
		addTombstoneScope(tx, "entry", e.Hash, nowUTC(), hostName())
	}
	if err := tx.Commit(); err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	// 只有真的带着附件时才去扫全表找孤儿，纯文本删除不必付这个代价。
	if n > 0 && entryBlobKey(e) != "" {
		s.GCBlobs()
	}
	return n > 0
}

// mergeEntry upserts a remote entry EXACTLY as received, preserving its
// timestamps. SaveEntry would stamp updated_at = now, which makes the local copy
// look newer than a peer's tombstone and silently resurrects deleted entries.
func (s *Store) mergeEntry(e Entry) error {
	if e.ID == "" || e.Hash == "" {
		return errors.New("条目缺少 id/hash")
	}
	// 派生列一律本端重算：旧版客户端上传的条目没有 todo_state，
	// 直接覆盖会让本端待办状态凭空消失（见设计文档 1.4）。
	deriveTodo(&e, e.UpdatedAt)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertEntry(tx, e); err != nil {
		return err
	}
	if err := indexEntryTx(tx, e); err != nil {
		return err
	}
	return tx.Commit()
}
