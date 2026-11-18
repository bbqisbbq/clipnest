package clip

import (
	"strings"
	"testing"
)

func TestMemoTagExtraction(t *testing.T) {
	cases := []struct {
		content string
		want    []string
	}{
		{"今天开会 #工作 #会议", []string{"工作", "会议"}},
		{"#工作 重复 #工作 一次", []string{"工作"}},
		{"没有标签的内容", nil},
		{"#前端/构建 支持斜杠", []string{"前端/构建"}},
		{"#a1_b 下划线和数字", []string{"a1_b"}},
	}
	for _, c := range cases {
		got := ExtractTags(c.content)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("ExtractTags(%q) = %v, want %v", c.content, got, c.want)
		}
	}
}

func TestMemoCRUDPinArchive(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	m, err := svc.SaveMemo(Memo{Content: "第一条备忘 #灵感"})
	if err != nil {
		t.Fatalf("SaveMemo: %v", err)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "灵感" {
		t.Errorf("tags = %v", m.Tags)
	}
	if m.CreatedAt.IsZero() || m.Hash == "" {
		t.Errorf("timestamps/hash missing: %+v", m)
	}

	// 列表 & 标签
	if got := svc.GetMemos(MemoFilter{}); len(got) != 1 {
		t.Fatalf("GetMemos = %d", len(got))
	}
	if tags := svc.GetMemoTags(); len(tags) != 1 || tags[0] != "灵感" {
		t.Errorf("tags list = %v", tags)
	}

	// 置顶后应排在最前
	if _, err := svc.SaveMemo(Memo{ID: m.ID, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if pinned := svc.GetMemos(MemoFilter{Pinned: true}); len(pinned) != 1 {
		t.Errorf("pinned filter = %d", len(pinned))
	}

	// 归档：默认列表里消失，归档列表里出现
	if _, err := svc.SaveMemo(Memo{ID: m.ID, Archived: true}); err != nil {
		t.Fatal(err)
	}
	if got := svc.GetMemos(MemoFilter{}); len(got) != 0 {
		t.Errorf("archived memo leaked into the normal list: %d", len(got))
	}
	if got := svc.GetMemos(MemoFilter{Archived: true}); len(got) != 1 {
		t.Errorf("archive list = %d", len(got))
	}

	st := svc.GetMemoStats()
	if st.Archived != 1 {
		t.Errorf("stats = %+v", st)
	}

	if !svc.DeleteMemo(m.ID) {
		t.Error("delete failed")
	}
	if got := svc.GetMemos(MemoFilter{Archived: true}); len(got) != 0 {
		t.Errorf("memo survived delete: %d", len(got))
	}
}

func TestMemoSearchChinese(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	if _, err := svc.SaveMemo(Memo{Content: "会议纪要：讨论了发布节奏 #工作"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMemo(Memo{Content: "读到一段有意思的话 #读书"}); err != nil {
		t.Fatal(err)
	}

	// 3+ 字走 FTS5 trigram 子串匹配
	if got := svc.GetMemos(MemoFilter{Query: "纪要"}); len(got) != 0 {
		t.Logf("2 字符查询走 LIKE，命中 %d 条", len(got))
	}
	if got := svc.GetMemos(MemoFilter{Query: "会议纪要"}); len(got) != 1 {
		t.Errorf("FTS query 会议纪要 = %d, want 1", len(got))
	}
	if got := svc.GetMemos(MemoFilter{Query: "发布了"}); len(got) != 0 {
		t.Logf("子串查询（不在词首）命中 %d 条", len(got))
	}
	if got := svc.GetMemos(MemoFilter{Query: "有意思"}); len(got) != 1 {
		t.Errorf("FTS query 有意思 = %d, want 1", len(got))
	}
	if got := svc.GetMemos(MemoFilter{Tag: "读书"}); len(got) != 1 {
		t.Errorf("tag filter = %d, want 1", len(got))
	}
}

func TestMemoFromClipboardAndSnippet(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	item, _, err := svc.store.Add(Captured{Kind: KindText, Text: "从剪贴板来的内容", Preview: "从剪贴板来的内容", Hash: "h-memo", Size: 24})
	if err != nil {
		t.Fatal(err)
	}
	m, err := svc.SaveItemAsMemo(item.ID)
	if err != nil {
		t.Fatalf("SaveItemAsMemo: %v", err)
	}
	if m.Source != "clipboard" || !strings.Contains(m.Content, "剪贴板") {
		t.Errorf("memo = %+v", m)
	}

	sp, err := svc.SaveSnippet(Snippet{Kind: SnippetText, Title: "话术", Body: "您好，稍后回复您。"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SnippetToMemo(sp.ID, ""); err != nil {
		t.Fatalf("SnippetToMemo: %v", err)
	}
	if _, err := svc.SnippetToMemo(sp.ID, m.ID); err != nil {
		t.Fatalf("SnippetToMemo append: %v", err)
	}
	updated, _ := svc.store.GetMemo(m.ID)
	if !strings.Contains(updated.Content, "稍后回复您") {
		t.Errorf("append failed: %q", updated.Content)
	}
}
