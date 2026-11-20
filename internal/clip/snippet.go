package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	SnippetImage    SnippetKind = "image"    // 图片（正文用 memoimg: 引用 entry_blobs）
	SnippetFile     SnippetKind = "file"     // 文件或文件夹路径
	SnippetURL      SnippetKind = "url"      // 网址
	SnippetApp      SnippetKind = "app"      // 启动程序（可带参数）
	SnippetCommand  SnippetKind = "command"  // 执行命令
	SnippetHTML     SnippetKind = "html"     // 富文本（HTML，走剪贴板）
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
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	UseCount  int         `json:"useCount"`
	Pinned    bool        `json:"pinned"`
	// ImageFile mirrors the item convention ("<id>.png") so the webview can ask
	// for a data URL without knowing about the blob table.
	ImageFile string `json:"imageFile,omitempty"`

	// ---- trigger behaviour ----
	// CaseSensitive makes "Addr" and "addr" different abbreviations.
	CaseSensitive bool `json:"caseSensitive"`
	// KeepTrigger inserts the phrase after the abbreviation instead of replacing it.
	KeepTrigger bool `json:"keepTrigger"`
	// Apps / ExcludeApps limit where the trigger fires (process image names).
	Apps        []string `json:"apps,omitempty"`
	ExcludeApps []string `json:"excludeApps,omitempty"`
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

// GetSnippet returns one snippet (an entry carrying the snippet action).
func (s *Store) GetSnippet(id string) (Snippet, bool) {
	e, ok := s.GetEntry(id)
	if !ok {
		return Snippet{}, false
	}
	return snippetFromEntry(e), true
}

