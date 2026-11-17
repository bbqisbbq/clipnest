package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snippets ("短语") are user-authored entries: text you reuse, an image, a path,
// a link, a program to launch or a command to run. Each one can carry a trigger
// (`addr-`) that expands inline, and — unlike clipboard history — snippets are
// first-class content: they have titles, groups and actions.

// SnippetKind describes what the body means and which action applies.
type SnippetKind string

const (
	SnippetText     SnippetKind = "text"     // 文本 / Markdown
	SnippetPassword SnippetKind = "password" // 敏感文本（掩码显示，但仍参与同步）
	SnippetImage    SnippetKind = "image"    // 图片（存在 snippet_blobs）
	SnippetFile     SnippetKind = "file"     // 文件或文件夹路径
	SnippetURL      SnippetKind = "url"      // 网址
	SnippetApp      SnippetKind = "app"      // 启动程序（可带参数）
	SnippetCommand  SnippetKind = "command"  // 执行命令
)

// Snippet is one reusable entry.
type Snippet struct {
	ID        string      `json:"id"`
	Kind      SnippetKind `json:"kind"`
	Title     string      `json:"title"`
	Trigger   string      `json:"trigger,omitempty"`
	Group     string      `json:"group,omitempty"`
	Body      string      `json:"body"`
	Hash      string      `json:"hash"`
	Size      int         `json:"size"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	UseCount  int         `json:"useCount"`
	Pinned    bool        `json:"pinned"`
	// ImageFile mirrors the item convention ("<id>.png") so the webview can ask
	// for a data URL without knowing about the blob table.
	ImageFile string `json:"imageFile,omitempty"`
}

// snippetHash keys a snippet by its meaning, so the same phrase saved on two
// machines (or twice locally) merges into one entry.
func snippetHash(kind SnippetKind, trigger, body string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", kind, strings.TrimSpace(trigger), body)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// NormalizeTrigger lowercases and trims a trigger; triggers are matched
// case-insensitively against what the user types.
func NormalizeTrigger(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// ---- store --------------------------------------------------------------

// snippetColumns must match scanSnippet. The blob extension rides along in the
// same row: the store uses a single connection, so querying it per row while the
// result set is open would deadlock.
const snippetColumns = `s.id, s.kind, s.title, s.trigger_key, s.group_name, s.body, s.hash,
	s.size, s.created_at, s.updated_at, s.use_count, s.pinned, COALESCE(b.ext, '')`

const selectSnippets = `SELECT ` + snippetColumns +
	` FROM snippets s LEFT JOIN snippet_blobs b ON b.snippet_id = s.id`

func scanSnippet(sc scannable) (Snippet, error) {
	var (
		sp                   Snippet
		kind                 string
		createdAt, updatedAt string
		pinned               int
		blobExt              string
	)
	if err := sc.Scan(&sp.ID, &kind, &sp.Title, &sp.Trigger, &sp.Group, &sp.Body, &sp.Hash,
		&sp.Size, &createdAt, &updatedAt, &sp.UseCount, &pinned, &blobExt); err != nil {
		return Snippet{}, err
	}
	sp.Kind = SnippetKind(kind)
	sp.CreatedAt = decodeTime(createdAt)
	sp.UpdatedAt = decodeTime(updatedAt)
	sp.Pinned = pinned != 0
	if blobExt != "" {
		sp.ImageFile = sp.ID + blobExt
	}
	return sp, nil
}


// SaveSnippet inserts or updates a snippet (matched by id, then by content hash).
func (s *Store) SaveSnippet(in Snippet) (Snippet, error) {
	in.Kind = SnippetKind(strings.TrimSpace(string(in.Kind)))
	if in.Kind == "" {
		in.Kind = SnippetText
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Trigger = strings.TrimSpace(in.Trigger)
	in.Group = strings.TrimSpace(in.Group)
	if in.Body == "" && in.Kind != SnippetImage {
		return Snippet{}, errors.New("内容不能为空")
	}
	if in.Title == "" {
		in.Title = snippetDefaultTitle(in)
	}
	in.Hash = snippetHash(in.Kind, in.Trigger, in.Body)
	in.Size = len(in.Body)
	now := nowUTC()

	// Same meaning already stored under another id? Update that row instead of
	// creating a duplicate.
	if in.ID == "" {
		var existing string
		if err := s.db.QueryRow(`SELECT id FROM snippets WHERE hash = ?`, in.Hash).Scan(&existing); err == nil {
			in.ID = existing
		}
	}

	if in.ID == "" {
		in.ID = newID()
		in.CreatedAt = now
		in.UpdatedAt = now
		if _, err := s.db.Exec(`
			INSERT INTO snippets (id, kind, title, trigger_key, group_name, body, hash, size,
			                      created_at, updated_at, use_count, pinned)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			in.ID, string(in.Kind), in.Title, in.Trigger, in.Group, in.Body, in.Hash, in.Size,
			encodeTime(in.CreatedAt), encodeTime(in.UpdatedAt), in.UseCount, boolInt(in.Pinned),
		); err != nil {
			return Snippet{}, err
		}
		out, ok := s.GetSnippet(in.ID)
		if !ok {
			return Snippet{}, errors.New("保存后读取失败")
		}
		return out, nil
	}

	if _, err := s.db.Exec(`
		UPDATE snippets SET kind=?, title=?, trigger_key=?, group_name=?, body=?, hash=?,
		                    size=?, updated_at=?
		 WHERE id=?`,
		string(in.Kind), in.Title, in.Trigger, in.Group, in.Body, in.Hash, in.Size,
		encodeTime(now), in.ID,
	); err != nil {
		return Snippet{}, err
	}
	out, ok := s.GetSnippet(in.ID)
	if !ok {
		return Snippet{}, errors.New("片段不存在")
	}
	return out, nil
}

