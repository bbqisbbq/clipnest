package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Memos ("备忘") — the Memos-style fragment notes, integrated natively.
//
// Shape is deliberately close to usememos/memos so the data can move in and out
// later: Markdown content, tags parsed out of the body (#tag), pinned/archived,
// create/update timestamps. What we do NOT copy: users, permissions, sharing,
// comments, reactions, and any HTTP layer.

// Memo is one note.
type Memo struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	Pinned    bool      `json:"pinned"`
	Archived  bool      `json:"archived"`
	Source    string    `json:"source"`    // manual | clipboard | snippet | import
	SourceApp string    `json:"sourceApp"` // which program it came from
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// MemoFilter drives the list query.
type MemoFilter struct {
	Query    string `json:"query"`
	Tag      string `json:"tag"`
	Pinned   bool   `json:"pinned"`
	Archived bool   `json:"archived"`
	Limit    int    `json:"limit"`
	// BeforeCreatedAt 是"再往前一页"的游标：只取创建时间严格早于它的条目。
	// 时间线是聊天式正序（最新在最下面），所以向上滚动时用这个游标翻页。
	BeforeCreatedAt string `json:"beforeCreatedAt"`
	// Tail 为 true 时取"最新的 N 条"（内部按倒序查再翻正），用于首屏。
	// 否则按正序取最早的 N 条，配合 BeforeCreatedAt 向上翻页。
	Tail bool `json:"tail"`
}

var tagPattern = regexp.MustCompile(`#([\p{L}\p{N}_\-/]+)`)

// ExtractTags pulls "#tag" tokens out of Markdown content (Memos semantics).
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
	// 保持正文里出现的顺序，用户看起来更自然
	return out
}

// TagSpec renders tags as ",a,b," for LIKE-friendly filtering.
func TagSpec(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return "," + strings.Join(tags, ",") + ","
}

func memoHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])[:16]
}

// ---- store --------------------------------------------------------------

const memoColumns = `id, content, tags, pinned, archived, source, source_app, hash,
	created_at, updated_at`

