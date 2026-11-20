package clip

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	// 待办派生列（1.0.x → 2.5 的迁移）：老库补两列，索引不回填也安全——
	// todo_state 由 SaveEntry 维护，老条目在被编辑前就是 ''（= 没有待办）。
	for _, col := range []struct{ name, decl string }{
		{"todo_state", "TEXT NOT NULL DEFAULT ''"},
		{"todo_done_at", "TEXT NOT NULL DEFAULT ''"},
		// 片段的执行类型（2.8.3）：以前直接写进 entries.kind，把 password/app/command
		// 混进了"正文类型"枚举里。
		{"snippet_kind", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(db, "entries", col.name, col.decl); err != nil {
			Logf(dir, "schema upgrade (entries.%s) failed: %v", col.name, err)
		}
	}

	// 日志目录要在可能写日志的迁移之前就位：dropLegacyTables 内部用 Logf，
	// 顺序反了会让迁移失败的记录写进空目录，等于一条日志都留不下。
	logDirValue.Store(dir)

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

// ---- 小工具 -------------------------------------------------------------

func encodeTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func decodeTime(v string) time.Time {
	t, err := time.Parse(timeLayout, v)
	if err != nil {
		// 兼容早期写入的变宽格式
		if t2, err2 := time.Parse(time.RFC3339Nano, v); err2 == nil {
			return t2.UTC()
		}
		return time.Time{}
	}
	return t.UTC()
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
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

// statFile refreshes the volatile metadata of one file reference.
func statFile(ref FileRef) FileRef {
	fi, err := os.Stat(ref.Path)
	if err != nil {
		ref.Exists = false
		return ref
	}
	ref.Exists = true
	ref.Size = fi.Size()
	ref.IsDir = fi.IsDir()
	return ref
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// placeholderForKind gives a text stand-in for captures with no text body.
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
	addTombstoneScope(s.db, scope, hash, at, device)
}

// addTombstoneScope is the transaction-aware form: deleting an entry writes its
// tombstone inside the same tx (see Store.DeleteEntry).
func addTombstoneScope(db execer, scope, hash string, at time.Time, device string) {
	if hash == "" {
		return
	}
	_, _ = db.Exec(`
		INSERT INTO tombstones (hash, deleted_at, device, scope) VALUES (?,?,?,?)
		ON CONFLICT(hash, scope) DO UPDATE SET
			deleted_at = excluded.deleted_at, device = excluded.device`,
		hash, encodeTime(at), device, scope)
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

// ---- 日志 ---------------------------------------------------------------

var logDirValue atomic.Value

func logDir() string {
	if v, ok := logDirValue.Load().(string); ok {
		return v
	}
	return ""
}

// Logf appends one diagnostic line to <dir>/clipnest.log.
func Logf(dir, format string, args ...any) {
	if dir == "" {
		dir = logDir()
	}
	if dir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "clipnest.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

// TrimLog keeps the log under limit bytes.
func TrimLog(dir string, limit int64) {
	p := filepath.Join(dir, "clipnest.log")
	fi, err := os.Stat(p)
	if err != nil || fi.Size() <= limit {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	keep := data[len(data)/2:]
	_ = os.WriteFile(p, keep, 0o644)
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
