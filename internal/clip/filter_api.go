package clip

// 用户自定义筛选器的对外接口（侧栏每一行 = 一条保存下来的 EntryQuery）。
//
// 单独一个文件的理由：这里是**持久化 + 隐藏位 + 内置筛选器覆盖**的组合逻辑，
// 而 entry_api.go 管的是条目本身。规则树怎么编译成 SQL 在 entry_filter_rule.go，
// 不在这里 —— 这一层只管"这条筛选器存不存、叫什么、图标是什么、藏没藏"。

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

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

