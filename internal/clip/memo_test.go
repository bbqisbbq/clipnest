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

func TestMemoSyncAcrossDevices(t *testing.T) {
	base, _, _ := newTestDAV(t)

	a := syncedService(t, base)
	if _, err := a.SaveMemo(Memo{Content: "同步测试：这条应该在另一台机器上出现 #同步"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	b := syncedService(t, base)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	got := b.GetMemos(MemoFilter{})
	if len(got) != 1 || !strings.Contains(got[0].Content, "同步测试") {
		t.Fatalf("B memos = %+v", got)
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "同步" {
		t.Errorf("tags lost in transit: %v", got[0].Tags)
	}

	// 删除要传播
	if !b.DeleteMemo(got[0].ID) {
		t.Fatal("delete failed")
	}
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B resync: %v", err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A resync: %v", err)
	}
	if left := a.GetMemos(MemoFilter{}); len(left) != 0 {
		t.Errorf("deleted memo survived on A: %d", len(left))
	}
}

func TestMemosExportImportRoundTrip(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	keep, err := svc.SaveMemo(Memo{Content: "导出测试 #导出", Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := svc.ExportMemosZip()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(zipBytes) == 0 {
		t.Fatal("empty archive")
	}

	other := newTestService(t, t.TempDir())
	res, err := other.ImportMemosZip(zipBytes)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Added != 1 {
		t.Errorf("added = %d, want 1", res.Added)
	}
	got := other.GetMemos(MemoFilter{})
	if len(got) != 1 || got[0].Content != keep.Content || !got[0].Pinned {
		t.Errorf("imported memo = %+v", got)
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "导出" {
		t.Errorf("tags lost: %v", got[0].Tags)
	}

	// 再导入一次不应重复
	again, err := other.ImportMemosZip(zipBytes)
	if err != nil {
		t.Fatal(err)
	}
	if again.Added != 0 || again.Skipped != 1 {
		t.Errorf("second import = %+v", again)
	}
}
func TestPackageCarriesMemos(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	if _, err := svc.SaveMemo(Memo{Content: "打包里应该有这条 #打包"}); err != nil {
		t.Fatal(err)
	}
	payload, _, err := svc.buildOwnPackage(nil, nil, nil, nil, svc.store.Settings())
	if err != nil {
		t.Fatalf("buildOwnPackage: %v", err)
	}
	pkg, err := parsePackage(payload)
	if err != nil {
		t.Fatalf("parsePackage: %v", err)
	}
	if len(pkg.index.Memos) != 1 {
		t.Errorf("index.Memos = %d, want 1", len(pkg.index.Memos))
	}
	if len(pkg.memos) != 1 {
		t.Fatalf("pkg.memos = %d, want 1", len(pkg.memos))
	}
	if !strings.Contains(pkg.memos[0].Content, "打包") {
		t.Errorf("memo content lost: %q", pkg.memos[0].Content)
	}
}