func scanMemo(sc scannable) (Memo, error) {
	var (
		m                    Memo
		tags                 string
		pinned, archived     int
		createdAt, updatedAt string
	)
	if err := sc.Scan(&m.ID, &m.Content, &tags, &pinned, &archived, &m.Source, &m.SourceApp,
		&m.Hash, &createdAt, &updatedAt); err != nil {
		return Memo{}, err
	}
	m.Tags = splitTagSpec(tags)
	m.Pinned = pinned != 0
	m.Archived = archived != 0
	m.CreatedAt = decodeTime(createdAt)
	m.UpdatedAt = decodeTime(updatedAt)
	return m, nil
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

// SaveMemo inserts or updates a note and refreshes the search index.
func (s *Store) SaveMemo(in Memo) (Memo, error) {
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return Memo{}, errors.New("内容不能为空")
	}
	in.Tags = ExtractTags(in.Content)
	in.Hash = memoHash(in.Content)
	now := nowUTC()

	if in.ID == "" {
		in.ID = newID()
		in.CreatedAt = now
		in.UpdatedAt = now
		if in.Source == "" {
			in.Source = "manual"
		}
		if _, err := s.db.Exec(`
			INSERT INTO memos (id, content, tags, pinned, archived, source, source_app, hash,
			                   created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			in.ID, in.Content, TagSpec(in.Tags), boolInt(in.Pinned), boolInt(in.Archived),
			in.Source, in.SourceApp, in.Hash, encodeTime(in.CreatedAt), encodeTime(in.UpdatedAt),
		); err != nil {
			return Memo{}, err
		}
	} else {
		in.UpdatedAt = now
		if _, err := s.db.Exec(`
			UPDATE memos SET content=?, tags=?, pinned=?, archived=?, hash=?, updated_at=?
			 WHERE id=?`,
			in.Content, TagSpec(in.Tags), boolInt(in.Pinned), boolInt(in.Archived), in.Hash,
			encodeTime(in.UpdatedAt), in.ID,
		); err != nil {
			return Memo{}, err
		}
	}
	if err := s.indexMemo(in); err != nil {
		Logf(s.dir, "memo fts index failed: %v", err)
	}
	return in, nil
}

// indexMemo rewrites one row in the trigram FTS table.
func (s *Store) indexMemo(m Memo) error {
	if _, err := s.db.Exec(`DELETE FROM memos_fts WHERE id = ?`, m.ID); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO memos_fts (id, content, tags) VALUES (?,?,?)`,
		m.ID, m.Content, strings.Join(m.Tags, " "))
	return err
}

// GetMemo returns one note.
func (s *Store) GetMemo(id string) (Memo, bool) {
	row := s.db.QueryRow(`SELECT `+memoColumns+` FROM memos WHERE id = ?`, id)
	m, err := scanMemo(row)
	if err != nil {
		return Memo{}, false
	}
	return m, true
}

// DeleteMemo removes a note (and its index row).
func (s *Store) DeleteMemo(id string) bool {
	res, err := s.db.Exec(`DELETE FROM memos WHERE id = ?`, id)
	if err != nil {
		return false
	}
	_, _ = s.db.Exec(`DELETE FROM memos_fts WHERE id = ?`, id)
	n, _ := res.RowsAffected()
	return n > 0
}

// ListMemos applies the filter. Chinese search uses the trigram index for
// queries of 3+ characters and falls back to LIKE for shorter ones (trigram
// cannot match those).
func (s *Store) ListMemos(f MemoFilter) []Memo {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 300
	}
	where := []string{"archived = ?"}
	args := []any{boolInt(f.Archived)}
	if f.Pinned {
		where = append(where, "pinned = 1")
	}
	if tag := strings.TrimSpace(f.Tag); tag != "" {
		where = append(where, "tags LIKE ?")
		args = append(args, "%,"+tag+",%")
	}
	q := strings.TrimSpace(f.Query)
	if q != "" {
		if len([]rune(q)) >= 3 {
			where = append(where, `id IN (SELECT id FROM memos_fts WHERE memos_fts MATCH ?)`)
			args = append(args, `"`+strings.ReplaceAll(q, `"`, `""`)+`"`)
		} else {
			where = append(where, `(content LIKE ? ESCAPE '\' OR tags LIKE ? ESCAPE '\')`)
			like := "%" + escapeLike(q) + "%"
			args = append(args, like, like)
		}
	}
	if cursor := strings.TrimSpace(f.BeforeCreatedAt); cursor != "" {
		// 时间戳是固定宽度的 RFC3339（见 timeLayout），字符串序 == 时间序，
		// 所以这里可以直接比较，既精确又能走索引。
		where = append(where, "created_at < ?")
		args = append(args, cursor)
	}
	// 聊天式时间线：纯按创建时间正序，最新的贴着底部输入框。
	// 不做「置顶优先」，否则置顶项会出现在每一页里，游标翻页就乱了；
	// 置顶靠侧栏的「置顶」筛选 + 卡片上的标记来表达。
	order := "ASC"
	if f.Tail {
		// 首屏要"最新的 N 条"：倒序取完再翻正，否则 LIMIT 拿到的是最早的 N 条
		order = "DESC"
	}
	sqlText := `SELECT ` + memoColumns + ` FROM memos WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at ` + order + `, updated_at ` + order + ` LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		Logf(s.dir, "memo list failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]Memo, 0, 32)
	for rows.Next() {
		m, err := scanMemo(rows)
		if err != nil {
			return out
		}
		out = append(out, m)
	}
	if f.Tail {
		// 倒序取回来的，翻成时间正序给界面
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// MemoTags lists the distinct tags in use, most used first.
func (s *Store) MemoTags() []string {
	rows, err := s.db.Query(`SELECT tags FROM memos WHERE archived = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var spec string
		if rows.Scan(&spec) != nil {
			continue
		}
		for _, t := range splitTagSpec(spec) {
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

// MemoStats summarises the module for the sidebar.
type MemoStats struct {
	Total    int `json:"total"`
	Pinned   int `json:"pinned"`
	Archived int `json:"archived"`
	Today    int `json:"today"`
}

// MemoStats counts notes for the sidebar badges.
func (s *Store) MemoStats() MemoStats {
	var st MemoStats
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM memos WHERE archived = 0`).Scan(&st.Total)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM memos WHERE archived = 0 AND pinned = 1`).Scan(&st.Pinned)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM memos WHERE archived = 1`).Scan(&st.Archived)
	today := time.Now().Format("2006-01-02")
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM memos WHERE archived = 0 AND substr(created_at,1,10) = ?`,
		today).Scan(&st.Today)
	return st
}

// ---- service API --------------------------------------------------------

// GetMemos lists notes for the manager view.
func (s *ClipService) GetMemos(filter MemoFilter) []Memo { return s.store.ListMemos(filter) }

// GetMemoTags lists tags in use.
func (s *ClipService) GetMemoTags() []string { return s.store.MemoTags() }

// GetMemoStats reports the badges for the sidebar.
func (s *ClipService) GetMemoStats() MemoStats { return s.store.MemoStats() }

