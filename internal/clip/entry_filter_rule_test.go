package clip

import (
	"strings"
	"testing"
	"time"
)

// 筛选器规则：编译成 SQL 的口径只有一处（EntryQuery.where），这里钉住
// and / or / not 的括号、多值 tag、以及"未知字段被丢弃"这几件事。

func ruleStore(t *testing.T) *Store {
	t.Helper()
	return newTestService(t, t.TempDir()).store
}

func seedRuleEntries(t *testing.T, s *Store) {
	t.Helper()
	seed := []Entry{
		{Content: "工作需要跟进 #工作\n\n- [ ] 写周报", Source: SourceManual},
		{Content: "私人笔记 #生活", Source: SourceManual},
		{Content: "采集的一段代码", Source: SourceClipboard, Actions: []string{ActionClipboard}},
		{Content: "全部做完了 #工作\n\n- [x] 交报告", Source: SourceManual},
	}
	for _, e := range seed {
		if _, err := s.SaveEntry(e); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestRuleAndOrNot(t *testing.T) {
	s := ruleStore(t)
	seedRuleEntries(t, s)

	// 标签 #工作 and 有未完成待办 → 只有第一条
	q := EntryQuery{Rule: &FilterGroup{Op: "and", Conditions: []FilterCondition{
		{Field: "tag", Op: "is", Value: "工作"},
		{Field: "todo", Op: "is", Value: "open"},
	}}}
	if got := s.ListEntries(q); len(got) != 1 || !strings.Contains(got[0].Content, "写周报") {
		t.Fatalf("and rule: got %d rows", len(got))
	}

	// or：(标签 工作) or (来源 剪贴板) → 3 条
	q = EntryQuery{Rule: &FilterGroup{Op: "or", Conditions: []FilterCondition{
		{Field: "tag", Op: "is", Value: "工作"},
		{Field: "source", Op: "is", Value: string(SourceClipboard)},
	}}}
	if got := s.ListEntries(q); len(got) != 3 {
		t.Fatalf("or rule: got %d rows", len(got))
	}

	// not 组：(标签 工作) 取反 → 2 条
	q = EntryQuery{Rule: &FilterGroup{Op: "not", Conditions: []FilterCondition{
		{Field: "tag", Op: "is", Value: "工作"},
	}}}
	if got := s.ListEntries(q); len(got) != 2 {
		t.Fatalf("not rule: got %d rows", len(got))
	}
}

func TestRuleGroupNesting(t *testing.T) {
	s := ruleStore(t)
	seedRuleEntries(t, s)

	// (标签 工作 AND 未完成) OR (标签 生活)
	q := EntryQuery{Rule: &FilterGroup{Op: "or",
		Groups: []FilterGroup{
			{Op: "and", Conditions: []FilterCondition{
				{Field: "tag", Op: "is", Value: "工作"},
				{Field: "todo", Op: "is", Value: "open"},
			}},
			{Op: "and", Conditions: []FilterCondition{{Field: "tag", Op: "is", Value: "生活"}}},
		},
	}}
	if got := s.ListEntries(q); len(got) != 2 {
		t.Fatalf("nested groups: got %d rows", len(got))
	}
}

func TestRuleMultiValueTag(t *testing.T) {
	s := ruleStore(t)
	seedRuleEntries(t, s)

	q := EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "tag", Op: "anyOf", Value: "工作\n生活"},
	}}}
	if got := s.ListEntries(q); len(got) != 3 {
		t.Fatalf("anyOf tags: got %d rows", len(got))
	}

	q = EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "tag", Op: "isNot", Value: "工作"},
	}}}
	if got := s.ListEntries(q); len(got) != 2 {
		t.Fatalf("isNot tag: got %d rows", len(got))
	}
}

func TestRuleActionsAndText(t *testing.T) {
	s := ruleStore(t)
	seedRuleEntries(t, s)

	q := EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "action", Op: "has", Value: ActionClipboard},
	}}}
	if got := s.ListEntries(q); len(got) != 1 {
		t.Fatalf("action has: got %d", len(got))
	}

	q = EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "text", Op: "contains", Value: "周报"},
	}}}
	if got := s.ListEntries(q); len(got) != 1 {
		t.Fatalf("text contains: got %d", len(got))
	}

	q = EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "text", Op: "notContains", Value: "周报"},
	}}}
	if got := s.ListEntries(q); len(got) != 3 {
		t.Fatalf("text notContains: got %d", len(got))
	}
}

