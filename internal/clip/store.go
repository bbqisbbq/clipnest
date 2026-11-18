package clip

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)
)

// Store is the clipboard history, persisted in a single SQLite database.
//
//	%APPDATA%\ClipNest\
//	  clipnest.db      items + blobs (WAL mode)
//	  settings.json    capture/UI settings (kept as JSON: tiny, human-editable)
//	  clipnest.log     diagnostics
//
// SQLite replaced the earlier "one JSONL index + blob directory" layout: searches
// are indexed, writes are transactional, and an interrupted process can no longer
// leave a half-written index behind. Blobs (images, unknown formats) live in the
// same database, so backing up the history is a single file copy.
type Store struct {
	db           *sql.DB
	dir          string
	settingsPath string

	mu     sync.RWMutex // guards settings + closed
	settings Settings
	closed   bool
}

const dbFileName = "clipnest.db"

const schemaSQL = `
CREATE TABLE IF NOT EXISTS items (
  id           TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  text         TEXT NOT NULL DEFAULT '',
  preview      TEXT NOT NULL DEFAULT '',
  hash         TEXT NOT NULL,
  size         INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  seen_count   INTEGER NOT NULL DEFAULT 1,
  copy_count   INTEGER NOT NULL DEFAULT 0,
  pinned       INTEGER NOT NULL DEFAULT 0,
  source_app   TEXT NOT NULL DEFAULT '',
  width        INTEGER NOT NULL DEFAULT 0,
  height       INTEGER NOT NULL DEFAULT 0,
  html         TEXT NOT NULL DEFAULT '',
  rtf          TEXT NOT NULL DEFAULT '',
  format_name  TEXT NOT NULL DEFAULT '',
  format_id    INTEGER NOT NULL DEFAULT 0,
  files        TEXT NOT NULL DEFAULT '[]'
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_items_hash ON items(hash);
CREATE INDEX IF NOT EXISTS idx_items_updated ON items(pinned DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_items_kind ON items(kind);

CREATE TABLE IF NOT EXISTS blobs (
  item_id TEXT PRIMARY KEY,
  ext     TEXT NOT NULL,
  data    BLOB NOT NULL
);

-- Tombstones record user-initiated deletions so they can propagate through sync.
-- Retention pruning deliberately does NOT write here: one machine's retention
-- policy must never delete another machine's history.
CREATE TABLE IF NOT EXISTS tombstones (
  hash       TEXT PRIMARY KEY,
  deleted_at TEXT NOT NULL,
  device     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tomb_deleted ON tombstones(deleted_at);

-- User-authored snippets ("短语"): reusable text, images, paths, links,
-- launchers and commands, optionally with an inline trigger.
CREATE TABLE IF NOT EXISTS snippets (
  id           TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  title        TEXT NOT NULL DEFAULT '',
  trigger_key  TEXT NOT NULL DEFAULT '',
  group_name   TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL DEFAULT '',
  hash         TEXT NOT NULL DEFAULT '',
  size         INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  use_count    INTEGER NOT NULL DEFAULT 0,
  pinned       INTEGER NOT NULL DEFAULT 0,
  case_sensitive INTEGER NOT NULL DEFAULT 0,
  keep_trigger   INTEGER NOT NULL DEFAULT 0,
  apps           TEXT NOT NULL DEFAULT '',
  exclude_apps   TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_snippets_hash ON snippets(hash);
CREATE INDEX IF NOT EXISTS idx_snippets_trigger ON snippets(trigger_key);

CREATE TABLE IF NOT EXISTS snippet_blobs (
  snippet_id TEXT PRIMARY KEY,
  ext        TEXT NOT NULL,
  data       BLOB NOT NULL
);

-- 备忘（Memos 形态的碎片笔记）：正文是 Markdown，标签从正文 #tag 派生
CREATE TABLE IF NOT EXISTS memos (
  id         TEXT PRIMARY KEY,
  content    TEXT NOT NULL,
  tags       TEXT NOT NULL DEFAULT '',
  pinned     INTEGER NOT NULL DEFAULT 0,
  archived   INTEGER NOT NULL DEFAULT 0,
  source     TEXT NOT NULL DEFAULT 'manual',
  source_app TEXT NOT NULL DEFAULT '',
  hash       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memos_updated ON memos(archived, pinned, updated_at DESC);

-- 中文可搜：trigram 分词，支持子串命中（SQLite 内置 FTS5）
CREATE VIRTUAL TABLE IF NOT EXISTS memos_fts USING fts5(
  id UNINDEXED, content, tags, tokenize='trigram'
);

-- 备忘的图片附件（内容寻址：hash = sha256(data)[:24]）
CREATE TABLE IF NOT EXISTS memo_blobs (
  hash    TEXT PRIMARY KEY,
  ext     TEXT NOT NULL DEFAULT 'png',
  mime    TEXT NOT NULL DEFAULT 'image/png',
  data    BLOB,
  storage TEXT NOT NULL DEFAULT 'db',   -- db = 小图进库；file = 大图落盘
  size    INTEGER NOT NULL DEFAULT 0,
  path    TEXT NOT NULL DEFAULT ''      -- 相对 dataDir（storage=file 时有值）
);

-- Small key/value scratch space (sync fingerprints and other one-off flags).
CREATE TABLE IF NOT EXISTS meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
`

