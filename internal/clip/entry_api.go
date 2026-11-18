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
	Builtin   bool       `json:"builtin"` // 内置的不许删，只能改名换图标
}

// BuiltinFilters are the default entry points. They are code, not rows, so a
// fresh install always has a usable sidebar; user edits are stored as overrides.
func BuiltinFilters() []EntryFilter {
	return []EntryFilter{
		{ID: "all", Name: "全部内容", Icon: "all", Builtin: true, Query: EntryQuery{}},
		{ID: "mine", Name: "我写的", Icon: "memo", Builtin: true, Query: EntryQuery{Source: "manual"}},
		{ID: "clipboard", Name: "剪贴板", Icon: "pin", Builtin: true, Query: EntryQuery{Action: ActionClipboard}},
		{ID: "snippets", Name: "片段", Icon: "snippet", Builtin: true, Query: EntryQuery{Snippets: true}},
		{ID: "images", Name: "图片", Icon: "image", Builtin: true, Query: EntryQuery{Kind: string(KindImage)}},
		{ID: "pinned", Name: "置顶", Icon: "star", Builtin: true, Query: EntryQuery{Pinned: true}},
		{ID: "archived", Name: "已归档", Icon: "folder", Builtin: true, Query: EntryQuery{Archived: true}},
	}
}

// ---- 条目 API ------------------------------------------------------------

// GetEntries is the single list call for the whole app.
func (s *ClipService) GetEntries(q EntryQuery) []Entry {
	return s.store.ListEntries(q)
}

// GetEntryStats reports the counts shown next to each filter.
func (s *ClipService) GetEntryStats() EntryStats { return s.store.EntryStats() }

// GetEntryTags lists tags in use (content tags + system tags).
func (s *ClipService) GetEntryTags() []string { return s.store.EntryTags() }

// SaveEntry creates or updates an entry. Empty content is allowed when only
// flags change — the stored content is reused.
func (s *ClipService) SaveEntry(e Entry) (Entry, error) {
	if strings.TrimSpace(e.Content) == "" && e.ID != "" {
		if existing, ok := s.store.GetEntry(e.ID); ok {
			e.Content = existing.Content
			e.Kind = existing.Kind
		}
	}
	return s.store.SaveEntry(e)
}

// DeleteEntry removes an entry and records a tombstone so peers drop theirs.
func (s *ClipService) DeleteEntry(id string) bool {
	if e, ok := s.store.GetEntry(id); ok && e.Hash != "" {
		s.store.AddTombstoneScoped("entry", e.Hash, nowUTC(), hostName())
	}
	return s.store.DeleteEntry(id)
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
	return s.store.SaveEntry(e)
}

// DemoteFromSnippet removes the trigger but keeps the content and the tag.
func (s *ClipService) DemoteFromSnippet(id string) (Entry, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	e.TriggerKey = ""
	return s.store.SaveEntry(e)
}

// PromoteToManual keeps a collected entry from expiring (it becomes "mine").
func (s *ClipService) PromoteToManual(id string) (Entry, error) {
	e, ok := s.store.GetEntry(id)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	e.Source = SourceManual
	e.ExpiresAt = nil
	return s.store.SaveEntry(e)
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
func (s *ClipService) GetFilters() []EntryFilter {
	builtins := BuiltinFilters()
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
			f := EntryFilter{ID: r.ID, Name: r.Name, Icon: r.Icon, Query: q, SortOrder: r.SortOrder}
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
	Logf(s.store.Dir(), "filter saved: %s (%s)", f.ID, f.Name)
	return f, nil
}

// DeleteFilter removes a user filter. Builtins fall back to their defaults.
func (s *ClipService) DeleteFilter(id string) bool {
	res, err := s.store.db.Exec(`DELETE FROM entry_filters WHERE id = ?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// ---- 日历 ----------------------------------------------------------------

// DayCount is one cell of the calendar heatmap.
type DayCount struct {
	Day   string `json:"day"` // YYYY-MM-DD（本地时区）
	Count int    `json:"count"`
}

// GetEntryCalendar returns per-day counts for a month ("YYYY-MM"), so the
// calendar can show which days actually have content.
func (s *ClipService) GetEntryCalendar(month string) []DayCount {
	if len(month) != 7 {
		return nil
	}
	// created_at 存的是 UTC；按本地日历分组需要偏移，这里用 Go 侧聚合更稳妥
	startLocal, err := parseMonthStart(month)
	if err != nil {
		return nil
	}
	from := encodeTime(startLocal)
	to := encodeTime(startLocal.AddDate(0, 1, 0))

	rows, err := s.store.db.Query(
		`SELECT created_at FROM entries WHERE created_at >= ? AND created_at < ?`, from, to)
	if err != nil {
		return nil
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		t := decodeTime(raw)
		if t.IsZero() {
			continue
		}
		counts[t.Local().Format("2006-01-02")]++
	}

	out := make([]DayCount, 0, len(counts))
	for day, n := range counts {
		out = append(out, DayCount{Day: day, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

// parseMonthStart turns "YYYY-MM" into the local first-of-month instant.
func parseMonthStart(month string) (time.Time, error) {
	return time.ParseInLocation("2006-01", month, time.Local)
}