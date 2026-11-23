package clip

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// 统一条目的对外接口 + 用户自定义筛选器。
// 侧栏的每个入口（我写的 / 剪贴板 / 片段 / 图片 …）都是一个保存下来的 EntryQuery。

// EntryFilter is a saved query: the sidebar entry the user clicks.
type EntryFilter struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Icon      string     `json:"icon"`
	Query     EntryQuery `json:"query"`
	SortOrder int        `json:"sortOrder"`
	Builtin   bool       `json:"builtin"` // 代码内置：不许改成另一套规则
	// Deletable 允许从侧栏移除（内置里只有「置顶」可以：它完全能用自定义规则重现）。
	// 「全部内容」等其余内置项不可删 —— 它们是系统能稳定识别的维度，也是删光后的落地视图。
	Deletable bool `json:"deletable"`
}

// BuiltinFilters are the default entry points. They are code, not rows, so a
// fresh install always has a usable sidebar; user edits are stored as overrides.
//
// 「剪贴板」按**来源**（source）而不是 capabilities（actions）划分：它和「我写的」是一对
// 互补的入口，两边加起来就是全部内容。原先按 `Action: clipboard`（"出现在快速粘贴列表"）
// 筛，实测就出现了 我写的 3 + 剪贴板 2 ≠ 全部 6（actions 列在老数据上是空的）。
// 顺带：`EntryStats` 直接复用这里的 Query，角标与列表因此不会再漂。
func BuiltinFilters() []EntryFilter {
	return []EntryFilter{
		{ID: "all", Name: "全部内容", Icon: "all", Builtin: true, Query: EntryQuery{}},
		{ID: "mine", Name: "我写的", Icon: "memo", Builtin: true, Query: EntryQuery{Source: string(SourceManual)}},
		{ID: "clipboard", Name: "剪贴板", Icon: "pin", Builtin: true, Query: EntryQuery{Source: string(SourceClipboard)}},
		{ID: "snippets", Name: "片段", Icon: "snippet", Builtin: true, Query: EntryQuery{Snippets: true}},
		{ID: "images", Name: "图片", Icon: "image", Builtin: true, Query: EntryQuery{Kind: string(KindImage)}},
		{ID: "pinned", Name: "置顶", Icon: "star", Builtin: true, Deletable: true, Query: EntryQuery{Pinned: true}},
		{ID: "archived", Name: "已归档", Icon: "folder", Builtin: true, Query: EntryQuery{Archived: true}},
	}
}

// ---- 条目 API ------------------------------------------------------------

// GetEntries is the single list call for the whole app.
func (s *ClipService) GetEntries(q EntryQuery) []Entry {
	return s.store.ListEntries(q)
}

// GetEntry returns one entry by id. The 待办收集页 needs it to jump from a task
// row to the entry that owns it (that row only carries a summary).
func (s *ClipService) GetEntry(id string) (Entry, error) {
	if e, ok := s.store.GetEntry(id); ok {
		return e, nil
	}
	return Entry{}, errors.New("条目不存在")
}

// GetEntryStats reports the counts shown next to each filter.
func (s *ClipService) GetEntryStats() EntryStats { return s.store.EntryStats() }

// GetEntryTags lists tags in use (content tags + system tags).

// SaveEntry creates or updates an entry. Empty content is allowed when only
// flags change — the stored content is reused.
func (s *ClipService) SaveEntry(e Entry) (Entry, error) {
	if strings.TrimSpace(e.Content) == "" && e.ID != "" {
		if existing, ok := s.store.GetEntry(e.ID); ok {
			e.Content = existing.Content
			e.Kind = existing.Kind
		}
	}
	saved, err := s.store.SaveEntry(e)
	if err != nil {
		return saved, err
	}
	// 缩写在这次保存里被设置/改掉/清掉，都要重建内存里的触发器索引，
	// 否则「设为片段」要重启才生效（见 refreshTriggers 的说明）。
	s.refreshTriggers()
	// **改完要让界面知道**：侧栏那几个数字（我写的 / 片段 / 置顶 / 已归档）全部靠
	// 收到事件才重取（App.tsx 订阅 clip:new/bumped/updated/deleted/cleared/stats）。
	// 以前这里的写路径一个事件都不发，于是"手写几条 / 置顶 / 归档"之后角标一直不动，
	// 数字与实际条数长期对不上。
	s.emit("clip:updated", saved)
	s.emit("clip:stats", s.store.Stats())
	return saved, nil
}

