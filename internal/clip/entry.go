package clip

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 统一条目（Entry）—— 被动收集的剪贴板内容、片段、手写备忘都是同一个类型。
//
// 模型（简单版，刻意不做插件系统）：
//
//	内容    文本 / 图片 / 文件引用
//	tag     正文里的 #tag，纯用户语义，系统不往里塞东西
//	action  能力，只有两个：clipboard（出现在快速粘贴列表）/ snippet（可配缩写）
//
// 早期版本用 auto_tags 表达「剪贴板/片段」，那是把能力塞进分类，模型不正；
// 现在 actions 单独一列。Source 只记录「怎么进来的」，不承担能力语义。

// Source says how an entry got here.
type Source string

const (
	SourceManual    Source = "manual"    // 用户手写
	SourceClipboard Source = "clipboard" // 自动采集
	SourceImport    Source = "import"    // 从外部导入
)

// 能力名（只有两个，不做插件式扩展）
const (
	ActionClipboard = "clipboard" // 出现在快速粘贴列表
	ActionSnippet   = "snippet"   // 可配缩写，打字时展开
)

// Entry is the single content type of the app.
type Entry struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Kind    Kind     `json:"kind"`
	Source  Source   `json:"source"`
	Tags    []string `json:"tags"`    // 从正文 #tag 派生
	Actions []string `json:"actions"` // clipboard / snippet

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

	// 生命周期：nil = 永久。现在所有条目都不自动过期（以保留为主）。
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AllTags returns the entry's tags (sorted, de-duplicated).
func (e Entry) AllTags() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(e.Tags))
	for _, t := range e.Tags {
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// HasAction reports whether the entry carries a capability.
func (e Entry) HasAction(name string) bool {
	for _, a := range e.Actions {
		if a == name {
			return true
		}
	}
	return false
}

// SetAction adds or removes a capability.
func (e *Entry) SetAction(name string, on bool) {
	if on {
		if !e.HasAction(name) {
			e.Actions = append(e.Actions, name)
		}
		return
	}
	out := e.Actions[:0]
	for _, a := range e.Actions {
		if a != name {
			out = append(out, a)
		}
	}
	e.Actions = out
}

// IsSnippet reports whether the entry participates in abbreviation expansion.
func (e Entry) IsSnippet() bool {
	return strings.TrimSpace(e.TriggerKey) != ""
}

// Preview returns a single-line excerpt for list rendering.
func (e Entry) Preview() string {
	s := strings.TrimSpace(strings.ReplaceAll(e.Content, "\n", " "))
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

// ---- action 集合的序列化 -------------------------------------------------

// actionSpec renders ",clipboard,snippet," so filters can use LIKE.
func actionSpec(actions []string) string {
	if len(actions) == 0 {
		return ""
	}
	return "," + strings.Join(actions, ",") + ","
}

func splitActionSpec(spec string) []string {
	spec = strings.Trim(spec, ",")
	if spec == "" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, a := range strings.Split(spec, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// ---- schema -------------------------------------------------------------

const entrySchema = `
CREATE TABLE IF NOT EXISTS entries (
  id           TEXT PRIMARY KEY,
  content      TEXT NOT NULL DEFAULT '',
  kind         TEXT NOT NULL DEFAULT 'text',
  source       TEXT NOT NULL DEFAULT 'manual',
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

  expires_at   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
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

const entryColumns = `id, content, kind, source, tags, actions, pinned, archived,
	hash, seen_count, copy_count, source_app, width, height, html, rtf, format_name, format_id, files,
	trigger_key, case_sensitive, keep_trigger, apps, exclude_apps, group_name, use_count,
	expires_at, created_at, updated_at`

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
	)
	err := sc.Scan(&e.ID, &e.Content, &kind, &source, &tags, &actionsSpec, &pinned, &archived,
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

	if _, err := s.db.Exec(`
		INSERT INTO entries (`+entryColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			content=excluded.content, kind=excluded.kind, source=excluded.source,
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
			expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		in.ID, in.Content, string(in.Kind), string(in.Source),
		TagSpec(in.Tags), actionSpec(in.Actions), boolInt(in.Pinned), boolInt(in.Archived),
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

// DeleteEntry removes an entry and its index row, and leaves a tombstone so
// peers drop their copy too.
func (s *Store) DeleteEntry(id string) bool {
	if e, ok := s.GetEntry(id); ok && e.Hash != "" {
		s.AddTombstoneScoped("entry", e.Hash, nowUTC(), hostName())
	}
	res, err := s.db.Exec(`DELETE FROM entries WHERE id = ?`, id)
	if err != nil {
		return false
	}
	_, _ = s.db.Exec(`DELETE FROM entries_fts WHERE id = ?`, id)
	n, _ := res.RowsAffected()
	return n > 0
}

// EntryQuery is the single filter language: the sidebar's saved filters, the
// calendar's day selection and the search box all compile down to this.
type EntryQuery struct {
	Text     string `json:"text"`
	Tag      string `json:"tag"`
	Source   string `json:"source"` // manual | clipboard | import
	Action   string `json:"action"` // clipboard | snippet
	Kind     string `json:"kind"`
	Snippets bool   `json:"snippets"` // 只看有缩写的
	Pinned   bool   `json:"pinned"`
	Archived bool   `json:"archived"`
	Day      string `json:"day"`  // YYYY-MM-DD：日历点某天
	From     string `json:"from"` // RFC3339（含）
	To       string `json:"to"`   // RFC3339（不含）
	Limit    int    `json:"limit"`
	Before   string `json:"before"` // 游标：created_at < 此值
	Tail     bool   `json:"tail"`   // true = 取最新 N 条
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
	if v := strings.TrimSpace(q.Action); v != "" {
		where = append(where, "actions LIKE ?")
		args = append(args, "%,"+v+",%")
	}
	if v := strings.TrimSpace(q.Tag); v != "" {
		where = append(where, "tags LIKE ?")
		args = append(args, "%,"+v+",%")
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
		// created_at 是固定宽度 RFC3339（见 timeLayout），字符串序 == 时间序
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
			where = append(where, `(content LIKE ? ESCAPE '\' OR tags LIKE ? ESCAPE '\')`)
			args = append(args, like, like)
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
	q(&st.Clipboard, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND actions LIKE '%,clipboard,%'`)
	q(&st.Snippets, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND trigger_key <> ''`)
	q(&st.Pinned, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND pinned = 1`)
	q(&st.Archived, `SELECT COUNT(*) FROM entries WHERE archived = 1`)
	start, end, _ := dayBounds(time.Now().Format("2006-01-02"))
	q(&st.Today, `SELECT COUNT(*) FROM entries WHERE archived = 0 AND created_at >= ? AND created_at < ?`, start, end)
	return st
}

// EntryTags lists the distinct tags in use, most used first.
func (s *Store) EntryTags() []string {
	rows, err := s.db.Query(`SELECT tags FROM entries WHERE archived = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var tags string
		if rows.Scan(&tags) != nil {
			continue
		}
		for _, t := range splitTagSpec(tags) {
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
// in Markdown. Small blobs stay in SQLite; larger ones go to disk so the
// database (and its WAL, and the sync package) stay small.
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

// ---- 标签与哈希（从原 memo.go 收拢到这里） ------------------------------

var tagPattern = regexp.MustCompile(`#([\p{L}\p{N}_\-/]+)`)

// ExtractTags pulls "#tag" tokens out of content, keeping the order they appear.
func ExtractTags(content string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range tagPattern.FindAllStringSubmatch(content, -1) {
		tag := strings.TrimSpace(m[1])
		if tag == "" || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		out = append(out, tag)
	}
	return out
}

// TagSpec renders tags as ",a,b," for LIKE-friendly filtering.
func TagSpec(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return "," + strings.Join(tags, ",") + ","
}

func splitTagSpec(spec string) []string {
	spec = strings.Trim(spec, ",")
	if spec == "" {
		return []string{}
	}
	out := make([]string, 0, 4)
	for _, t := range strings.Split(spec, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// contentHash is the de-dupe / sync pairing key.
func contentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])[:16]
}

// SaveCapture stores a clipboard capture as an entry carrying the clipboard
// action. De-duplicates by content hash (bumping seen_count) so copying the same
// thing twice does not create two rows.
func (s *Store) SaveCapture(c Captured) (*Entry, bool, error) {
	if c.Hash != "" {
		if existing, ok := s.findEntryByHash(c.Hash); ok {
			if _, err := s.db.Exec(
				`UPDATE entries SET seen_count = seen_count + 1, updated_at = ? WHERE id = ?`,
				encodeTime(nowUTC()), existing.ID); err != nil {
				return nil, false, err
			}
			e, _ := s.GetEntry(existing.ID)
			return &e, false, nil
		}
	}

	content := strings.TrimSpace(c.Text)
	if len(c.PNG) > 0 {
		if token, err := s.SaveEntryBlob(c.PNG, "png"); err == nil {
			content = "![](" + token + ")"
		}
	}
	if content == "" {
		content = placeholderForKind(c.Kind)
	}
	if content == "" {
		return nil, false, errors.New("没有可保存的内容")
	}

	saved, err := s.SaveEntry(Entry{
		Content:    content,
		Kind:       c.Kind,
		Source:     SourceClipboard,
		Actions:    []string{ActionClipboard},
		Hash:       c.Hash,
		SeenCount:  1,
		SourceApp:  c.SourceApp,
		Width:      c.Width,
		Height:     c.Height,
		HTML:       c.HTML,
		RTF:        c.RTF,
		FormatName: c.FormatName,
		FormatID:   c.FormatID,
		Files:      c.Files,
	})
	if err != nil {
		return nil, false, err
	}
	return &saved, true, nil
}

// BumpCopyCount records that an entry was pasted/copied again.
func (s *Store) BumpCopyCount(id string) error {
	_, err := s.db.Exec(`UPDATE entries SET copy_count = copy_count + 1 WHERE id = ?`, id)
	return err
}

// DeleteAllUnpinned clears the history but keeps pinned entries.
func (s *Store) DeleteAllUnpinned() int {
	rows, err := s.db.Query(`SELECT id FROM entries WHERE pinned = 0`)
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

// RefreshEntryFiles re-stats the file references of one entry.
func (s *Store) RefreshEntryFiles(id string) (Entry, bool) {
	e, ok := s.GetEntry(id)
	if !ok || len(e.Files) == 0 {
		return Entry{}, false
	}
	for i := range e.Files {
		e.Files[i] = statFile(e.Files[i])
	}
	if _, err := s.SaveEntry(e); err != nil {
		return e, false
	}
	return e, true
}

// blobExtForKind is the file extension used for an entry's attachment.
func blobExtForKind(k Kind) string {
	if k == KindImage {
		return ".png"
	}
	return ""
}

// CountEntries is the number of live (non-archived) entries.
func (s *Store) CountEntries() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE archived = 0`).Scan(&n)
	return n
}

// mergeEntry upserts a remote entry EXACTLY as received, preserving its
// timestamps. SaveEntry would stamp updated_at = now, which makes the local copy
// look newer than a peer's tombstone and silently resurrects deleted entries.
func (s *Store) mergeEntry(e Entry) error {
	if e.ID == "" || e.Hash == "" {
		return errors.New("条目缺少 id/hash")
	}
	if _, err := s.db.Exec(`
		INSERT INTO entries (`+entryColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			content=excluded.content, kind=excluded.kind, source=excluded.source,
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
			expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		e.ID, e.Content, string(e.Kind), string(e.Source),
		TagSpec(e.Tags), actionSpec(e.Actions), boolInt(e.Pinned), boolInt(e.Archived),
		e.Hash, e.SeenCount, e.CopyCount, e.SourceApp, e.Width, e.Height,
		e.HTML, e.RTF, e.FormatName, e.FormatID, encodeFiles(e.Files),
		e.TriggerKey, boolInt(e.CaseSensitive), boolInt(e.KeepTrigger),
		joinList(e.Apps), joinList(e.ExcludeApps), e.GroupName, e.UseCount,
		encodeTimePtr(e.ExpiresAt), encodeTime(e.CreatedAt), encodeTime(e.UpdatedAt),
	); err != nil {
		return err
	}
	return s.indexEntry(e)
}
