package clip

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store 是唯一的数据入口。库里只有统一条目相关的表：
//
//	entries        条目本体（tag / actions / 内容）
//	entry_blobs    附件（内容寻址，小图进库、大图落盘）
//	entries_fts    中文全文索引（trigram）
//	entry_filters  用户保存的筛选器
//	tombstones     删除墓碑（同步用）
//	meta           键值对（设备 id、一次性标记等）
//
// 早期版本的 items / memos / snippets 三张表已删除：它们表达的是同一种东西的
// 三种形态，现在统一为 entries + actions。

type Store struct {
	db           *sql.DB
	dir          string
	settingsPath string

	mu       sync.RWMutex // guards settings + closed
	settings Settings
	closed   bool
}

const dbFileName = "clipnest.db"

// timeLayout is a FIXED-WIDTH RFC3339 form: always 9 fractional digits, so that
// lexicographic order equals chronological order (variable-width fractions break
// both ordering and cursor pagination).
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// coreSchema is the part of the schema that is not the entries model itself.
const coreSchema = `
CREATE TABLE IF NOT EXISTS meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS tombstones (
  hash       TEXT NOT NULL,
  deleted_at TEXT NOT NULL,
  device     TEXT NOT NULL DEFAULT '',
  scope      TEXT NOT NULL DEFAULT 'entry',
  PRIMARY KEY (hash, scope)
);
CREATE INDEX IF NOT EXISTS idx_tombstones_scope ON tombstones(scope, deleted_at);
`

// scannable is what both *sql.Row and *sql.Rows satisfy.
type scannable interface {
	Scan(dest ...any) error
}

// Tombstone records a deletion so peers drop their copy too.
type Tombstone struct {
	Hash      string    `json:"hash"`
	DeletedAt time.Time `json:"deletedAt"`
	Device    string    `json:"device,omitempty"`
	Scope     string    `json:"scope,omitempty"`
}

// Open opens (or creates) the database in dir.
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
	// WAL 已由上面的 DSN 打开：允许「一个写者 + 多个读者」并行。但只给 1 条连接时，
	// database/sql 会把所有查询串在同一把连接上——首次同步要写上千条，界面在这期间
	// 完全查不动。给 4 条：读走空闲连接，写者互斥交给 busy_timeout(5000) 与 SQLite。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if _, err := db.Exec(coreSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库失败: %w", err)
	}
	if _, err := db.Exec(entrySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化条目表失败: %w", err)
	}
	// CREATE TABLE IF NOT EXISTS 不会给已有库加列：老库的新列在这里补齐。
	if err := ensureColumn(db, "entries", "title", "TEXT NOT NULL DEFAULT ''"); err != nil {
		Logf(dir, "schema upgrade (entries.title) failed: %v", err)
	}
	// 标题来源（2.9.5）：空 = 老数据/未判定，SaveEntry 时按正文一级标题补判。
	if err := ensureColumn(db, "entries", "title_source", "TEXT NOT NULL DEFAULT ''"); err != nil {
		Logf(dir, "schema upgrade (entries.title_source) failed: %v", err)
	}
	// 待办派生列（1.0.x → 2.5 的迁移）：老库补两列，索引不回填也安全——
	// todo_state 由 SaveEntry 维护，老条目在被编辑前就是 ''（= 没有待办）。
	for _, col := range []struct{ name, decl string }{
		{"todo_state", "TEXT NOT NULL DEFAULT ''"},
		{"todo_done_at", "TEXT NOT NULL DEFAULT ''"},
		// 片段的执行类型（2.8.3）：以前直接写进 entries.kind，把 password/app/command
		// 混进了"正文类型"枚举里。
		{"snippet_kind", "TEXT NOT NULL DEFAULT ''"},
		// 图片 OCR（2.9.5）：识别文字与状态。老库必须补上 —— 少了这两列，
		// SELECT 与 INSERT 都会报 `no such column: ocr_text`，整个时间线直接空掉。
		{"ocr_text", "TEXT NOT NULL DEFAULT ''"},
		{"ocr_state", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(db, "entries", col.name, col.decl); err != nil {
			Logf(dir, "schema upgrade (entries.%s) failed: %v", col.name, err)
		}
	}

	// 日志目录要在可能写日志的迁移之前就位：dropLegacyTables 内部用 Logf，
	// 顺序反了会让迁移失败的记录写进空目录，等于一条日志都留不下。
	logDirValue.Store(dir)

	migrateTombstoneSchema(db)
	backfillClipboardCapability(db)

	dropLegacyTables(db)

	s := &Store{
		db:           db,
		dir:          dir,
		settingsPath: filepath.Join(dir, "settings.json"),
		settings:     DefaultSettings(),
	}
	if err := s.loadSettings(); err != nil {
		Logf(dir, "settings load failed: %v", err)
	}
	return s, nil
}

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
	st.normalize()
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	return nil
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// SaveSettings persists settings to disk and keeps the in-memory copy in step.
func (s *Store) SaveSettings(st Settings) error {
	st.normalize()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.settingsPath, data, 0o644); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	return nil
}