// DeleteEntry removes an entry (the store records the tombstone peers need).
//
// 返回 error 而不是 bool：删除有好几种失败（条目已不存在、SQL 出错），bool 把它们
// 压成一个 false，前端只能猜原因。墓碑由 store.DeleteEntry 自己写，这里不再重复写。
func (s *ClipService) DeleteEntry(id string) error {
	if _, ok := s.store.GetEntry(id); !ok {
		return errors.New("条目不存在")
	}
	if !s.store.DeleteEntry(id) {
		return errors.New("删除条目失败")
	}
	// 删掉的可能是片段：索引里不能留着它
	s.refreshTriggers()
	// 主窗口与快速粘贴窗都订阅这个事件：在 picker 里删掉一条，主列表也要跟着少一条，
	// 否则点进去只会得到"条目不存在"。
	s.emit("clip:deleted", id)
	s.emit("clip:stats", s.store.Stats())
	return nil
}

// SetEntryPinned flips the pin flag. The quick-paste list only carries ids, so
// patching a flag must not require the caller to hand back a whole Entry (that
// is how tags and triggers get silently reset).
func (s *ClipService) SetEntryPinned(id string, pinned bool) (Entry, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	e.Pinned = pinned
	// 走服务层 SaveEntry：置顶/取消置顶会改「置顶」那个角标，必须发事件。
	return s.SaveEntry(e)
}

// SetEntryArchived flips the archive flag (same reasoning as SetEntryPinned).
func (s *ClipService) SetEntryArchived(id string, archived bool) (Entry, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	e.Archived = archived
	// 归档会把条目从可展开的片段里摘掉（EntryTriggers 只看未归档的）
	return s.SaveEntry(e)
}

// SnippetFields 是片段设置里那些"只属于片段"的字段。
type SnippetFields struct {
	Trigger       string   `json:"trigger"`
	CaseSensitive bool     `json:"caseSensitive"`
	KeepTrigger   bool     `json:"keepTrigger"`
	Apps          []string `json:"apps"`
	ExcludeApps   []string `json:"excludeApps"`
	GroupName     string   `json:"groupName"`
}

// SetSnippetFields updates only the snippet settings of one entry.
//
// 详情页以前是 `SaveEntry({...entry, ...patch})`：拿一份读到的旧快照整行回写，
// 内容、标签、置顶全跟着回滚（连同步刚拉回来的对端修改一起）。这个接口改哪几列
// 就写哪几列，其余保持库里当前的样子。
func (s *ClipService) SetSnippetFields(id string, f SnippetFields) (Entry, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	e.TriggerKey = strings.TrimSpace(f.Trigger)
	e.CaseSensitive = f.CaseSensitive
	e.KeepTrigger = f.KeepTrigger
	e.Apps = f.Apps
	e.ExcludeApps = f.ExcludeApps
	e.GroupName = strings.TrimSpace(f.GroupName)
	return s.SaveEntry(e)
}

// PromoteToSnippet turns any entry into an abbreviation-triggered snippet.
func (s *ClipService) PromoteToSnippet(id, trigger string) (Entry, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		return Entry{}, errors.New("缩写不能为空")
	}
	e.TriggerKey = trigger
	e.SetAction(ActionSnippet, true)
	return s.SaveEntry(e)
}

// DemoteFromSnippet removes the trigger but keeps the content and the tag.
func (s *ClipService) DemoteFromSnippet(id string) (Entry, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	e.TriggerKey = ""
	e.SetAction(ActionSnippet, false)
	return s.SaveEntry(e)
}

// PromoteToManual keeps a collected entry from expiring (it becomes "mine").

// ---- 待办 API ------------------------------------------------------------