func snippetDefaultTitle(sp Snippet) string {
	switch sp.Kind {
	case SnippetURL:
		return sp.Body
	case SnippetImage:
		return "图片片段"
	case SnippetApp:
		return filepath.Base(strings.Fields(sp.Body + " ")[0])
	case SnippetCommand:
		return sp.Body
	case SnippetPassword:
		return "密码"
	}
	text := strings.TrimSpace(strings.SplitN(sp.Body, "\n", 2)[0])
	if r := []rune(text); len(r) > 24 {
		return string(r[:24]) + "…"
	}
	if text == "" {
		return "文本片段"
	}
	return text
}

// GetSnippet returns one snippet.
func (s *Store) GetSnippet(id string) (Snippet, bool) {
	row := s.db.QueryRow(selectSnippets+` WHERE s.id = ?`, id)
	sp, err := scanSnippet(row)
	if err != nil {
		return Snippet{}, false
	}
	return sp, true
}

func (s *Store) querySnippets(query string, args ...any) []Snippet {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		Logf(s.dir, "snippet query failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]Snippet, 0, 32)
	for rows.Next() {
		sp, err := scanSnippet(rows)
		if err != nil {
			return out
		}
			out = append(out, sp)
	}
	return out
}

// ListSnippets searches title/trigger/body/group, newest edit first.
func (s *Store) ListSnippets(query, group string, limit int) []Snippet {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	where := make([]string, 0, 2)
	args := make([]any, 0, 6)
	if g := strings.TrimSpace(group); g != "" {
		where = append(where, "s.group_name = ?")
		args = append(args, g)
	}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + escapeLike(strings.ToLower(q)) + "%"
		where = append(where,
			`(lower(title) LIKE ? ESCAPE '\' OR lower(trigger_key) LIKE ? ESCAPE '\'`+
				` OR lower(body) LIKE ? ESCAPE '\' OR lower(group_name) LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like)
	}
	sql := selectSnippets
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += ` ORDER BY s.pinned DESC, s.updated_at DESC LIMIT ?`
	args = append(args, limit)
	return s.querySnippets(sql, args...)
}

// AllSnippets returns every snippet (used by sync).
func (s *Store) AllSnippets() []Snippet {
	return s.querySnippets(selectSnippets + ` ORDER BY s.updated_at DESC`)
}

// SnippetGroups lists the distinct group names.
func (s *Store) SnippetGroups() []string {
	rows, err := s.db.Query(`SELECT group_name FROM snippets WHERE group_name <> '' GROUP BY group_name ORDER BY group_name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil {
			out = append(out, g)
		}
	}
	return out
}

// FindSnippetByTrigger returns the snippet whose trigger equals key (case-insensitive).
func (s *Store) FindSnippetByTrigger(key string) (Snippet, bool) {
	key = NormalizeTrigger(key)
	if key == "" {
		return Snippet{}, false
	}
	row := s.db.QueryRow(selectSnippets+` WHERE lower(s.trigger_key) = ?`, key)
	sp, err := scanSnippet(row)
	if err != nil {
		return Snippet{}, false
	}
	return sp, true
}

// DeleteSnippet removes a snippet (and leaves a tombstone so peers drop theirs).
func (s *Store) DeleteSnippet(id string) bool {
	var hash string
	_ = s.db.QueryRow(`SELECT hash FROM snippets WHERE id = ?`, id).Scan(&hash)
	res, err := s.db.Exec(`DELETE FROM snippets WHERE id = ?`, id)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	_, _ = s.db.Exec(`DELETE FROM snippet_blobs WHERE snippet_id = ?`, id)
	if hash != "" {
		s.AddTombstoneScoped("snippet", hash, nowUTC(), hostName())
	}
	return true
}

// MarkSnippetUsed bumps the usage counter.
func (s *Store) MarkSnippetUsed(id string) {
	_, _ = s.db.Exec(`UPDATE snippets SET use_count = use_count + 1 WHERE id = ?`, id)
}

// SaveSnippetBlob stores an image payload for a snippet.
func (s *Store) SaveSnippetBlob(id, ext string, data []byte) error {
	_, err := s.db.Exec(
		`INSERT INTO snippet_blobs (snippet_id, ext, data) VALUES (?,?,?)
		 ON CONFLICT(snippet_id) DO UPDATE SET ext = excluded.ext, data = excluded.data`,
		id, ext, data)
	return err
}

// SnippetBlob returns the payload of an image snippet.
func (s *Store) SnippetBlob(id string) ([]byte, error) {
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM snippet_blobs WHERE snippet_id = ?`, id).Scan(&data); err != nil {
		return nil, err
	}
	return data, nil
}

// UpsertRemoteSnippet merges a snippet arriving from a peer (by hash).
func (s *Store) UpsertRemoteSnippet(in Snippet) (isNew bool, changed bool) {
	if in.Hash == "" {
		return false, false
	}
	var localID string
	var localUpdated string
	err := s.db.QueryRow(`SELECT id, updated_at FROM snippets WHERE hash = ?`, in.Hash).
		Scan(&localID, &localUpdated)
	if err == nil && localID != "" {
		if in.UpdatedAt.After(decodeTime(localUpdated)) {
			if _, err := s.db.Exec(
				`UPDATE snippets SET title=?, trigger_key=?, group_name=?, body=?, size=?, updated_at=? WHERE id=?`,
				in.Title, in.Trigger, in.Group, in.Body, in.Size, encodeTime(in.UpdatedAt), localID,
			); err != nil {
				return false, false
			}
			changed = true
		}
		var use int
		var pinned int
		_ = s.db.QueryRow(`SELECT use_count, pinned FROM snippets WHERE id = ?`, localID).Scan(&use, &pinned)
		if in.UseCount > use {
			_, _ = s.db.Exec(`UPDATE snippets SET use_count = ? WHERE id = ?`, in.UseCount, localID)
			changed = true
		}
		if in.Pinned && pinned == 0 {
			_, _ = s.db.Exec(`UPDATE snippets SET pinned = 1 WHERE id = ?`, localID)
			changed = true
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
		sp.UpdatedAt = nowUTC()
	}
	if _, err := s.db.Exec(`
		INSERT INTO snippets (id, kind, title, trigger_key, group_name, body, hash, size,
		                      created_at, updated_at, use_count, pinned)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		sp.ID, string(sp.Kind), sp.Title, sp.Trigger, sp.Group, sp.Body, sp.Hash, sp.Size,
		encodeTime(sp.CreatedAt), encodeTime(sp.UpdatedAt), sp.UseCount, boolInt(sp.Pinned),
	); err != nil {
		return false, false
	}
	return true, true
}