// Open opens (or creates and migrates) the database inside dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, dbFileName)) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite serialises writers anyway; one connection avoids lock churn
	// between the capture goroutine and UI queries.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库失败: %w", err)
	}
	// 统一条目模型（entries / entry_blobs / entries_fts / entry_filters）
	if _, err := db.Exec(entrySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化统一条目表失败: %w", err)
	}
	// Older databases predate the snippet tombstones: add the scope column in
	// place. SQLite has no "ADD COLUMN IF NOT EXISTS", so the error is expected.
	_, _ = db.Exec(`ALTER TABLE tombstones ADD COLUMN scope TEXT NOT NULL DEFAULT 'item'`)
	// 备忘附件（1.5.0 起支持大文件落盘）：老库补齐列
	for _, stmt := range []string{
		`ALTER TABLE memo_blobs ADD COLUMN storage TEXT NOT NULL DEFAULT 'db'`,
		`ALTER TABLE memo_blobs ADD COLUMN size INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE memo_blobs ADD COLUMN path TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = db.Exec(stmt)
	}
	// 片段触发选项（0.9.x 起）：老库补齐列
	for _, stmt := range []string{
		`ALTER TABLE snippets ADD COLUMN case_sensitive INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE snippets ADD COLUMN keep_trigger INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE snippets ADD COLUMN apps TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE snippets ADD COLUMN exclude_apps TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = db.Exec(stmt)
	}

	normalizeTimestamps(db)

	logDirValue.Store(dir)
	s := &Store{
		db:           db,
		dir:          dir,
		settingsPath: filepath.Join(dir, "settings.json"),
		settings:     DefaultSettings(),
	}
	if err := s.loadSettings(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateLegacyJSONL(); err != nil {
		// A failed migration must not brick the app; the old files stay intact.
		Logf(dir, "migrate legacy history failed: %v", err)
	}
	return s, nil
}

// migrateLegacyJSONL imports an items.jsonl + blobs/ directory produced by
// ClipNest <= 0.1.1, then renames the old file so the import runs only once.
func (s *Store) migrateLegacyJSONL() error {
	legacy := filepath.Join(s.dir, "items.jsonl")
	data, err := os.ReadFile(legacy)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var existing int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil // already have data; leave the legacy file alone
	}

	imported, withBlob := 0, 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var it Item
		if err := json.Unmarshal([]byte(line), &it); err != nil || it.ID == "" {
			continue
		}
		blobName := it.ImageFile
		if blobName == "" {
			blobName = it.BinaryFile
		}
		var payload []byte
		blobExt := ""
		if blobName != "" {
			blobExt = strings.ToLower(filepath.Ext(blobName))
			if blobExt == "" {
				blobExt = ".bin"
			}
			if b, err := os.ReadFile(filepath.Join(s.dir, "blobs", filepath.Base(blobName))); err == nil {
				payload = b
			}
		}

		cap := Captured{
			Kind:       it.Kind,
			Text:       it.Text,
			HTML:       it.HTML,
			RTF:        it.RTF,
			Width:      it.Width,
			Height:     it.Height,
			Files:      it.Files,
			Hash:       it.Hash,
			Preview:    it.Preview,
			Size:       it.Size,
			SourceApp:  it.SourceApp,
			FormatName: it.FormatName,
			FormatID:   it.FormatID,
		}
		switch blobExt {
		case ".png":
			cap.PNG = payload
		case ".bin":
			cap.Binary = payload
		}
		if payload != nil {
			withBlob++
		}
		added, isNew, err := s.Add(cap)
		if err == nil && isNew && added != nil {
			imported++
			// Preserve the original timestamps and counters.
			_, _ = s.db.Exec(
				`UPDATE items SET created_at = ?, updated_at = ?, seen_count = ?, copy_count = ?, pinned = ? WHERE id = ?`,
				encodeTime(it.CreatedAt), encodeTime(it.UpdatedAt),
				maxInt(it.SeenCount, 1), it.CopyCount, boolInt(it.Pinned), added.ID)
		}
	}

	_ = os.Rename(legacy, legacy+".migrated")
	Logf(s.dir, "migrated %d legacy items (%d with blobs) from items.jsonl", imported, withBlob)
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- settings -----------------------------------------------------------

