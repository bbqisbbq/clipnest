package clip

import (
	"testing"
	"time"
)

func TestEntryUnifiedType(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	// 手写：永久，标签从正文派生
	manual, err := svc.store.SaveEntry(Entry{Content: "手写的 #工作 #会议"})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if manual.Source != SourceManual {
		t.Errorf("source = %q", manual.Source)
	}
	if manual.ExpiresAt != nil {
		t.Errorf("手写条目不应该有过期时间")
	}
	if len(manual.Tags) != 2 {
		t.Errorf("tags = %v", manual.Tags)
	}

	// 采集：自动加系统标签 + 30 天过期
	exp := time.Now().UTC().AddDate(0, 0, 30)
	clipEntry, err := svc.store.SaveEntry(Entry{
		Content:   "复制来的一段话",
		Kind:      KindText,
		Source:    SourceClipboard,
		Actions:   []string{ActionClipboard},
		Hash:      "h-clip",
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatalf("SaveEntry clipboard: %v", err)
	}
	if clipEntry.ExpiresAt == nil {
		t.Error("采集条目应该有到期时间")
	}
	if !clipEntry.HasAction(ActionClipboard) {
		t.Errorf("采集条目应该有 clipboard 能力: %v", clipEntry.Actions)
	}

	// 片段：有 trigger 才算片段
	sp, err := svc.store.SaveEntry(Entry{
		Content:    "您好，稍后回复您。",
		Source:     SourceManual,
		Actions:    []string{ActionSnippet},
		TriggerKey: "th-",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sp.IsSnippet() {
		t.Error("有 trigger 的条目应该是片段")
	}
	if manual.IsSnippet() {
		t.Error("没有 trigger 的条目不该是片段")
	}

	// 三者都在同一张表里，用属性筛得开
	if got := svc.store.ListEntries(EntryQuery{Source: "manual"}); len(got) != 2 {
		t.Errorf("manual = %d 条", len(got))
	}
	if got := svc.store.ListEntries(EntryQuery{Source: "clipboard"}); len(got) != 1 {
		t.Errorf("clipboard = %d 条", len(got))
	}
	if got := svc.store.ListEntries(EntryQuery{Snippets: true}); len(got) != 1 {
		t.Errorf("snippets = %d 条", len(got))
	}
	if got := svc.store.ListEntries(EntryQuery{Action: ActionSnippet}); len(got) != 1 {
		t.Errorf("auto tag 片段 = %d 条", len(got))
	}
	if got := svc.store.ListEntries(EntryQuery{Tag: "工作"}); len(got) != 1 {
		t.Errorf("tag 工作 = %d 条", len(got))
	}
}

func TestEntryExpiryOnlyTouchesCollected(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	permanent, _ := svc.store.SaveEntry(Entry{Content: "永久保存的笔记"})
	past := time.Now().UTC().Add(-time.Hour)
	expired, _ := svc.store.SaveEntry(Entry{
		Content: "早就过期的采集", Source: SourceClipboard,
		Actions:  []string{ActionClipboard}, Hash: "h-old", ExpiresAt: &past,
	})

	if n := svc.store.PruneExpired(); n != 1 {
		t.Fatalf("PruneExpired = %d, want 1", n)
	}
	if _, ok := svc.store.GetEntry(expired.ID); ok {
		t.Error("过期的采集条目应该被删")
	}
	if _, ok := svc.store.GetEntry(permanent.ID); !ok {
		t.Error("手写笔记被误删了——这是最不能接受的错误")
	}
}

func TestEntryDayFilterAndChatOrder(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	var ids []string
	for i := 0; i < 3; i++ {
		e, _ := svc.store.SaveEntry(Entry{Content: "第 " + string(rune('A'+i)) + " 条"})
		ids = append(ids, e.ID)
		time.Sleep(5 * time.Millisecond)
	}

	// 聊天式：最早在前，最新在末尾
	got := svc.store.ListEntries(EntryQuery{})
	if len(got) != 3 || got[0].ID != ids[0] || got[2].ID != ids[2] {
		t.Errorf("顺序不对")
	}

	// Tail：取最新 2 条，仍是正序
	tail := svc.store.ListEntries(EntryQuery{Limit: 2, Tail: true})
	if len(tail) != 2 || tail[1].ID != ids[2] {
		t.Errorf("tail = %d 条", len(tail))
	}

	// 日历：按天筛选应该能命中今天写的
	today := time.Now().Format("2006-01-02")
	if day := svc.store.ListEntries(EntryQuery{Day: today}); len(day) != 3 {
		t.Errorf("今天 = %d 条", len(day))
	}
	if day := svc.store.ListEntries(EntryQuery{Day: "2000-01-01"}); len(day) != 0 {
		t.Errorf("空日期 = %d 条", len(day))
	}

	// 统计
	st := svc.store.EntryStats()
	if st.Total != 3 || st.Today != 3 || st.Manual != 3 {
		t.Errorf("stats = %+v", st)
	}
}

func TestEntryTriggerLookup(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	if _, err := svc.store.SaveEntry(Entry{
		Content: "办公地址内容", TriggerKey: "addr-", Actions: []string{ActionSnippet},
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := svc.store.FindEntryByTrigger("addr-", false); !ok {
		t.Error("大小写不敏感查找失败")
	}
	if _, ok := svc.store.FindEntryByTrigger("ADDR-", false); !ok {
		t.Error("大写应命中（不区分大小写）")
	}
	if _, ok := svc.store.FindEntryByTrigger("ADDR-", true); ok {
		t.Error("区分大小写时不该命中大写")
	}
	if n := len(svc.store.EntryTriggers()); n != 1 {
		t.Errorf("triggers = %d", n)
	}
}

func TestEntryBlobContentAddressed(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	const onePixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	payload, err := decodeBase64Image(onePixel)
	if err != nil {
		t.Fatal(err)
	}

	t1, err := svc.store.SaveEntryBlob(payload, "png")
	if err != nil {
		t.Fatalf("SaveEntryBlob: %v", err)
	}
	t2, _ := svc.store.SaveEntryBlob(payload, "png")
	if t1 != t2 {
		t.Errorf("内容寻址失效: %q vs %q", t1, t2)
	}

	got, mime, err := svc.store.LoadEntryBlob(t1)
	if err != nil {
		t.Fatalf("LoadEntryBlob: %v", err)
	}
	if len(got) != len(payload) {
		t.Errorf("长度 = %d, want %d", len(got), len(payload))
	}
	if mime != "image/png" {
		t.Errorf("mime = %q", mime)
	}
}