// findSnippetByHash looks a snippet up by its merge key.
func (s *Store) findSnippetByHash(hash string) (Snippet, bool) {
	if hash == "" {
		return Snippet{}, false
	}
	row := s.db.QueryRow(selectSnippets+` WHERE s.hash = ?`, hash)
	sp, err := scanSnippet(row)
	if err != nil {
		return Snippet{}, false
	}
	return sp, true
}

// snippetBlobExt is the payload extension a snippet carries ("" when none).
func snippetBlobExt(sp Snippet) string {
	if sp.ImageFile != "" {
		return ".png"
	}
	return ""
}

// snippetManifest renders snippets for the sync package index.
func (s *Store) snippetManifest() []manifestItem {
	all := s.AllSnippets()
	out := make([]manifestItem, 0, len(all))
	for _, sp := range all {
		ext := ""
		if sp.ImageFile != "" {
			ext = filepath.Ext(sp.ImageFile)
		}
		out = append(out, manifestItem{
			Hash:      sp.Hash,
			ID:        sp.ID,
			Kind:      Kind(sp.Kind),
			Preview:   truncateRunes(sp.Title, 120),
			UpdatedAt: sp.UpdatedAt,
			Blob:      ext,
			Pinned:    sp.Pinned,
			CopyCount: sp.UseCount,
		})
	}
	return out
}