// SaveMemo creates or updates a note. content may be empty when only toggling
// flags — the stored content is then reused.
func (s *ClipService) SaveMemo(m Memo) (Memo, error) {
	if strings.TrimSpace(m.Content) == "" && m.ID != "" {
		existing, ok := s.store.GetMemo(m.ID)
		if !ok {
			return Memo{}, errors.New("备忘不存在")
		}
		m.Content = existing.Content
	}
	saved, err := s.store.SaveMemo(m)
	if err != nil {
		return Memo{}, err
	}
	Logf(s.store.Dir(), "memo saved: id=%s tags=%d pinned=%v", saved.ID, len(saved.Tags), saved.Pinned)
	return saved, nil
}

// DeleteMemo removes a note and leaves a tombstone so peers drop theirs.
func (s *ClipService) DeleteMemo(id string) bool {
	if m, ok := s.store.GetMemo(id); ok && m.Hash != "" {
		s.store.AddTombstoneScoped("memo", m.Hash, nowUTC(), hostName())
	}
	return s.store.DeleteMemo(id)
}

// QuickMemo creates a note from a single line (the capture popup / tray entry).
func (s *ClipService) QuickMemo(content string) (Memo, error) {
	return s.SaveMemo(Memo{Content: content, Source: "manual"})
}

// SaveItemAsMemo turns a clipboard entry into a note.
func (s *ClipService) SaveItemAsMemo(itemID string) (Memo, error) {
	it, ok := s.store.Get(itemID)
	if !ok {
		return Memo{}, errors.New("条目不存在")
	}
	content := strings.TrimSpace(it.Text)
	if it.Kind == KindImage {
		// 图片必须进 memo_blobs 才能显示：直接写文件路径的话 webview 加载不出来
		data, err := s.store.LoadBlobByID(it.ID)
		if err != nil {
			return Memo{}, errors.New("这条图片记录没有可用的数据")
		}
		token, err := s.SaveMemoImage(encodeBase64(data))
		if err != nil {
			return Memo{}, err
		}
		alt := strings.TrimSpace(it.SourceApp)
		if alt == "" {
			alt = "图片"
		}
		content = "![" + alt + "](" + token + ")"
	}
	if content == "" {
		return Memo{}, errors.New("这条记录没有可写入的文本")
	}
	return s.SaveMemo(Memo{Content: content, Source: "clipboard", SourceApp: it.SourceApp})
}

// SnippetToMemo appends (or creates) a note from a snippet body.
func (s *ClipService) SnippetToMemo(snippetID string, memoID string) (Memo, error) {
	sp, ok := s.store.GetSnippet(snippetID)
	if !ok {
		return Memo{}, errors.New("片段不存在")
	}
	body := strings.TrimSpace(sp.Body)
	if body == "" {
		return Memo{}, errors.New("片段内容为空")
	}
	if memoID == "" {
		return s.SaveMemo(Memo{Content: body, Source: "snippet"})
	}
	existing, ok := s.store.GetMemo(memoID)
	if !ok {
		return Memo{}, errors.New("备忘不存在")
	}
	existing.Content = existing.Content + "\n\n" + body
	return s.SaveMemo(existing)
}

// MemoFromClipboard turns whatever text is on the clipboard right now into a
// note. This is the tray "记一笔" entry: no window, no focus stealing.
func (s *ClipService) MemoFromClipboard() (Memo, error) {
	cap, err := captureClipboard()
	if err != nil || cap == nil {
		return Memo{}, errors.New("读不到剪贴板")
	}
	text := strings.TrimSpace(cap.Text)
	if text == "" {
		return Memo{}, errors.New("剪贴板里没有文本")
	}
	return s.SaveMemo(Memo{Content: text, Source: "clipboard"})
}
// ---- 同步支持 ------------------------------------------------------------

// findMemoByHash looks a memo up by its content hash (the sync pairing key).
func (s *Store) findMemoByHash(hash string) (Memo, bool) {
	if hash == "" {
		return Memo{}, false
	}
	row := s.db.QueryRow(`SELECT `+memoColumns+` FROM memos WHERE hash = ?`, hash)
	m, err := scanMemo(row)
	if err != nil {
		return Memo{}, false
	}
	return m, true
}