// GetTodos expands every entry matching q into individual task rows, for the
// 待办 collection page (未完成在前)。
func (s *ClipService) GetTodos(q EntryQuery) []TodoRef { return s.store.ListTodos(q) }

// SetTodoDone flips one task line inside an entry. The entry is re-read and
// saved back whole, so tags / trigger / hash are never reset by a checkbox click.
func (s *ClipService) SetTodoDone(entryID, key string, done bool) (Entry, error) {
	e, err := s.store.SetTodoDone(entryID, key, done)
	if err != nil {
		return e, err
	}
	// 待办角标是"还差几件事"，勾选一件就得动。
	s.emit("clip:updated", e)
	s.emit("clip:stats", s.store.Stats())
	return e, nil
}

// ---- 筛选器 API ----------------------------------------------------------

// filterOverrides holds user edits to builtin filters and user-added filters.
// Stored as rows so it survives restarts and syncs with settings later.
type filterRow struct {
	ID        string
	Name      string
	Icon      string
	Query     string
	SortOrder int
}

// GetFilters returns builtins (with user overrides applied) plus user filters.
// Builtins the user removed (只有「置顶」能删) are filtered out via meta.
func (s *ClipService) GetFilters() []EntryFilter {
	hidden := s.hiddenFilterIDs()
	builtins := make([]EntryFilter, 0, 8)
	for _, f := range BuiltinFilters() {
		if hidden[f.ID] {
			continue
		}
		builtins = append(builtins, f)
	}
	byID := map[string]int{}
	for i, f := range builtins {
		byID[f.ID] = i
	}

	rows, err := s.store.db.Query(
		`SELECT id, name, icon, query, sort_order FROM entry_filters ORDER BY sort_order`)
	var user []EntryFilter
	if err == nil {
		for rows.Next() {
			var r filterRow
			if rows.Scan(&r.ID, &r.Name, &r.Icon, &r.Query, &r.SortOrder) != nil {
				continue
			}
			var q EntryQuery
			_ = json.Unmarshal([]byte(r.Query), &q)
			f := EntryFilter{ID: r.ID, Name: r.Name, Icon: r.Icon, Query: q, SortOrder: r.SortOrder, Deletable: true}
			// 内置 id 的行是"覆盖"，不是新入口
			if idx, ok := byID[f.ID]; ok {
				if f.Name != "" {
					builtins[idx].Name = f.Name
				}
				if f.Icon != "" {
					builtins[idx].Icon = f.Icon
				}
				builtins[idx].SortOrder = r.SortOrder
				continue
			}
			if hidden[f.ID] {
				continue
			}
			user = append(user, f)
		}
		rows.Close()
	}

	out := append(append([]EntryFilter{}, builtins...), user...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SortOrder == out[j].SortOrder {
			return false
		}
		return out[i].SortOrder < out[j].SortOrder
	})
	return out
}

// SaveFilter stores a new filter, or an override for a builtin one.
func (s *ClipService) SaveFilter(f EntryFilter) (EntryFilter, error) {
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		return EntryFilter{}, errors.New("名称不能为空")
	}
	if f.ID == "" {
		f.ID = newID()
	}
	// 规则里认不出的字段/算子会在编译时被丢弃，但保存时先提示一次，
	// 免得用户以为规则生效了却查不到东西。
	if err := validateRule(f.Query.Rule); err != nil {
		return EntryFilter{}, err
	}
	body, err := json.Marshal(f.Query)
	if err != nil {
		return EntryFilter{}, err
	}
	if _, err := s.store.db.Exec(`
		INSERT INTO entry_filters (id, name, icon, query, sort_order) VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, icon = excluded.icon,
			query = excluded.query, sort_order = excluded.sort_order`,
		f.ID, f.Name, f.Icon, string(body), f.SortOrder); err != nil {
		return EntryFilter{}, err
	}
	// 改回来的内置筛选器要重新露出来
	s.unhideFilter(f.ID)
	Logf(s.store.Dir(), "filter saved: %s (%s)", f.ID, f.Name)
	return f, nil
}