// ---- service API --------------------------------------------------------

// GetSnippets returns snippets for the manager view / picker.
func (s *ClipService) GetSnippets(query, group string) []Snippet {
	return s.store.ListSnippets(query, group, 300)
}

// GetSnippetGroups lists distinct groups.
func (s *ClipService) GetSnippetGroups() []string { return s.store.SnippetGroups() }

// SaveSnippet creates or updates a snippet. Image snippets carry their PNG in
// imageDataB64 (base64), because the webview cannot hand us a file handle.
func (s *ClipService) SaveSnippet(sp Snippet, imageDataB64 string) (Snippet, error) {
	saved, err := s.store.SaveSnippet(sp)
	if err != nil {
		return Snippet{}, err
	}
	if strings.TrimSpace(imageDataB64) != "" && saved.Kind == SnippetImage {
		raw, decErr := decodeBase64Image(imageDataB64)
		if decErr != nil {
			return saved, fmt.Errorf("图片数据无效: %w", decErr)
		}
		if err := s.store.SaveSnippetBlob(saved.ID, ".png", raw); err != nil {
			return saved, err
		}
		saved.ImageFile = saved.ID + ".png"
	}
	s.refreshTriggerIndex()
	out, ok := s.store.GetSnippet(saved.ID)
	if !ok {
		return Snippet{}, errors.New("片段保存后读取失败")
	}
	return out, nil
}

// DeleteSnippet removes one snippet.
func (s *ClipService) DeleteSnippet(id string) bool {
	ok := s.store.DeleteSnippet(id)
	if ok {
		s.refreshTriggerIndex()
	}
	return ok
}

// GetSnippetImage returns a data URL for an image snippet.
func (s *ClipService) GetSnippetImage(id string) (string, error) {
	payload, err := s.store.SnippetBlob(id)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + encodeBase64(payload), nil
}

// SnippetAction reports what Enter will do for this snippet — the UI shows it so
// "copy" and "launch" never get confused.
func SnippetAction(sp Snippet) string {
	switch sp.Kind {
	case SnippetURL:
		return "打开网址"
	case SnippetApp:
		return "启动程序"
	case SnippetCommand:
		return "执行命令"
	case SnippetFile:
		if strings.HasSuffix(sp.Body, `\`) || isDirPath(sp.Body) {
			return "打开文件夹"
		}
		return "复制文件"
	case SnippetImage:
		return "复制图片"
	case SnippetPassword:
		return "复制密码"
	}
	return "复制文本"
}

func isDirPath(p string) bool {
	info, err := osStat(p)
	return err == nil && info.IsDir()
}

// sortedSnippetsForPicker returns snippets matching query with the trigger
// prefix ranked first, so typing "ad" surfaces "addr-".
func (s *Store) sortedSnippetsForPicker(query string, limit int) []Snippet {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []Snippet
	if q == "" {
		out = s.ListSnippets("", "", limit)
	} else {
		out = s.ListSnippets(q, "", limit)
		sort.SliceStable(out, func(i, j int) bool {
			return triggerScore(out[i], q) < triggerScore(out[j], q)
		})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func triggerScore(sp Snippet, q string) int {
	t := strings.ToLower(sp.Trigger)
	switch {
	case t != "" && t == q:
		return 0
	case t != "" && strings.HasPrefix(t, q):
		return 1
	case strings.Contains(strings.ToLower(sp.Title), q):
		return 2
	}
	return 3
}
