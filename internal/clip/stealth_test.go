package clip

import (
	"strings"
	"testing"
)

// 隐写模式的规则很薄但很容易写歪（`@名称@` 的解析、粘住的目标、追加分隔），
// 所以直接锁住行为，别让 UI 手感去替它兜底。

func TestSplitStealthTarget(t *testing.T) {
	cases := []struct {
		in    string
		body  string
		name  string
		reset bool
	}{
		{in: "随手记一条", body: "随手记一条"},
		{in: "@随记@ 买牛奶", body: " 买牛奶", name: "随记"},
		{in: "买牛奶 @随记@", body: "买牛奶 ", name: "随记"},
		{in: "@随记@", body: "", name: "随记"},
		{in: "@-@ 回到默认", body: " 回到默认", reset: true},
		// 只认第一个标记：第二个当普通文本留着
		{in: "a @x@ b @y@", body: "a  b @y@", name: "x"},
		// 空名字不是标记
		{in: "@@ hi", body: "@@ hi"},
	}
	for _, c := range cases {
		body, name, reset := splitStealthTarget(c.in)
		if body != c.body || name != c.name || reset != c.reset {
			t.Errorf("splitStealthTarget(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, body, name, reset, c.body, c.name, c.reset)
		}
	}
}

func TestStealthCommitSticksToNamedEntry(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	res, err := svc.StealthCommit("@随记@ 第一条")
	if err != nil {
		t.Fatalf("StealthCommit: %v", err)
	}
	if res.Target != "随记" || !res.Created {
		t.Fatalf("第一次应该新建「随记」: %+v", res)
	}
	if res.Chars != 3 {
		t.Errorf("chars = %d, want 3", res.Chars)
	}

	// 不带标记也沿用上次的目标：追加，不新建
	res, err = svc.StealthCommit("第二条")
	if err != nil {
		t.Fatalf("StealthCommit 2: %v", err)
	}
	if res.Created || res.Target != "随记" {
		t.Fatalf("第二次应该追加到「随记」: %+v", res)
	}

	e, ok := svc.store.FindEntryByTitle("随记")
	if !ok {
		t.Fatal("找不到「随记」")
	}
	if e.Content != "第一条\n\n第二条" {
		t.Errorf("content = %q", e.Content)
	}

	// @-@ 清掉目标：之后每次都是一条新的
	res, err = svc.StealthCommit("@-@ 独立的一条")
	if err != nil {
		t.Fatalf("StealthCommit 3: %v", err)
	}
	if res.Target != "" || !res.Created {
		t.Fatalf("@-@ 之后应该是无目标的新条目: %+v", res)
	}
	res, err = svc.StealthCommit("又一条")
	if err != nil {
		t.Fatalf("StealthCommit 4: %v", err)
	}
	if !res.Created {
		t.Fatalf("没有目标时每次都应新建: %+v", res)
	}
}

func TestStealthCommitIgnoresEmpty(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	if _, err := svc.StealthCommit("   \n  "); err == nil {
		t.Error("空缓冲不该写出条目")
	}
	if n := len(svc.store.ListEntries(EntryQuery{})); n != 0 {
		t.Errorf("库里不该有内容, got %d", n)
	}
}

func TestFindEntryByTitleIsCaseInsensitive(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	created, err := svc.store.SaveEntry(Entry{Title: "Inbox", Content: "第一行"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := svc.store.FindEntryByTitle(" inbox ")
	if !ok || got.ID != created.ID {
		t.Fatalf("FindEntryByTitle 应忽略大小写与空白: ok=%v id=%q", ok, got.ID)
	}
	if _, ok := svc.store.FindEntryByTitle("不存在"); ok {
		t.Error("不该凭空找到条目")
	}
}

func TestAppendTextToEntryKeepsFlags(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	created, err := svc.store.SaveEntry(Entry{
		Title:   "随记",
		Content: "第一行 #工作",
		Pinned:  true,
		Actions: []string{ActionClipboard},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.AppendTextToEntry(created.ID, "第二行"); err != nil {
		t.Fatalf("AppendTextToEntry: %v", err)
	}
	got, _ := svc.store.GetEntry(created.ID)
	if !got.Pinned || !got.HasAction(ActionClipboard) || got.Title != "随记" {
		t.Errorf("追加不该清掉条目属性: %+v", got)
	}
	if !strings.HasPrefix(got.Content, "第一行 #工作\n\n第二行") {
		t.Errorf("content = %q", got.Content)
	}
}
