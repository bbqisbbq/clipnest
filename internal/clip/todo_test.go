package clip

import (
	"strings"
	"testing"
)

// 待办解析与勾选：边角都在这里钉住，省得以后"顺手优化正则"把某类清单弄丢。

// todoTestStore 复用 sync_test.go 的 newTestService：整个包共用一套临时库搭法。
func todoTestStore(t *testing.T) *Store {
	t.Helper()
	return newTestService(t, t.TempDir()).store
}

func TestParseTodosBasic(t *testing.T) {
	content := "# 周计划\n\n- [ ] 写周报\n- [x] 交水电费\n* [X] 大写也算完成\n1. [ ] 带序号的也算\n\n普通段落不算。"
	items := ParseTodos("e1", content)
	if len(items) != 4 {
		t.Fatalf("want 4 items, got %d: %+v", len(items), items)
	}
	if items[0].Text != "写周报" || items[0].Done {
		t.Errorf("item0 = %+v", items[0])
	}
	if !items[1].Done {
		t.Errorf("item1 should be done: %+v", items[1])
	}
	if !items[2].Done {
		t.Errorf("[X] should count as done: %+v", items[2])
	}
	if items[3].Done || items[3].Text != "带序号的也算" {
		t.Errorf("ordered list item = %+v", items[3])
	}
	// 行号必须是正文里的真实行号（勾选时按行改写）
	if items[0].Line != 2 {
		t.Errorf("item0 line = %d, want 2", items[0].Line)
	}
}

func TestParseTodosSkipsFencedCode(t *testing.T) {
	content := strings.Join([]string{
		"- [ ] 真的任务",
		"```md",
		"- [ ] 代码里的示例，不算",
		"```",
		"~~~",
		"- [ ] 波浪号围栏里的也不算",
		"~~~",
		"- [x] 另一个真的任务",
	}, "\n")
	if items := ParseTodos("e1", content); len(items) != 2 {
		t.Fatalf("want 2 items outside fences, got %d: %+v", len(items), items)
	}
}

func TestParseTodosIndent(t *testing.T) {
	content := "- [ ] 一级\n  - [ ] 二级\n    - [ ] 三级"
	items := ParseTodos("e1", content)
	if len(items) != 3 {
		t.Fatalf("want 3, got %d", len(items))
	}
	for i, want := range []int{0, 1, 2} {
		if items[i].Indent != want {
			t.Errorf("item%d indent = %d, want %d", i, items[i].Indent, want)
		}
	}
}

// 同一条目里文本相同的两条任务：key 必须不同，否则勾一个会命中两个。
func TestParseTodosDuplicateText(t *testing.T) {
	items := ParseTodos("e1", "- [ ] 重复\n- [ ] 重复")
	if len(items) != 2 {
		t.Fatalf("want 2, got %d", len(items))
	}
	if items[0].Key == items[1].Key {
		t.Fatalf("duplicate texts must get distinct keys, both = %s", items[0].Key)
	}
}

// key 只依赖文本：在任务上方插一行别的内容，key 不能变（否则勾选会错位到别的任务）。
func TestTodoKeyStableAcrossLineShift(t *testing.T) {
	before := ParseTodos("e1", "- [ ] 任务甲\n- [ ] 任务乙")
	after := ParseTodos("e1", "插入了一行\n\n- [ ] 任务甲\n- [ ] 任务乙")
	if before[0].Key != after[0].Key || before[1].Key != after[1].Key {
		t.Fatalf("keys shifted: %v vs %v", before, after)
	}
	if after[0].Line != 2 {
		t.Fatalf("line should follow the text, got %d", after[0].Line)
	}
}

func TestTodoKeyIgnoresWhitespaceNoise(t *testing.T) {
	a := ParseTodos("e1", "- [ ] 写   周报")
	b := ParseTodos("e1", "- [ ] 写 周报")
	if a[0].Key != b[0].Key {
		t.Fatalf("whitespace-only change must keep the key: %s vs %s", a[0].Key, b[0].Key)
	}
}