func TestRuleAgeDays(t *testing.T) {
	s := ruleStore(t)
	// 直接带着旧的 created_at 写入：SaveEntry 的 UPDATE 分支不动 created_at，
	// 所以"先建再改时间"是无效的（这本身是设计：采集时间不该被重写）。
	if _, err := s.SaveEntry(Entry{
		Content:   "很久以前 #旧",
		Source:    SourceManual,
		CreatedAt: time.Now().UTC().AddDate(0, 0, -40),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.SaveEntry(Entry{Content: "刚写的 #新", Source: SourceManual}); err != nil {
		t.Fatalf("save: %v", err)
	}

	older := EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "ageDays", Op: "olderThan", Value: "30"},
	}}}
	if got := s.ListEntries(older); len(got) != 1 {
		t.Fatalf("olderThan 30: got %d", len(got))
	}
	newer := EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "ageDays", Op: "newerThan", Value: "30"},
	}}}
	if got := s.ListEntries(newer); len(got) != 1 {
		t.Fatalf("newerThan 30: got %d", len(got))
	}
}

// 未知字段不能把整页查空：它被丢弃，剩下的条件照常生效。
func TestRuleUnknownFieldDropped(t *testing.T) {
	s := ruleStore(t)
	seedRuleEntries(t, s)

	q := EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "color", Op: "is", Value: "red"},
		{Field: "tag", Op: "is", Value: "工作"},
	}}}
	if got := s.ListEntries(q); len(got) != 2 {
		t.Fatalf("unknown field should be dropped, got %d rows", len(got))
	}
}

// 规则里提到 archived 时，规则说了算（否则归档筛选永远查不到东西）。
func TestRuleExplicitArchived(t *testing.T) {
	s := ruleStore(t)
	e, _ := s.SaveEntry(Entry{Content: "要归档的 #归档", Source: SourceManual})
	e.Archived = true
	if _, err := s.SaveEntry(e); err != nil {
		t.Fatalf("archive: %v", err)
	}

	q := EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "archived", Op: "is", Value: "true"},
	}}}
	if got := s.ListEntries(q); len(got) != 1 {
		t.Fatalf("archived rule: got %d rows", len(got))
	}
}

func TestCountEntriesWith(t *testing.T) {
	s := ruleStore(t)
	seedRuleEntries(t, s)

	q := EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
		{Field: "tag", Op: "is", Value: "工作"},
	}}}
	if n := s.CountEntriesMatching(q); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	// 与列表口径一致
	if n := len(s.ListEntries(q)); n != 2 {
		t.Fatalf("list = %d, want 2", n)
	}
}

func TestBuiltinFilterDeletable(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	visible := func() map[string]bool {
		out := map[string]bool{}
		for _, f := range svc.GetFilters() {
			out[f.ID] = true
		}
		return out
	}

	if !visible()["pinned"] {
		t.Fatal("pinned should be visible by default")
	}
	if !svc.DeleteFilter("pinned") {
		t.Fatal("pinned should be deletable")
	}
	if visible()["pinned"] {
		t.Fatal("pinned should be hidden after delete")
	}
	// 「全部内容」不许删
	if svc.DeleteFilter("all") {
		t.Fatal("all must not be deletable")
	}
	if !visible()["all"] {
		t.Fatal("all must stay visible")
	}
	// 恢复默认
	svc.RestoreFilters()
	if !visible()["pinned"] {
		t.Fatal("restore should bring pinned back")
	}
}

func TestSaveFilterRejectsBadRule(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	_, err := svc.SaveFilter(EntryFilter{
		Name: "坏规则",
		Query: EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
			{Field: "color", Op: "is", Value: "red"},
		}}},
	})
	if err == nil {
		t.Fatal("unknown field must be rejected at save time")
	}
	if _, err := svc.SaveFilter(EntryFilter{
		Name: "好规则",
		Query: EntryQuery{Rule: &FilterGroup{Conditions: []FilterCondition{
			{Field: "tag", Op: "is", Value: "工作"},
		}}},
	}); err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}
}