// 返回 error：不可删除的内置筛选器与"本来就没了"是两件事，前端要分开提示。
func (s *ClipService) DeleteFilter(id string) error {
	for _, b := range BuiltinFilters() {
		if b.ID != id {
			continue
		}
		if !b.Deletable {
			return errors.New("内置筛选器不可删除")
		}
		s.hideFilter(id)
		if _, err := s.store.db.Exec(`DELETE FROM entry_filters WHERE id = ?`, id); err != nil {
			return err
		}
		return nil
	}
	res, err := s.store.db.Exec(`DELETE FROM entry_filters WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("筛选器不存在")
	}
	return nil
}

// RestoreFilters brings back every builtin the user removed.
//
// 侧栏 `⋯` 里能删的内置项只有「置顶」（其余是系统维度，见 DeleteFilter）。
// 删掉之后 `filters.hidden` 记着它，界面上再没有任何入口能还原 —— 以前这里
// 只剩一行注释，函数根本不存在，于是删除等于永久消失。
func (s *ClipService) RestoreFilters() ([]EntryFilter, error) {
	if err := s.store.SetMeta(metaHiddenFilters, ""); err != nil {
		return nil, err
	}
	Logf(s.store.Dir(), "filters restored to builtins")
	return s.GetFilters(), nil
}

// CountEntriesWith reports how many entries match q — 筛选器弹窗的实时预览。
func (s *ClipService) CountEntriesWith(q EntryQuery) int { return s.store.CountEntriesMatching(q) }

// GetFilterFields tells the frontend which fields/operators the rule engine
// accepts. 只此一份白名单，UI 不自己硬编码。
func (s *ClipService) GetFilterFields() []FilterFieldSpec { return FilterFields() }

const metaHiddenFilters = "filters.hidden"

func (s *ClipService) hiddenFilterIDs() map[string]bool {
	out := map[string]bool{}
	raw := s.store.Meta(metaHiddenFilters)
	if raw == "" {
		return out
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return out
	}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func (s *ClipService) hideFilter(id string) {
	ids := s.hiddenFilterIDs()
	ids[id] = true
	list := make([]string, 0, len(ids))
	for k := range ids {
		list = append(list, k)
	}
	sort.Strings(list)
	if body, err := json.Marshal(list); err == nil {
		_ = s.store.SetMeta(metaHiddenFilters, string(body))
	}
}

func (s *ClipService) unhideFilter(id string) {
	ids := s.hiddenFilterIDs()
	if !ids[id] {
		return
	}
	delete(ids, id)
	list := make([]string, 0, len(ids))
	for k := range ids {
		list = append(list, k)
	}
	sort.Strings(list)
	if body, err := json.Marshal(list); err == nil {
		_ = s.store.SetMeta(metaHiddenFilters, string(body))
	}
}

// ---- 日历 ----------------------------------------------------------------

// DayCount is one cell of the calendar heatmap.
type DayCount struct {
	Day   string `json:"day"` // YYYY-MM-DD（本地时区）
	Count int    `json:"count"`
}

// GetEntryCalendar returns per-day counts for a month ("YYYY-MM"), so the
// calendar can show which days actually have content.
//
// q 是当前侧栏筛选器的查询：日历的圆点必须和"点进去能看到什么"用同一套条件，
// 否则会在「片段 / 图片 / 置顶」这类筛选下画出点却一条也点不出来。
func (s *ClipService) GetEntryCalendar(month string, q EntryQuery) []DayCount {
	if len(month) != 7 {
		return nil
	}
	// created_at 存的是 UTC；按本地日历分组需要偏移，这里用 Go 侧聚合更稳妥
	startLocal, err := parseMonthStart(month)
	if err != nil {
		return nil
	}
	return s.store.CountByDay(q, encodeTime(startLocal), encodeTime(startLocal.AddDate(0, 1, 0)))
}

// parseMonthStart turns "YYYY-MM" into the local first-of-month instant.
func parseMonthStart(month string) (time.Time, error) {
	return time.ParseInLocation("2006-01", month, time.Local)
}