// UpsertRemoteMemo merges a memo arriving from a peer (paired by hash).
func (s *Store) UpsertRemoteMemo(in Memo) (isNew bool, changed bool) {
	if in.Hash == "" || strings.TrimSpace(in.Content) == "" {
		return false, false
	}
	local, ok := s.findMemoByHash(in.Hash)
	if ok {
		if in.UpdatedAt.After(local.UpdatedAt) {
			_, _ = s.db.Exec(`UPDATE memos SET content=?, tags=?, updated_at=? WHERE id=?`,
				in.Content, TagSpec(ExtractTags(in.Content)), encodeTime(in.UpdatedAt), local.ID)
			changed = true
		}
		if in.Pinned && !local.Pinned {
			_, _ = s.db.Exec(`UPDATE memos SET pinned = 1 WHERE id = ?`, local.ID)
			changed = true
		}
		if in.Archived && !local.Archived {
			_, _ = s.db.Exec(`UPDATE memos SET archived = 1 WHERE id = ?`, local.ID)
			changed = true
		}
		if merged, ok := s.GetMemo(local.ID); ok {
			_ = s.indexMemo(merged)
		}
		return false, changed
	}
	sp := in
	if sp.ID == "" {
		sp.ID = newID()
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = nowUTC()
	}
	if sp.UpdatedAt.IsZero() {
		sp.UpdatedAt = sp.CreatedAt
	}
	if _, err := s.db.Exec(`
		INSERT INTO memos (id, content, tags, pinned, archived, source, source_app, hash,
		                   created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		sp.ID, sp.Content, TagSpec(ExtractTags(sp.Content)), boolInt(sp.Pinned), boolInt(sp.Archived),
		sp.Source, sp.SourceApp, sp.Hash, encodeTime(sp.CreatedAt), encodeTime(sp.UpdatedAt),
	); err != nil {
		return false, false
	}
	_ = s.indexMemo(sp)
	return true, true
}

// DeleteMemoByHash drops a memo because a peer deleted it.
func (s *Store) DeleteMemoByHash(hash string) bool {
	local, ok := s.findMemoByHash(hash)
	if !ok {
		return false
	}
	return s.DeleteMemo(local.ID)
}

// ImportMemo inserts a memo with explicit timestamps (used by the Memos importer,
// which must preserve createTime/updateTime).
func (s *Store) ImportMemo(in Memo) (Memo, error) {
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return Memo{}, errors.New("内容为空")
	}
	in.ID = newID()
	in.Hash = memoHash(in.Content)
	in.Tags = ExtractTags(in.Content)
	if in.CreatedAt.IsZero() {
		in.CreatedAt = nowUTC()
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = in.CreatedAt
	}
	if in.Source == "" {
		in.Source = "import"
	}
	if _, err := s.db.Exec(`
		INSERT INTO memos (id, content, tags, pinned, archived, source, source_app, hash,
		                   created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		in.ID, in.Content, TagSpec(in.Tags), boolInt(in.Pinned), boolInt(in.Archived),
		in.Source, in.SourceApp, in.Hash, encodeTime(in.CreatedAt), encodeTime(in.UpdatedAt),
	); err != nil {
		return Memo{}, err
	}
	_ = s.indexMemo(in)
	return in, nil
}

// memoManifest renders memos for the sync package index.
func (s *Store) memoManifest() []manifestItem {
	rows, err := s.db.Query(`SELECT ` + memoColumns + ` FROM memos ORDER BY updated_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]manifestItem, 0, 32)
	for rows.Next() {
		m, err := scanMemo(rows)
		if err != nil {
			return out
		}
		out = append(out, manifestItem{
			Hash:      m.Hash,
			ID:        m.ID,
			Kind:      KindText,
			Preview:   truncateRunes(strings.ReplaceAll(m.Content, "\n", " "), 120),
			UpdatedAt: m.UpdatedAt,
			Pinned:    m.Pinned,
			CopyCount: 0,
		})
	}
	return out
}

// AllMemos returns every memo (used by sync and export).
func (s *Store) AllMemos() []Memo {
	rows, err := s.db.Query(`SELECT ` + memoColumns + ` FROM memos ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]Memo, 0, 64)
	for rows.Next() {
		m, err := scanMemo(rows)
		if err != nil {
			return out
		}
		out = append(out, m)
	}
	return out
}
// EnsureCaptureHotkey repairs the one-time damage done by an older build whose
// defaults lacked the field: such a profile stored captureHotkey="" which means
// "disabled", so the shortcut silently never registered. We only heal once (a
// later deliberate clear is respected).
func (s *ClipService) EnsureCaptureHotkey() {
	if s.store.Meta("capture.hotkeyInit") != "" {
		return
	}
	st := s.store.Settings()
	if st.CaptureHotkey == "" {
		st.CaptureHotkey = DefaultSettings().CaptureHotkey
		if err := s.store.SaveSettings(st); err != nil {
			Logf(s.store.Dir(), "capture hotkey heal failed: %v", err)
		} else {
			Logf(s.store.Dir(), "capture hotkey healed to %q (older profile had it empty)", st.CaptureHotkey)
		}
	}
	_ = s.store.SetMeta("capture.hotkeyInit", "1")
}