// ListSnippets searches title/trigger/body/group, newest edit first.
// ListSnippets lists entries that carry the snippet action, newest first.
//
// 数据源是 entries：片段不再是独立表。注意这里按 action 查而不是按
// trigger_key 查——图片/文件类片段可以没有缩写，只能从列表里挑。
func (s *Store) ListSnippets(query, group string, limit int) []Snippet {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	entries := s.ListEntries(EntryQuery{
		Action: ActionSnippet,
		Limit:  limit * 2,
		Tail:   true,
	})
	needle := strings.ToLower(strings.TrimSpace(query))
	grp := strings.TrimSpace(group)
	out := make([]Snippet, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- { // Tail 返回正序，这里要最新在前
		e := entries[i]
		sp := snippetFromEntry(e)
		if grp != "" && sp.Group != grp {
			continue
		}
		if needle != "" {
			hay := strings.ToLower(sp.Title + " " + sp.Trigger + " " + sp.Body + " " + sp.Group)
			if !strings.Contains(hay, needle) {
				continue
			}
		}
		out = append(out, sp)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// AllSnippets returns every entry that carries the snippet action, adapted to
// the Snippet shape the trigger engine already understands.
//
// 片段不再是独立的一张表：它就是带 snippet action 的条目。保留 Snippet 类型
// 只是作为引擎侧的适配层（expand.Trigger 的字段与之对应），数据源是 entries。
func (s *Store) AllSnippets() []Snippet {
	entries := s.EntryTriggers()
	out := make([]Snippet, 0, len(entries))
	for _, e := range entries {
		out = append(out, snippetFromEntry(e))
	}
	return out
}

// snippetFromEntry adapts an entry to the engine's Snippet view.
// snippetKindOf maps an entry kind onto the snippet kind the expander understands.
//
// 两套枚举**不是一一对应**：条目是 text / link / image / files / rich / custom，
// 片段是 text / password / url / app / command / file / image / html。
// 直接 `SnippetKind(e.Kind)` 强转会让链接、富文本、文件引用、私有格式的片段在
// Render 里落到 default 分支 → 返回空字符串，引擎就记 "produced no text"，
// 用户看到的是"设成片段了但打缩写没反应"（2026-11-19 修）。
func snippetKindOf(k Kind) SnippetKind {
	switch k {
	case KindLink:
		return SnippetURL
	case KindRichText:
		return SnippetHTML
	case KindFiles:
		return SnippetFile
	case KindImage:
		return SnippetImage
	default:
		// text / custom（私有格式的兜底文本）都按纯文本展开
		return SnippetText
	}
}

// snippetKindOfEntry recovers a snippet's execution kind. 2.8.3 起它单独存一列
// （entries.snippet_kind）；旧数据把片段类型直接写进了 entries.kind，这里照原样
// 认回来，免得升级后「命令片段」退化成「粘贴文本」。
func snippetKindOfEntry(e Entry) SnippetKind {
	if k := strings.TrimSpace(e.SnippetKind); k != "" {
		return SnippetKind(k)
	}
	switch k := SnippetKind(e.Kind); k {
	case SnippetPassword, SnippetApp, SnippetCommand, SnippetHTML:
		return k
	}
	return snippetKindOf(e.Kind)
}

// kindOfSnippet is the inverse: what the body *is*, for the kind column, the
// filters and the attachment extension.
func kindOfSnippet(k SnippetKind) Kind {
	switch k {
	case SnippetImage:
		return KindImage
	case SnippetFile:
		return KindFiles
	case SnippetURL:
		return KindLink
	case SnippetHTML:
		return KindRichText
	default: // text / password / app / command
		return KindText
	}
}

func snippetFromEntry(e Entry) Snippet {
	title := strings.TrimSpace(e.Title)
	if title == "" {
		title = firstLineOf(e.Content)
	}
	return Snippet{
		ID:            e.ID,
		Kind:          snippetKindOfEntry(e),
		Title:         title,
		Hash:          e.Hash,
		Trigger:       e.TriggerKey,
		Group:         e.GroupName,
		Body:          e.Content,
		Pinned:        e.Pinned,
		CaseSensitive: e.CaseSensitive,
		KeepTrigger:   e.KeepTrigger,
		Apps:          e.Apps,
		ExcludeApps:   e.ExcludeApps,
		UseCount:      e.UseCount,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
		ImageFile:     memoImageToken(e.Content),
	}
}

// memoImageToken pulls "memoimg:<hash>" out of an entry body ("" when absent).
func memoImageToken(content string) string {
	i := strings.Index(content, memoImagePrefix)
	if i < 0 {
		return ""
	}
	rest := content[i:]
	if j := strings.IndexAny(rest, ") \n"); j > 0 {
		return rest[:j]
	}
	return rest
}

func firstLineOf(s string) string {
	line := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	r := []rune(line)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return line
}

// SnippetGroups lists the distinct group names.
// SnippetGroups lists the distinct groups of snippet entries.
func (s *Store) SnippetGroups() []string {
	rows, err := s.db.Query(`SELECT DISTINCT group_name FROM entries
		WHERE group_name <> '' AND archived = 0 ORDER BY group_name`)
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

// FindSnippetByTrigger returns the snippet whose trigger equals key.
func (s *Store) FindSnippetByTrigger(key string) (Snippet, bool) {
	key = NormalizeTrigger(key)
	if key == "" {
		return Snippet{}, false
	}
	e, ok := s.FindEntryByTrigger(key, false)
	if !ok {
		return Snippet{}, false
	}
	return snippetFromEntry(e), true
}

// DeleteSnippet removes a snippet entry and records a tombstone.

// MarkSnippetUsed bumps the use counter of a snippet entry.
func (s *Store) MarkSnippetUsed(id string) {
	_, _ = s.db.Exec(`UPDATE entries SET use_count = use_count + 1 WHERE id = ?`, id)
}

// UpsertRemoteSnippet merges a snippet arriving from a peer (by hash).

// 片段现在落在 entries：同步也走同一条路，否则拉下来的片段看不见

// snippetBlobExt is the payload extension a snippet carries ("" when none).

// ---- service API --------------------------------------------------------

// GetSnippets returns snippets for the manager view / picker.

// GetSnippetGroups lists distinct groups.

// DeleteSnippet removes one snippet.

// GetSnippetImage returns a data URL for an image snippet.

// 片段图片现在存在 entry_blobs 里，正文用 memoimg: 引用

// SnippetAction reports what Enter will do for this snippet — the UI shows it so
// "copy" and "launch" never get confused.

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

// splitList parses a comma-separated process list stored in SQLite.
func splitList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// joinList renders a process list for storage.
func joinList(list []string) string {
	cleaned := make([]string, 0, len(list))
	for _, p := range list {
		if t := strings.TrimSpace(p); t != "" {
			cleaned = append(cleaned, t)
		}
	}
	return strings.Join(cleaned, ",")
}

// SaveSnippet creates or updates a snippet. Image snippets carry their PNG in
// imageDataB64 (base64), because the webview cannot hand us a file handle.
// 片段表已废弃：片段就是带 snippet 能力的条目，这里做一次形状转换。
func (s *ClipService) SaveSnippet(sp Snippet, imageDataB64 string) (Snippet, error) {
	content := sp.Body
	if strings.TrimSpace(imageDataB64) != "" && sp.Kind == SnippetImage {
		raw, decErr := decodeBase64Image(imageDataB64)
		if decErr != nil {
			return Snippet{}, fmt.Errorf("图片数据无效: %w", decErr)
		}
		token, err := s.store.SaveEntryBlob(raw, "png")
		if err != nil {
			return Snippet{}, err
		}
		content = "![](" + token + ")"
	}
	hash := snippetHash(sp.Kind, strings.TrimSpace(sp.Trigger), content)
	// 同一份内容+缩写只存一条：先按 hash 找已存在的条目
	id := sp.ID
	if id == "" {
		if existing, ok := s.store.findEntryByHash(hash); ok {
			id = existing.ID
		}
	}
	e := Entry{
		ID:            id,
		Content:       content,
		Kind:          kindOfSnippet(sp.Kind),
		Source:        SourceManual,
		Actions:       []string{ActionSnippet},
		Title:         strings.TrimSpace(sp.Title),
		Pinned:        sp.Pinned,
		TriggerKey:    strings.TrimSpace(sp.Trigger),
		CaseSensitive: sp.CaseSensitive,
		KeepTrigger:   sp.KeepTrigger,
		Apps:          sp.Apps,
		ExcludeApps:   sp.ExcludeApps,
		GroupName:     sp.Group,
		UseCount:      sp.UseCount,
		SnippetKind:   string(sp.Kind),
		Hash:          hash,
		CreatedAt:     sp.CreatedAt,
		UpdatedAt:     sp.UpdatedAt,
	}
	saved, err := s.store.SaveEntry(e)
	if err != nil {
		return Snippet{}, err
	}
	s.refreshTriggers()
	return snippetFromEntry(saved), nil
}
