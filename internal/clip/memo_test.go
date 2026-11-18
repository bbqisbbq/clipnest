package clip

import (
	"fmt"
	"strings"
	"testing"
	"time"
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
func TestMemoImageRoundTrip(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	// 1x1 透明 PNG
	const onePixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	token, err := svc.SaveMemoImage(onePixel)
	if err != nil {
		t.Fatalf("SaveMemoImage: %v", err)
	}
	if !strings.HasPrefix(token, "memoimg:") {
		t.Fatalf("token = %q", token)
	}

	m, err := svc.SaveMemo(Memo{Content: "看这张图 ![截图](" + token + ") #图片"})
	if err != nil {
		t.Fatal(err)
	}

	hash := strings.TrimPrefix(token, "memoimg:")
	dataURL, err := svc.GetMemoImage(hash)
	if err != nil {
		t.Fatalf("GetMemoImage: %v", err)
	}
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("dataURL = %.40q", dataURL)
	}

	// 引用还在，清理不应删掉
	if removed := svc.store.PruneMemoImages(); removed != 0 {
		t.Errorf("prune removed referenced image: %d", removed)
	}

	// 删掉备忘后再清理，图片应被回收
	svc.DeleteMemo(m.ID)
	if removed := svc.store.PruneMemoImages(); removed != 1 {
		t.Errorf("prune after delete = %d, want 1", removed)
	}

	// 不同内容同 hash 只存一份
	t1, _ := svc.SaveMemoImage(onePixel)
	t2, _ := svc.SaveMemoImage(onePixel)
	if t1 != t2 {
		t.Errorf("content addressing broken: %q vs %q", t1, t2)
	}
}
func TestMemoTimelineOrder(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	first, _ := svc.SaveMemo(Memo{Content: "第一条"})
	second, _ := svc.SaveMemo(Memo{Content: "第二条"})
	third, _ := svc.SaveMemo(Memo{Content: "第三条"})

	// 时间戳精度到秒，稍等一下确保 created_at 不同
	time.Sleep(1100 * time.Millisecond)
	third, _ = svc.SaveMemo(Memo{ID: third.ID, Content: "第三条（改过）"})

	got := svc.GetMemos(MemoFilter{})
	if len(got) != 3 {
		t.Fatalf("memos = %d", len(got))
	}
	// 聊天式正序：最早的在最前，最新的贴在最后（靠输入框）；改过内容不改变位置
	if got[0].ID != first.ID || got[1].ID != second.ID || got[2].ID != third.ID {
		t.Errorf("order = %s, %s, %s", shortID(got[0].ID), shortID(got[1].ID), shortID(got[2].ID))
	}

	// 游标翻页：取比第二条更早的，应只剩第一条
	// 前端是把 JSON 里的 createdAt 原样回传，所以这里也用序列化后的精确值
	page := svc.GetMemos(MemoFilter{BeforeCreatedAt: encodeTime(second.CreatedAt)})
	if len(page) != 1 || page[0].ID != first.ID {
		t.Errorf("cursor page = %d 条", len(page))
	}
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}
func TestMemoTailPagination(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	var ids []string
	for i := 0; i < 5; i++ {
		m, err := svc.SaveMemo(Memo{Content: fmt.Sprintf("第 %d 条", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
		time.Sleep(20 * time.Millisecond)
	}

	// Tail：拿最新的 2 条，且按时间正序（最新在末尾）
	tail := svc.GetMemos(MemoFilter{Limit: 2, Tail: true})
	if len(tail) != 2 {
		t.Fatalf("tail = %d 条", len(tail))
	}
	if tail[0].ID != ids[3] || tail[1].ID != ids[4] {
		t.Errorf("tail 顺序不对: %s, %s", shortID(tail[0].ID), shortID(tail[1].ID))
	}

	// 用最早一条作游标往前翻，应拿到它之前的（这里没有）
	older := svc.GetMemos(MemoFilter{Limit: 2, BeforeCreatedAt: encodeTime(tail[0].CreatedAt)})
	if len(older) != 2 || older[1].ID != ids[1] {
		t.Errorf("cursor 翻页 = %d 条", len(older))
	}
}