func (s *Store) loadSettings() error {
	data, err := os.ReadFile(s.settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	st := DefaultSettings()
	if err := json.Unmarshal(data, &st); err != nil {
		return nil // corrupt file → keep defaults
	}
	s.settings = st
	return nil
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.clone()
}

// SaveSettings persists settings synchronously (JSON next to the database).
// Any change to the *shareable* fields bumps SyncUpdatedAt, which is how peers
// decide whose copy of the settings is newer.
func (s *Store) SaveSettings(st Settings) error {
	st = st.clone()
	s.mu.Lock()
	if shareableStamp(s.settings) != shareableStamp(st) {
		st.SyncUpdatedAt = nowUTC()
	}
	s.settings = st
	s.mu.Unlock()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.settingsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.settingsPath)
}

// PeekSettings reads settings.json without opening the database (used before
// the window is created, to decide whether to start hidden).
func PeekSettings(dir string) (Settings, error) {
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return DefaultSettings(), err
	}
	st := DefaultSettings()
	if err := json.Unmarshal(data, &st); err != nil {
		return DefaultSettings(), err
	}
	return st, nil
}

// ---- lifecycle ----------------------------------------------------------

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }

// Close checkpoints the WAL and closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// Fold the WAL back in so a copied clipnest.db is self-contained.
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return s.db.Close()
}

// ---- items --------------------------------------------------------------

// statFile fills a FileRef from disk. Files are stored by reference (path +
// size + existence) rather than copied, so the history never duplicates data.
func statFile(ref FileRef) FileRef {
	if ref.Path == "" {
		return ref
	}
	ref.Name = filepath.Base(ref.Path)
	info, err := os.Stat(ref.Path)
	if err != nil {
		ref.Exists = false
		ref.Size = 0
		ref.IsDir = false
		return ref
	}
	ref.Exists = true
	ref.IsDir = info.IsDir()
	ref.Size = info.Size()
	return ref
}

// timeLayout is a FIXED-WIDTH RFC3339 form: always 9 fractional digits.
//
// Why fixed width matters: every timestamp is stored as TEXT and ordered by
// SQLite as a string. RFC3339Nano trims trailing zeros, so ".5Z" sorts AFTER
// ".1234567Z" — ordering and cursor pagination silently break. A fixed-width
// layout makes lexicographic order identical to chronological order, which also
// keeps the (created_at) indexes usable.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func encodeTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func decodeTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b[:])
}


