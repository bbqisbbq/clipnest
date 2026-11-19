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

// 统一条目模型（Entry）：字段、派生方法（预览/动作/标签）、动作与标签的序列化，
// 外加采集入库与生命周期（SaveCapture / 复制计数 / 清空 / 文件引用刷新）。
// 不负责：SQL 行映射与读写（entry_store.go）、查询与统计（entry_query.go）、
// 附件字节存取（entry_blob.go）。

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
	ID string `json:"id"`
	// Title 是给条目起的名字（现在只有隐写模式在写：`@名称@` 作为落点）。
	// 普通条目留空，卡片就不多一行标题。
	Title   string   `json:"title,omitempty"`
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

	// 待办（从正文的 - [ ] 派生，见 todo.go）：
	//   TodoState 落库（'' / open / done），收集页与筛选器按它过滤；
	//   TodoTotal / TodoOpen 只在内存里算，给卡片上的「2/5」用。
	// 派生数据不跨端传：mergeEntry 拿到远端条目后会本端重算。
	TodoState  string     `json:"todoState,omitempty"`
	TodoDoneAt *time.Time `json:"todoDoneAt,omitempty"`
	TodoTotal  int        `json:"todoTotal,omitempty"`
	TodoOpen   int        `json:"todoOpen,omitempty"`

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
//
// 两层去重都要：
//  1. 采集口径的 hash（c.Hash）—— 同一条内容被复制两次；
//  2. 内容口径的 hash（contentHash）—— 库里已经有同样文字的条目（典型是手写
//     备忘、导入内容，或者别的程序把它重新放回剪贴板）。缺了第 2 层就会
//     "同一条备忘变两条，其中一条来源是 clipnest"。
func (s *Store) SaveCapture(c Captured) (*Entry, bool, error) {
	if c.Hash != "" {
		if existing, ok := s.findEntryByHash(c.Hash); ok {
			return s.bumpSeen(existing.ID)
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
	// 内容已经在库里（哪怕它当初是手写进来的）就不再建第二条，只刷新时间
	if existing, ok := s.findEntryByHash(contentHash(content)); ok {
		return s.bumpSeen(existing.ID)
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

// bumpSeen marks an existing entry as "copied/seen again" without touching its
// content, source or created_at. Used by SaveCapture's de-dupe paths.
func (s *Store) bumpSeen(id string) (*Entry, bool, error) {
	if _, err := s.db.Exec(
		`UPDATE entries SET seen_count = seen_count + 1, updated_at = ? WHERE id = ?`,
		encodeTime(nowUTC()), id); err != nil {
		return nil, false, err
	}
	e, ok := s.GetEntry(id)
	if !ok {
		return nil, false, errors.New("条目不存在")
	}
	return &e, false, nil
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