func TestSetTodoDoneOnlyTouchesOneChar(t *testing.T) {
	s := todoTestStore(t)
	e, err := s.SaveEntry(Entry{
		Content: "前言 #标签\n\n- [ ] 甲\n- [ ] 乙\n\n结尾",
		Source:  SourceManual,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if e.TodoState != todoStateOpen || e.TodoOpen != 2 {
		t.Fatalf("derived state = %q open=%d", e.TodoState, e.TodoOpen)
	}
	items := ParseTodos(e.ID, e.Content)

	got, err := s.SetTodoDone(e.ID, items[1].Key, true)
	if err != nil {
		t.Fatalf("SetTodoDone: %v", err)
	}
	want := "前言 #标签\n\n- [ ] 甲\n- [x] 乙\n\n结尾"
	if got.Content != want {
		t.Fatalf("content changed more than the marker:\n got: %q\nwant: %q", got.Content, want)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "标签" {
		t.Errorf("tags got reset: %v", got.Tags)
	}
	if got.Hash != e.Hash {
		t.Errorf("hash must not change on a checkbox click: %s -> %s", e.Hash, got.Hash)
	}
	if got.TodoOpen != 1 || got.TodoState != todoStateOpen {
		t.Errorf("after one done: state=%q open=%d", got.TodoState, got.TodoOpen)
	}

	// 全部勾完 → done + 完成时刻
	items = ParseTodos(got.ID, got.Content)
	for _, it := range items {
		if !it.Done {
			if _, err := s.SetTodoDone(got.ID, it.Key, true); err != nil {
				t.Fatalf("SetTodoDone: %v", err)
			}
		}
	}
	all, _ := s.GetEntry(e.ID)
	if all.TodoState != todoStateDone || all.TodoDoneAt == nil {
		t.Fatalf("all done should set done_at: state=%q at=%v", all.TodoState, all.TodoDoneAt)
	}
}

func TestSetTodoDoneUnknownKey(t *testing.T) {
	s := todoTestStore(t)
	e, _ := s.SaveEntry(Entry{Content: "- [ ] 甲", Source: SourceManual})
	if _, err := s.SetTodoDone(e.ID, "deadbeef", true); err == nil {
		t.Fatal("unknown key must fail instead of silently doing nothing")
	}
}

func TestListTodosOrderAndFilter(t *testing.T) {
	s := todoTestStore(t)
	if _, err := s.SaveEntry(Entry{Content: "- [ ] 旧任务\n- [x] 旧完成", Source: SourceManual}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.SaveEntry(Entry{Content: "- [ ] 新任务", Source: SourceManual}); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows := s.ListTodos(EntryQuery{})
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	// 未完成在前，且最新条目在前
	if rows[0].Item.Text != "新任务" || rows[0].Item.Done {
		t.Errorf("first row should be the newest open task: %+v", rows[0])
	}
	if !rows[len(rows)-1].Item.Done {
		t.Errorf("done items must sink to the bottom: %+v", rows[len(rows)-1])
	}

	// 归档的条目不出现在收集页
	archived, ok := s.GetEntry(rows[0].EntryID)
	if !ok {
		t.Fatal("get entry")
	}
	archived.Archived = true
	if _, err := s.SaveEntry(archived); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if rows := s.ListTodos(EntryQuery{}); len(rows) != 2 {
		t.Fatalf("archived entry should be excluded, got %d rows", len(rows))
	}
}

func TestTodoStateFilter(t *testing.T) {
	s := todoTestStore(t)
	if _, err := s.SaveEntry(Entry{Content: "- [ ] 开着", Source: SourceManual}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.SaveEntry(Entry{Content: "- [x] 做完了", Source: SourceManual}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.SaveEntry(Entry{Content: "没有任务的普通条目", Source: SourceManual}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if n := len(s.ListEntries(EntryQuery{Todo: "open"})); n != 1 {
		t.Errorf("todo=open want 1, got %d", n)
	}
	if n := len(s.ListEntries(EntryQuery{Todo: "done"})); n != 1 {
		t.Errorf("todo=done want 1, got %d", n)
	}
	if n := len(s.ListEntries(EntryQuery{Todo: "none"})); n != 1 {
		t.Errorf("todo=none want 1, got %d", n)
	}
	// 日历圆点必须跟列表同一个口径
	open := s.ListEntries(EntryQuery{Todo: "open"})
	if len(open) != 1 {
		t.Fatalf("todo=open want 1, got %d", len(open))
	}
	from := encodeTime(open[0].CreatedAt.AddDate(0, 0, -1))
	to := encodeTime(open[0].CreatedAt.AddDate(0, 0, 2))
	if days := s.CountByDay(EntryQuery{Todo: "open"}, from, to); len(days) != 1 || days[0].Count != 1 {
		t.Errorf("calendar should agree with the list: %+v", days)
	}
}