func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Add inserts a capture, or bumps an existing entry with the same content hash.
// The bool reports whether the entry is new to the history.
func (s *Store) Add(c Captured) (*Item, bool, error) {
	now := nowUTC()

	var existingID string
	err := s.db.QueryRow(`SELECT id FROM items WHERE hash = ?`, c.Hash).Scan(&existingID)
	if err == nil && existingID != "" {
		if _, err := s.db.Exec(
			`UPDATE items SET updated_at = ?, seen_count = seen_count + 1,
			        source_app = CASE WHEN ? <> '' THEN ? ELSE source_app END
			 WHERE id = ?`,
			encodeTime(now), c.SourceApp, c.SourceApp, existingID,
		); err != nil {
			return nil, false, err
		}
		it, ok := s.Get(existingID)
		if !ok {
			return nil, false, errors.New("条目读取失败")
		}
		return &it, false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	id := newID()
	it := Item{
		ID:         id,
		Kind:       c.Kind,
		Text:       c.Text,
		Preview:    c.Preview,
		Hash:       c.Hash,
		Size:       c.Size,
		CreatedAt:  now,
		UpdatedAt:  now,
		SeenCount:  1,
		SourceApp:  c.SourceApp,
		Width:      c.Width,
		Height:     c.Height,
		HTML:       c.HTML,
		RTF:        c.RTF,
		FormatName: c.FormatName,
		FormatID:   c.FormatID,
		Files:      c.Files,
	}
	if len(c.PNG) > 0 {
		it.ImageFile = id + ".png"
	}
	if len(c.Binary) > 0 {
		it.BinaryFile = id + ".bin"
	}

	if _, err := s.db.Exec(`
		INSERT INTO items (id, kind, text, preview, hash, size, created_at, updated_at,
		                   seen_count, copy_count, pinned, source_app, width, height,
		                   html, rtf, format_name, format_id, files)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, string(it.Kind), it.Text, it.Preview, it.Hash, it.Size,
		encodeTime(it.CreatedAt), encodeTime(it.UpdatedAt),
		it.SeenCount, it.CopyCount, boolInt(it.Pinned), it.SourceApp,
		it.Width, it.Height, it.HTML, it.RTF, it.FormatName, it.FormatID,
		encodeFiles(it.Files),
	); err != nil {
		return nil, false, err
	}

	if len(c.PNG) > 0 {
		if err := s.SaveBlob(it.ID, ".png", c.PNG); err != nil {
			return nil, false, err
		}
	}
	if len(c.Binary) > 0 {
		if err := s.SaveBlob(it.ID, ".bin", c.Binary); err != nil {
			return nil, false, err
		}
	}
	return &it, true, nil
}

func encodeFiles(files []FileRef) string {
	if len(files) == 0 {
		return "[]"
	}
	b, err := json.Marshal(files)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// itemColumns must stay in sync with scanItem. The blob extension comes along in
// the same row: querying it per item would deadlock, because the store uses a
// single connection and the outer rows are still open.
const itemColumns = `i.id, i.kind, i.text, i.preview, i.hash, i.size, i.created_at, i.updated_at,
	i.seen_count, i.copy_count, i.pinned, i.source_app, i.width, i.height, i.html, i.rtf,
	i.format_name, i.format_id, i.files, COALESCE(b.ext, '')`

const selectItems = `SELECT ` + itemColumns + ` FROM items i LEFT JOIN blobs b ON b.item_id = i.id`

type scannable interface {
	Scan(dest ...any) error
}

func scanItem(sc scannable) (Item, error) {
	var (
		it                   Item
		kind                 string
		createdAt, updatedAt string
		pinned               int
		filesJSON            string
		blobExt              string
	)
	err := sc.Scan(
		&it.ID, &kind, &it.Text, &it.Preview, &it.Hash, &it.Size,
		&createdAt, &updatedAt, &it.SeenCount, &it.CopyCount, &pinned,
		&it.SourceApp, &it.Width, &it.Height, &it.HTML, &it.RTF,
		&it.FormatName, &it.FormatID, &filesJSON, &blobExt,
	)
	if err != nil {
		return Item{}, err
	}
	it.Kind = Kind(kind)
	it.CreatedAt = decodeTime(createdAt)
	it.UpdatedAt = decodeTime(updatedAt)
	it.Pinned = pinned != 0
	if filesJSON != "" && filesJSON != "[]" {
		_ = json.Unmarshal([]byte(filesJSON), &it.Files)
	}
	switch blobExt {
	case "":
	case ".png":
		it.ImageFile = it.ID + blobExt
	default:
		it.BinaryFile = it.ID + blobExt
	}
	return it, nil
}

// Get returns one entry.
func (s *Store) Get(id string) (Item, bool) {
	row := s.db.QueryRow(selectItems+` WHERE i.id = ?`, id)
	it, err := scanItem(row)
	if err != nil {
		return Item{}, false
	}
	return it, true
}

func (s *Store) queryItems(query string, args ...any) ([]Item, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Item, 0, 64)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// List returns entries matching the filter, pinned first then newest first.
func (s *Store) List(f Filter) []Item {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	where := make([]string, 0, 3)
	args := make([]any, 0, 5)

	if f.Pinned {
		where = append(where, "i.pinned = 1")
	}
	if f.Kind != "" && f.Kind != "all" {
		where = append(where, "i.kind = ?")
		args = append(args, f.Kind)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + escapeLike(strings.ToLower(q)) + "%"
		where = append(where,
			`(lower(i.text) LIKE ? ESCAPE '\' OR lower(i.preview) LIKE ? ESCAPE '\'`+
				` OR lower(i.source_app) LIKE ? ESCAPE '\' OR lower(i.files) LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like)
	}

	query := selectItems
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY i.pinned DESC, i.updated_at DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	items, err := s.queryItems(query, args...)
	if err != nil {
		Logf(s.dir, "list query failed: %v", err)
		return nil
	}
	return items
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// All returns every entry, pinned first then newest first.
func (s *Store) All() []Item {
	items, err := s.queryItems(selectItems + ` ORDER BY i.pinned DESC, i.updated_at DESC`)
	if err != nil {
		Logf(s.dir, "all query failed: %v", err)
		return nil
	}
	return items
}

// Count returns how many entries match the filter.
func (s *Store) Count(f Filter) int {
	f.Limit = 5000
	return len(s.List(f))
}

// Delete removes one entry (its blob goes with it).
func (s *Store) Delete(id string) bool {
	return s.deleteRow(id, true)
}

// deleteRow removes an entry and (optionally) leaves a tombstone so peers drop
// their copy too. Retention pruning passes false: a full history on another
// machine is not a reason to erase it there.
func (s *Store) deleteRow(id string, tombstone bool) bool {
	var hash string
	_ = s.db.QueryRow(`SELECT hash FROM items WHERE id = ?`, id).Scan(&hash)

	res, err := s.db.Exec(`DELETE FROM items WHERE id = ?`, id)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	_, _ = s.db.Exec(`DELETE FROM blobs WHERE item_id = ?`, id)
	if tombstone && hash != "" {
		s.AddTombstone(hash, nowUTC(), hostName())
	}
	return true
}

// DeleteWhere removes every non-pinned entry matching the filter.
func (s *Store) DeleteWhere(f Filter) int {
	victims := s.List(f)
	n := 0
	for _, it := range victims {
		if it.Pinned {
			continue
		}
		if s.Delete(it.ID) {
			n++
		}
	}
	return n
}

// ---- meta ---------------------------------------------------------------

// Meta reads a small persisted value ("" when absent).
func (s *Store) Meta(k string) string {
	var v string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = ?`, k).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetMeta writes a small persisted value.
func (s *Store) SetMeta(k, v string) error {
	_, err := s.db.Exec(
		`INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// ---- tombstones ---------------------------------------------------------

// Tombstone is a deletion marker that travels with sync.
type Tombstone struct {
	Hash      string    `json:"hash"`
	DeletedAt time.Time `json:"deletedAt"`
	Device    string    `json:"device,omitempty"`
}

// AddTombstoneScoped records a deletion marker for an item or a snippet.
func (s *Store) AddTombstoneScoped(scope, hash string, at time.Time, device string) {
	if hash == "" {
		return
	}
	if scope == "" {
		scope = "item"
	}
	_, _ = s.db.Exec(
		`INSERT INTO tombstones (hash, deleted_at, device, scope) VALUES (?,?,?,?)
		 ON CONFLICT(hash) DO UPDATE SET deleted_at = MAX(deleted_at, excluded.deleted_at)`,
		hash, encodeTime(at), device, scope,
	)
}

// TombstonesScoped returns deletion markers of one scope.
func (s *Store) TombstonesScoped(scope string, since time.Time, limit int) []Tombstone {
	if limit <= 0 || limit > 10000 {
		limit = 2000
	}
	rows, err := s.db.Query(
		`SELECT hash, deleted_at, device FROM tombstones
		 WHERE scope = ? AND deleted_at >= ? ORDER BY deleted_at DESC LIMIT ?`,
		scope, encodeTime(since), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Tombstone
	for rows.Next() {
		var t Tombstone
		var at string
		if rows.Scan(&t.Hash, &at, &t.Device) == nil {
			t.DeletedAt = decodeTime(at)
			out = append(out, t)
		}
	}
	return out
}

// DeleteSnippetByHash drops a snippet because a peer deleted it.
func (s *Store) DeleteSnippetByHash(hash string) bool {
	var id string
	if err := s.db.QueryRow(`SELECT id FROM snippets WHERE hash = ?`, hash).Scan(&id); err != nil {
		return false
	}
	res, err := s.db.Exec(`DELETE FROM snippets WHERE id = ?`, id)
	if err != nil {
		return false
	}
	_, _ = s.db.Exec(`DELETE FROM snippet_blobs WHERE snippet_id = ?`, id)
	n, _ := res.RowsAffected()
	return n > 0
}

// AddTombstone records (or refreshes) a deletion marker.
func (s *Store) AddTombstone(hash string, at time.Time, device string) {
	if hash == "" {
		return
	}
	_, _ = s.db.Exec(
		`INSERT INTO tombstones (hash, deleted_at, device) VALUES (?,?,?)
		 ON CONFLICT(hash) DO UPDATE SET deleted_at = MAX(deleted_at, excluded.deleted_at)`,
		hash, encodeTime(at), device,
	)
}

// Tombstones returns deletion markers newer than since, newest first.
func (s *Store) Tombstones(since time.Time, limit int) []Tombstone {
	if limit <= 0 || limit > 10000 {
		limit = 2000
	}
	rows, err := s.db.Query(
		`SELECT hash, deleted_at, device FROM tombstones WHERE deleted_at >= ? ORDER BY deleted_at DESC LIMIT ?`,
		encodeTime(since), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Tombstone
	for rows.Next() {
		var t Tombstone
		var at string
		if rows.Scan(&t.Hash, &at, &t.Device) == nil {
			t.DeletedAt = decodeTime(at)
			out = append(out, t)
		}
	}
	return out
}

// PruneTombstones drops markers older than the retention horizon.
func (s *Store) PruneTombstones(before time.Time) int {
	res, err := s.db.Exec(`DELETE FROM tombstones WHERE deleted_at < ?`, encodeTime(before))
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

func hostName() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "windows"
}

// SetPinned toggles the favourite flag.
func (s *Store) SetPinned(id string, pinned bool) (Item, bool) {
	if _, err := s.db.Exec(`UPDATE items SET pinned = ? WHERE id = ?`, boolInt(pinned), id); err != nil {
		return Item{}, false
	}
	return s.Get(id)
}

// MarkCopied records a copy-back so frequent entries can be ranked.
func (s *Store) MarkCopied(id string) {
	_, _ = s.db.Exec(`UPDATE items SET copy_count = copy_count + 1, updated_at = ? WHERE id = ?`,
		encodeTime(nowUTC()), id)
}

// RefreshFiles re-stats every file reference of an entry (the "reference"
// strategy goes stale when the original is moved or deleted).
func (s *Store) RefreshFiles(id string) (Item, bool) {
	it, ok := s.Get(id)
	if !ok {
		return Item{}, false
	}
	for i := range it.Files {
		it.Files[i] = statFile(it.Files[i])
	}
	_, _ = s.db.Exec(`UPDATE items SET files = ? WHERE id = ?`, encodeFiles(it.Files), id)
	return it, true
}

// UpsertRemote merges one entry coming from a sync peer (matched by content
// hash). Newest body wins; favourite state and counters never regress.
func (s *Store) UpsertRemote(in Item) (isNew bool, changed bool) {
	local, ok := s.findByHash(in.Hash)
	if ok {
		if in.UpdatedAt.After(local.UpdatedAt) {
			if _, err := s.db.Exec(`
				UPDATE items SET text=?, preview=?, html=?, rtf=?, files=?, format_name=?,
				                 format_id=?, width=?, height=?, size=?, updated_at=?,
				                 source_app = CASE WHEN ? <> '' THEN ? ELSE source_app END
				 WHERE id = ?`,
				in.Text, in.Preview, in.HTML, in.RTF, encodeFiles(in.Files), in.FormatName, in.FormatID,
				in.Width, in.Height, in.Size, encodeTime(in.UpdatedAt), in.SourceApp, in.SourceApp,
				local.ID,
			); err != nil {
				return false, false
			}
			changed = true
		}
		if in.Pinned && !local.Pinned {
			_, _ = s.db.Exec(`UPDATE items SET pinned = 1 WHERE id = ?`, local.ID)
			changed = true
		}
		if in.SeenCount > local.SeenCount {
			_, _ = s.db.Exec(`UPDATE items SET seen_count = ? WHERE id = ?`, in.SeenCount, local.ID)
			changed = true
		}
		if in.CopyCount > local.CopyCount {
			_, _ = s.db.Exec(`UPDATE items SET copy_count = ? WHERE id = ?`, in.CopyCount, local.ID)
			changed = true
		}
		return false, changed
	}

	it := in
	if it.ID == "" {
		it.ID = newID()
	}
	if it.CreatedAt.IsZero() {
		it.CreatedAt = nowUTC()
	}
	if it.UpdatedAt.IsZero() {
		it.UpdatedAt = nowUTC()
	}
	if it.SeenCount == 0 {
		it.SeenCount = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO items (id, kind, text, preview, hash, size, created_at, updated_at,
		                   seen_count, copy_count, pinned, source_app, width, height,
		                   html, rtf, format_name, format_id, files)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, string(it.Kind), it.Text, it.Preview, it.Hash, it.Size,
		encodeTime(it.CreatedAt), encodeTime(it.UpdatedAt),
		it.SeenCount, it.CopyCount, boolInt(it.Pinned), it.SourceApp,
		it.Width, it.Height, it.HTML, it.RTF, it.FormatName, it.FormatID,
		encodeFiles(it.Files),
	)
	if err != nil {
		return false, false // duplicate id from a peer: nothing to do
	}
	return true, true
}

// UnionFlags merges the never-regressing fields (star, counters) for an entry
// that a peer already has. Reports whether anything actually changed.
func (s *Store) UnionFlags(hash string, pinned bool, seen, copies int) bool {
	if hash == "" {
		return false
	}
	res, err := s.db.Exec(
		`UPDATE items SET pinned = MAX(pinned, ?), seen_count = MAX(seen_count, ?),
		                  copy_count = MAX(copy_count, ?) WHERE hash = ?`,
		boolInt(pinned), seen, copies, hash,
	)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	var nowPinned bool
	var nowSeen, nowCopies int
	if err := s.db.QueryRow(
		`SELECT pinned, seen_count, copy_count FROM items WHERE hash = ?`, hash,
	).Scan(&nowPinned, &nowSeen, &nowCopies); err != nil {
		return true
	}
	return true
}

func (s *Store) findByHash(hash string) (Item, bool) {
	if hash == "" {
		return Item{}, false
	}
	row := s.db.QueryRow(selectItems+` WHERE i.hash = ?`, hash)
	it, err := scanItem(row)
	if err != nil {
		return Item{}, false
	}
	return it, true
}

// Prune enforces the retention policy (pinned entries survive).
func (s *Store) Prune(maxItems, maxAgeDays int) int {
	removed := 0
	if maxAgeDays > 0 {
		cutoff := encodeTime(nowUTC().AddDate(0, 0, -maxAgeDays))
		for _, id := range s.collectIDs(`SELECT id FROM items WHERE pinned = 0 AND updated_at < ?`, cutoff) {
			if s.deleteRow(id, false) {
				removed++
			}
		}
	}
	if maxItems > 0 {
		var total int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&total); err == nil && total > maxItems {
			for _, id := range s.collectIDs(
				`SELECT id FROM items WHERE pinned = 0 ORDER BY updated_at ASC LIMIT ?`, total-maxItems) {
				if s.deleteRow(id, false) {
					removed++
				}
			}
		}
	}
	return removed
}

func (s *Store) collectIDs(query string, args ...any) []string {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// Stats summarises the history for the UI.
func (s *Store) Stats() Stats {
	out := Stats{ByKind: make(map[string]int, 6)}
	rows, err := s.db.Query(`SELECT kind, COUNT(*), SUM(pinned) FROM items GROUP BY kind`)
	if err == nil {
		for rows.Next() {
			var kind string
			var count, pinned int
			if rows.Scan(&kind, &count, &pinned) == nil {
				out.ByKind[kind] = count
				out.Total += count
				out.Pinned += pinned
			}
		}
		rows.Close()
	}
	var last string
	if err := s.db.QueryRow(`SELECT updated_at FROM items ORDER BY updated_at DESC LIMIT 1`).Scan(&last); err == nil {
		if t := decodeTime(last); !t.IsZero() {
			out.LastCapture = t.Local().Format(time.RFC3339)
		}
	}
	var bytes int64
	for _, name := range []string{dbFileName, dbFileName + "-wal"} {
		if info, err := os.Stat(filepath.Join(s.dir, name)); err == nil {
			bytes += info.Size()
		}
	}
	out.StorageBytes = bytes
	return out
}

// ---- blobs --------------------------------------------------------------

// SaveBlob stores an image/binary payload for an item. Callers keep passing
// "<id>.ext" style names; the id is all the database needs.
func (s *Store) SaveBlob(id, ext string, data []byte) error {
	_, err := s.db.Exec(
		`INSERT INTO blobs (item_id, ext, data) VALUES (?,?,?)
		 ON CONFLICT(item_id) DO UPDATE SET ext = excluded.ext, data = excluded.data`,
		id, ext, data,
	)
	return err
}

// LoadBlobByName resolves "<id>.png" / "<id>.bin" to its bytes.
func (s *Store) LoadBlobByName(name string) ([]byte, error) {
	if name == "" {
		return nil, errors.New("没有可用的附件")
	}
	return s.LoadBlobByID(strings.TrimSuffix(strings.TrimSuffix(name, ".png"), ".bin"))
}

// LoadBlobByID returns the payload stored for an item id.
func (s *Store) LoadBlobByID(id string) ([]byte, error) {
	if id == "" {
		return nil, errors.New("没有可用的附件")
	}
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM blobs WHERE item_id = ?`, id).Scan(&data); err != nil {
		return nil, err
	}
	return data, nil
}

// BlobExt returns the stored extension for an item ("" when there is none).
func (s *Store) BlobExt(id string) string {
	var ext string
	if err := s.db.QueryRow(`SELECT ext FROM blobs WHERE item_id = ?`, id).Scan(&ext); err != nil {
		return ""
	}
	return ext
}

// ReadBlobData returns a data: URL for the webview.
func (s *Store) ReadBlobData(name string) (string, error) {
	data, err := s.LoadBlobByName(name)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}

// ---- diagnostics --------------------------------------------------------

// logDir remembers the data directory so package-level code (hooks, expander) can
// log without threading a *Store everywhere.
var logDirValue atomic.Value // string

func logDir() string {
	if v, ok := logDirValue.Load().(string); ok {
		return v
	}
	return ""
}

// Logf appends one diagnostic line to <dir>/clipnest.log. Logging failures are
// deliberately silent: diagnostics must never break the app.
func Logf(dir, format string, args ...any) {
	if dir == "" {
		return
	}
	line := fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"),
		fmt.Sprintf(format, args...))
	f, err := os.OpenFile(filepath.Join(dir, "clipnest.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// TrimLog keeps clipnest.log below limit bytes so it cannot grow forever.
func TrimLog(dir string, limit int64) {
	p := filepath.Join(dir, "clipnest.log")
	if info, err := os.Stat(p); err == nil && info.Size() > limit {
		_ = os.WriteFile(p, nil, 0o644)
	}
}

// normalizeTimestamps rewrites any timestamp that is not in the fixed-width
// layout (older builds wrote RFC3339Nano, whose fraction width varies). Runs on
// every open; it is a no-op once the data is already normalised.
func normalizeTimestamps(db *sql.DB) {
	targets := []struct{ table, column string }{
		{"items", "created_at"}, {"items", "updated_at"},
		{"snippets", "created_at"}, {"snippets", "updated_at"},
		{"memos", "created_at"}, {"memos", "updated_at"},
		{"tombstones", "deleted_at"},
	}
	for _, t := range targets {
		rows, err := db.Query(`SELECT rowid, ` + t.column + ` FROM ` + t.table)
		if err != nil {
			continue
		}
		type fix struct {
			rowid int64
			value string
		}
		var fixes []fix
		for rows.Next() {
			var id int64
			var raw string
			if rows.Scan(&id, &raw) != nil {
				continue
			}
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				continue
			}
			want := parsed.UTC().Format(timeLayout)
			if want != raw {
				fixes = append(fixes, fix{id, want})
			}
		}
		rows.Close()
		for _, f := range fixes {
			_, _ = db.Exec(`UPDATE `+t.table+` SET `+t.column+` = ? WHERE rowid = ?`, f.value, f.rowid)
		}
	}
}