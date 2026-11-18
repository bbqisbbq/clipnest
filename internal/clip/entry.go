package clip

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// 统一条目（Entry）—— 剪贴板采集、片段、手写备忘都是同一个类型，
// 用 Source / AutoTags / TriggerKey / ExpiresAt 这些"属性"区分用途，
// 而不是用不同的表。设计说明见 docs/UNIFIED-ENTRY.md。
//
// 为什么能合：三者的差异都是属性差异（怎么进来的、会不会过期、有没有缩写），
// 不是结构差异。合并后导航靠日历 + 自定义筛选器，不再需要三个并列页面。

// Source says how an entry got here.
type Source string

const (
	SourceManual    Source = "manual"    // 用户手写
	SourceClipboard Source = "clipboard" // 自动采集
	SourceImport    Source = "import"    // 从外部导入
)

// 系统标签：自动加，用户改正文不会丢。
const (
	AutoTagClipboard = "剪贴板"
	AutoTagSnippet   = "片段"
)

// Entry is the single content type of the app.
type Entry struct {
	ID       string   `json:"id"`
	Content  string   `json:"content"`
	Kind     Kind     `json:"kind"`
	Source   Source   `json:"source"`
	Tags     []string `json:"tags"`     // 从正文 #tag 派生
	AutoTags []string `json:"autoTags"` // 系统标签（剪贴板 / 片段）

	Pinned   bool `json:"pinned"`
	Archived bool `json:"archived"`

	// 采集相关
	Hash       string    `json:"hash,omitempty"`
	SeenCount  int       `json:"seenCount,omitempty"`
	CopyCount  int       `json:"copyCount,omitempty"`
	SourceApp  string    `json:"sourceApp,omitempty"`
	Width      int       `json:"width,omitempty"`
	Height     int       `json:"height,omitempty"`
	HTML       string    `json:"html,omitempty"`
	RTF        string    `json:"rtf,omitempty"`
	FormatName string    `json:"formatName,omitempty"`
	FormatID   uint32    `json:"formatId,omitempty"`
	Files      []FileRef `json:"files,omitempty"`

	// 片段相关（TriggerKey 非空 = 这条参与缩写展开）
	TriggerKey    string   `json:"triggerKey,omitempty"`
	CaseSensitive bool     `json:"caseSensitive,omitempty"`
	KeepTrigger   bool     `json:"keepTrigger,omitempty"`
	Apps          []string `json:"apps,omitempty"`
	ExcludeApps   []string `json:"excludeApps,omitempty"`
	GroupName     string   `json:"groupName,omitempty"`
	UseCount      int      `json:"useCount,omitempty"`

	// 生命周期：nil = 永久（手写）；采集条目默认 +30 天
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AllTags merges derived and system tags for display / filtering.
func (e Entry) AllTags() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(e.Tags)+len(e.AutoTags))
	for _, t := range append(append([]string{}, e.Tags...), e.AutoTags...) {
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// IsSnippet reports whether the entry participates in abbreviation expansion.
func (e Entry) IsSnippet() bool { return strings.TrimSpace(e.TriggerKey) != "" }

// Preview returns a single-line excerpt for list rendering.
func (e Entry) Preview() string {
	s := strings.ReplaceAll(e.Content, "\n", " ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

// ---- schema -------------------------------------------------------------

const entrySchema = `
CREATE TABLE IF NOT EXISTS entries (
  id           TEXT PRIMARY KEY,
  content      TEXT NOT NULL DEFAULT '',
  kind         TEXT NOT NULL DEFAULT 'text',
  source       TEXT NOT NULL DEFAULT 'manual',
  tags         TEXT NOT NULL DEFAULT '',
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

  expires_at   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_entries_created ON entries(created_at);
CREATE INDEX IF NOT EXISTS idx_entries_expires ON entries(expires_at);
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

const entryColumns = `id, content, kind, source, tags, auto_tags, pinned, archived,
	hash, seen_count, copy_count, source_app, width, height, html, rtf, format_name, format_id, files,
	trigger_key, case_sensitive, keep_trigger, apps, exclude_apps, group_name, use_count,
	expires_at, created_at, updated_at`

func scanEntry(sc scannable) (Entry, error) {
	var (
		e                              Entry
		kind, source                   string
		tags, autoTags                 string
		pinned, archived               int
		formatID                       uint32
		files                          string
		caseSensitive, keepTrigger     int
		apps, excludeApps              string
		expiresAt                      string
		createdAt, updatedAt           string
	)
	err := sc.Scan(&e.ID, &e.Content, &kind, &source, &tags, &autoTags, &pinned, &archived,
		&e.Hash, &e.SeenCount, &e.CopyCount, &e.SourceApp, &e.Width, &e.Height,
		&e.HTML, &e.RTF, &e.FormatName, &formatID, &files,
		&e.TriggerKey, &caseSensitive, &keepTrigger, &apps, &excludeApps, &e.GroupName, &e.UseCount,
		&expiresAt, &createdAt, &updatedAt)
	if err != nil {
		return Entry{}, err
	}
	e.Kind = Kind(kind)
	e.Source = Source(source)
	e.Tags = splitTagSpec(tags)
	e.AutoTags = splitTagSpec(autoTags)
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

// SaveEntry inserts or updates an entry (a real upsert: an explicit ID that does
// not exist yet is inserted, which is what the migration needs), refreshing tags
// and the FTS index.
func (s *Store) SaveEntry(in Entry) (Entry, error) {
	in.Content = strings.TrimSpace(in.Content)
	if in.Source == "" {
		in.Source = SourceManual
	}
	if in.Kind == "" {
		in.Kind = KindText
	}
	if in.Content == "" && in.Kind == KindText {
		return Entry{}, errors.New("内容不能为空")
	}
	// 手写/导入的内容才有 #tag；采集内容保持原样，靠 auto_tags 分类
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
		in.Hash = memoHash(in.Content)
	}

	if _, err := s.db.Exec(`
		INSERT INTO entries (`+entryColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			content=excluded.content, kind=excluded.kind, source=excluded.source,
			tags=excluded.tags, auto_tags=excluded.auto_tags,
			pinned=excluded.pinned, archived=excluded.archived,
			hash=excluded.hash, source_app=excluded.source_app,
			width=excluded.width, height=excluded.height,
			html=excluded.html, rtf=excluded.rtf,
			format_name=excluded.format_name, format_id=excluded.format_id,
			files=excluded.files,
			trigger_key=excluded.trigger_key, case_sensitive=excluded.case_sensitive,
			keep_trigger=excluded.keep_trigger, apps=excluded.apps,
			exclude_apps=excluded.exclude_apps, group_name=excluded.group_name,
			expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		in.ID, in.Content, string(in.Kind), string(in.Source),
		TagSpec(in.Tags), TagSpec(in.AutoTags), boolInt(in.Pinned), boolInt(in.Archived),
		in.Hash, in.SeenCount, in.CopyCount, in.SourceApp, in.Width, in.Height,
		in.HTML, in.RTF, in.FormatName, in.FormatID, encodeFiles(in.Files),
		in.TriggerKey, boolInt(in.CaseSensitive), boolInt(in.KeepTrigger),
		joinList(in.Apps), joinList(in.ExcludeApps), in.GroupName, in.UseCount,
		encodeTimePtr(in.ExpiresAt), encodeTime(in.CreatedAt), encodeTime(in.UpdatedAt),
	); err != nil {
		return Entry{}, err
	}
	if err := s.indexEntry(in); err != nil {
		Logf(s.dir, "entry fts index failed: %v", err)
	}
	return in, nil
}
func encodeTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return encodeTime(*t)
}

func (s *Store) indexEntry(e Entry) error {
	if _, err := s.db.Exec(`DELETE FROM entries_fts WHERE id = ?`, e.ID); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO entries_fts (id, content, tags) VALUES (?,?,?)`,
		e.ID, e.Content, strings.Join(e.AllTags(), " "))
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

// DeleteEntry removes an entry and its index row.
func (s *Store) DeleteEntry(id string) bool {
	res, err := s.db.Exec(`DELETE FROM entries WHERE id = ?`, id)
	if err != nil {
		return false
	}
	_, _ = s.db.Exec(`DELETE FROM entries_fts WHERE id = ?`, id)
	n, _ := res.RowsAffected()
	return n > 0
}

// EntryQuery is the single filter language for the whole app: the sidebar's
// saved filters, the calendar's day selection and the search box all compile
// down to this.
type EntryQuery struct {
	Text      string `json:"text"`
	Tag       string `json:"tag"`
	Source    string `json:"source"`    // manual | clipboard | import
	AutoTag   string `json:"autoTag"`   // 剪贴板 | 片段
	Kind      string `json:"kind"`
	Snippets  bool   `json:"snippets"`  // 只看有缩写的
	Pinned    bool   `json:"pinned"`
	Archived  bool   `json:"archived"`
	Day       string `json:"day"`       // YYYY-MM-DD：日历点某天
	From      string `json:"from"`      // RFC3339（含）
	To        string `json:"to"`        // RFC3339（不含）
	Limit     int    `json:"limit"`
	Before    string `json:"before"`    // 游标：created_at < 此值
	Tail      bool   `json:"tail"`      // true = 取最新 N 条
}

// ListEntries applies the query. Ordered chronologically (chat style).
func (s *Store) ListEntries(q EntryQuery) []Entry {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := []string{"archived = ?"}
	args := []any{boolInt(q.Archived)}

	if v := strings.TrimSpace(q.Source); v != "" {
		where = append(where, "source = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.AutoTag); v != "" {
		where = append(where, "auto_tags LIKE ?")
		args = append(args, "%,"+v+",%")
	}
	if v := strings.TrimSpace(q.Tag); v != "" {
		where = append(where, "(tags LIKE ? OR auto_tags LIKE ?)")
		args = append(args, "%,"+v+",%", "%,"+v+",%")
	}
	if v := strings.TrimSpace(q.Kind); v != "" {
		where = append(where, "kind = ?")
		args = append(args, v)
	}
	if q.Snippets {
		where = append(where, "trigger_key <> ''")
	}
	if q.Pinned {
		where = append(where, "pinned = 1")
	}
	if v := strings.TrimSpace(q.Day); v != "" {
		// 按天：把这一天的 [00:00, 次日 00:00) 换算成时间戳比较
		if start, end, ok := dayBounds(v); ok {
			where = append(where, "created_at >= ? AND created_at < ?")
			args = append(args, start, end)
		}
	}
	if v := strings.TrimSpace(q.From); v != "" {
		where = append(where, "created_at >= ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.To); v != "" {
		where = append(where, "created_at < ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.Before); v != "" {
		where = append(where, "created_at < ?")
		args = append(args, v)
	}

	text := strings.TrimSpace(q.Text)
	if text != "" {
		if len([]rune(text)) >= 3 {
			where = append(where, `id IN (SELECT id FROM entries_fts WHERE entries_fts MATCH ?)`)
			args = append(args, `"`+strings.ReplaceAll(text, `"`, `""`)+`"`)
		} else {
			like := "%" + escapeLike(text) + "%"
			where = append(where, `(content LIKE ? ESCAPE '\' OR tags LIKE ? ESCAPE '\' OR auto_tags LIKE ? ESCAPE '\')`)
			args = append(args, like, like, like)
		}
	}

	order := "ASC"
	if q.Tail {
		order = "DESC"
	}
	sqlText := `SELECT ` + entryColumns + ` FROM entries WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at ` + order + `, updated_at ` + order + ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		Logf(s.dir, "entry list failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]Entry, 0, 32)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return out
		}
		out = append(out, e)
	}
	if q.Tail {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// dayBounds turns "YYYY-MM-DD" (local) into a UTC [start, end) timestamp pair.
func dayBounds(day string) (string, string, bool) {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return "", "", false
	}
	return encodeTime(t), encodeTime(t.AddDate(0, 0, 1)), true
}

// PruneExpired deletes entries past their own expiry. Permanent entries
// (ExpiresAt == nil) are never touched, which is why the two lifecycles can
// share one table.
func (s *Store) PruneExpired() int {
	now := encodeTime(nowUTC())
	rows, err := s.db.Query(`SELECT id FROM entries WHERE expires_at <> '' AND expires_at < ?`, now)
	if err != nil {
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		s.DeleteEntry(id)
	}
	return len(ids)
}

// EntryStats summarises counts for the sidebar / filters.
type EntryStats struct {
	Total     int `json:"total"`
	Manual    int `json:"manual"`
	Clipboard int `json:"clipboard"`
	Snippets  int `json:"snippets"`
	Pinned    int `json:"pinned"`
	Archived  int `json:"archived"`
	Today     int `json:"today"`
}

// EntryStats counts entries per dimension.
func (s *Store) EntryStats() EntryStats {
	var st EntryStats
	q := func(dst *int, sqlText string, args ...any) {
		_ = s.db.QueryRow(sqlText, args...).Scan(dst)
	}
	q(&st.Total, `SELECT COUNT(*) FROM entries WHERE archived = 0`)
	q(&st.Manual, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND source = 'manual'`)
	q(&st.Clipboard, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND source = 'clipboard'`)
	q(&st.Snippets, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND trigger_key <> ''`)
	q(&st.Pinned, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND pinned = 1`)
	q(&st.Archived, `SELECT COUNT(*) FROM entries WHERE archived = 1`)
	start, end, _ := dayBounds(time.Now().Format("2006-01-02"))
	q(&st.Today, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND created_at >= ? AND created_at < ?`, start, end)
	return st
}

// EntryTags lists the distinct tags in use, most used first.
func (s *Store) EntryTags() []string {
	rows, err := s.db.Query(`SELECT tags, auto_tags FROM entries WHERE archived = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var tags, autoTags string
		if rows.Scan(&tags, &autoTags) != nil {
			continue
		}
		for _, t := range append(splitTagSpec(tags), splitTagSpec(autoTags)...) {
			counts[t]++
		}
	}
	type kv struct {
		tag string
		n   int
	}
	list := make([]kv, 0, len(counts))
	for t, n := range counts {
		list = append(list, kv{t, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n == list[j].n {
			return list[i].tag < list[j].tag
		}
		return list[i].n > list[j].n
	})
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.tag)
	}
	return out
}

// EntryTriggers returns entries that participate in abbreviation expansion.
func (s *Store) EntryTriggers() []Entry {
	rows, err := s.db.Query(`SELECT ` + entryColumns + ` FROM entries
		WHERE trigger_key <> '' AND archived = 0 ORDER BY LENGTH(trigger_key) DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return out
		}
		out = append(out, e)
	}
	return out
}

// FindEntryByTrigger looks up a snippet entry by its abbreviation.
func (s *Store) FindEntryByTrigger(key string, caseSensitive bool) (Entry, bool) {
	var row *sql.Row
	if caseSensitive {
		row = s.db.QueryRow(`SELECT `+entryColumns+` FROM entries
			WHERE trigger_key = ? AND case_sensitive = 1 AND archived = 0 LIMIT 1`, key)
	} else {
		row = s.db.QueryRow(`SELECT `+entryColumns+` FROM entries
			WHERE LOWER(trigger_key) = LOWER(?) AND archived = 0 LIMIT 1`, key)
	}
	e, err := scanEntry(row)
	if err != nil {
		return Entry{}, false
	}
	return e, true
}

// ---- 附件（内容寻址，剪贴板图片与备忘图片共用） --------------------------

// SaveEntryBlob stores bytes under their content hash and returns the token used
// in Markdown / the entry. Small blobs stay in SQLite; larger ones go to disk so
// the database (and its WAL, and the sync package) stay small.
func (s *Store) SaveEntryBlob(payload []byte, ext string) (string, error) {
	if len(payload) == 0 {
		return "", errors.New("内容为空")
	}
	if ext == "" {
		ext = "png"
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])[:24]
	mime := sniffImageMime(payload)

	storage := "db"
	relPath := ""
	var data []byte
	if len(payload) > memoInlineMax {
		relPath = "entry_files/" + hash + "." + ext
		abs := filepath.Join(s.dir, relPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, payload, 0o644); err != nil {
			return "", err
		}
		storage = "file"
	} else {
		data = payload
	}
	if _, err := s.db.Exec(`
		INSERT INTO entry_blobs (hash, ext, mime, data, storage, size, path)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(hash) DO UPDATE SET
			mime = excluded.mime, size = excluded.size, storage = excluded.storage,
			path = excluded.path,
			data = CASE WHEN excluded.storage = 'db' THEN excluded.data ELSE entry_blobs.data END`,
		hash, ext, mime, data, storage, len(payload), relPath); err != nil {
		return "", err
	}
	return memoImagePrefix + hash, nil
}

// LoadEntryBlob returns the bytes behind a hash (with or without the token prefix).
func (s *Store) LoadEntryBlob(hash string) ([]byte, string, error) {
	hash = strings.TrimPrefix(strings.TrimSpace(hash), memoImagePrefix)
	if hash == "" {
		return nil, "", errors.New("缺少标识")
	}
	var (
		mime, storage, path string
		data                []byte
	)
	if err := s.db.QueryRow(
		`SELECT mime, storage, path, data FROM entry_blobs WHERE hash = ?`, hash).
		Scan(&mime, &storage, &path, &data); err != nil {
		return nil, "", err
	}
	if storage == "file" && path != "" {
		buf, err := os.ReadFile(filepath.Join(s.dir, path))
		if err != nil {
			return nil, mime, err
		}
		return buf, mime, nil
	}
	return data, mime, nil
}