// PeekSettings reads settings.json without opening the database (used before the
// window is created, to decide whether to start hidden).
func PeekSettings(dir string) (Settings, error) {
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return DefaultSettings(), err
	}
	st := DefaultSettings()
	if err := json.Unmarshal(data, &st); err != nil {
		return DefaultSettings(), err
	}
	st.normalize()
	return st, nil
}

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
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return s.db.Close()
}


// ---- meta ---------------------------------------------------------------

// Meta reads a key from the meta table ("" when absent).
func (s *Store) Meta(k string) string {
	var v string
	_ = s.db.QueryRow(`SELECT v FROM meta WHERE k = ?`, k).Scan(&v)
	return v
}

// SetMeta writes a key.
func (s *Store) SetMeta(k, v string) error {
	_, err := s.db.Exec(
		`INSERT INTO meta (k, v) VALUES (?,?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// ---- tombstones ---------------------------------------------------------

// AddTombstoneScoped records a deletion under a scope ("entry").
func (s *Store) AddTombstoneScoped(scope, hash string, at time.Time, device string) {
	if err := addTombstoneScope(s.db, scope, hash, at, device); err != nil {
		Logf(s.dir, "add tombstone %s/%s failed: %v", scope, hash, err)
	}
}

// addTombstoneScope is the transaction-aware form: deleting an entry writes its
// tombstone inside the same tx (see Store.DeleteEntry).
//
// 返回 error 而不再一丢了之：这条语句在墓碑表的旧主键上会**整条失败**，吞掉错误
// 就等于"删了但不留痕"，对端永远不会知道这条被删过（见 migrateTombstoneSchema）。
func addTombstoneScope(db execer, scope, hash string, at time.Time, device string) error {
	if hash == "" {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO tombstones (hash, deleted_at, device, scope) VALUES (?,?,?,?)
		ON CONFLICT(hash, scope) DO UPDATE SET
			deleted_at = excluded.deleted_at, device = excluded.device`,
		hash, encodeTime(at), device, scope)
	return err
}

// TombstonesScoped lists deletions of one scope newer than since.
func (s *Store) TombstonesScoped(scope string, since time.Time, limit int) []Tombstone {
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	rows, err := s.db.Query(`
		SELECT hash, deleted_at, device, scope FROM tombstones
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
		if rows.Scan(&t.Hash, &at, &t.Device, &t.Scope) == nil {
			t.DeletedAt = decodeTime(at)
			out = append(out, t)
		}
	}
	return out
}

// PruneTombstones drops tombstones older than before.
func (s *Store) PruneTombstones(before time.Time) int {
	res, err := s.db.Exec(`DELETE FROM tombstones WHERE deleted_at < ?`, encodeTime(before))
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// ---- stats --------------------------------------------------------------

// Stats is the summary the UI shows in settings (storage usage etc.).
type Stats struct {
	Total        int            `json:"total"`
	Pinned       int            `json:"pinned"`
	ByKind       map[string]int `json:"byKind"`
	StorageBytes int64          `json:"storageBytes"`
	LastCapture  string         `json:"lastCapture"`
}

// Stats summarises the entries table.
func (s *Store) Stats() Stats {
	st := Stats{ByKind: map[string]int{}}
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE archived = 0`).Scan(&st.Total)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE archived = 0 AND pinned = 1`).Scan(&st.Pinned)

	rows, err := s.db.Query(`SELECT kind, COUNT(*) FROM entries WHERE archived = 0 GROUP BY kind`)
	if err == nil {
		for rows.Next() {
			var k string
			var n int
			if rows.Scan(&k, &n) == nil {
				st.ByKind[k] = n
			}
		}
		rows.Close()
	}

	var last sql.NullString
	_ = s.db.QueryRow(`SELECT MAX(created_at) FROM entries`).Scan(&last)
	st.LastCapture = last.String

	// 数据占用：数据库文件 + WAL + 落盘的附件
	for _, name := range []string{dbFileName, dbFileName + "-wal"} {
		if fi, err := os.Stat(filepath.Join(s.dir, name)); err == nil {
			st.StorageBytes += fi.Size()
		}
	}
	if entries, err := os.ReadDir(filepath.Join(s.dir, "entry_files")); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil {
				st.StorageBytes += fi.Size()
			}
		}
	}
	return st
}

