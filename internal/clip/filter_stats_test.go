package clip

import (
	"testing"
	"time"
)

// 侧栏角标（EntryStats）与"点进去能看到的条数"必须是同一个数字。
//
// 这两边以前各写一套：角标手写 SQL 用 `actions LIKE '%,clipboard,%'`（"有这个能力"），
// 而「剪贴板」筛选器按 action 取，于是老数据 actions 为空的行两边一起漏；手写却带
// clipboard 能力的行又被算进"剪贴板"。真机实测「我写的 3 + 剪贴板 2」凑不出「全部 6」。
// 现在 EntryStats 直接复用 BuiltinFilters() 的查询，这个用例把口径钉死。

// mixedStore 造一批覆盖各维度的条目，返回它的统计口径。
func mixedStore(t *testing.T) *ClipService {
	t.Helper()
	svc := newTestService(t, t.TempDir())

	must := func(c Captured) *Entry {
		e, _, err := svc.store.SaveCapture(c)
		if err != nil {
			t.Fatalf("SaveCapture: %v", err)
		}
		return e
	}
	// 剪贴板采集（actions 带 clipboard）
	must(Captured{Kind: KindText, Text: "copied", Preview: "copied", Hash: "h-copied", Size: 6})
	// 剪贴板采集但 actions 丢了（老数据的样子）
	noCap := must(Captured{Kind: KindText, Text: "copied-no-cap", Preview: "p", Hash: "h-nocap", Size: 5})
	if _, err := svc.store.db.Exec(`UPDATE entries SET actions = '' WHERE id = ?`, noCap.ID); err != nil {
		t.Fatal(err)
	}
	// 手写
	if _, err := svc.SaveEntry(Entry{Content: "handwritten", Kind: KindText, Source: SourceManual, Hash: "h-manual"}); err != nil {
		t.Fatal(err)
	}
	// 置顶 + 归档 + 片段
	pinned := must(Captured{Kind: KindText, Text: "pinned one", Preview: "p", Hash: "h-pinned", Size: 4})
	if _, err := svc.SetEntryPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	arch := must(Captured{Kind: KindText, Text: "archived one", Preview: "p", Hash: "h-arch", Size: 4})
	if _, err := svc.SetEntryArchived(arch.ID, true); err != nil {
		t.Fatal(err)
	}
	snip := must(Captured{Kind: KindText, Text: "snippet one", Preview: "p", Hash: "h-snip", Size: 4})
	if _, err := svc.PromoteToSnippet(snip.ID, "sn1"); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestEntryStatsMatchBuiltinFilterCounts(t *testing.T) {
	svc := mixedStore(t)
	es := svc.store.EntryStats()

	byID := map[string]EntryQuery{}
	for _, f := range BuiltinFilters() {
		byID[f.ID] = f.Query
	}
	// 侧栏角标 → 对应内置筛选器（Sidebar.tsx 的映射就这几对）
	pairs := []struct {
		name string
		stat int
		id   string
	}{
		{"all", es.Total, "all"},
		{"mine", es.Manual, "mine"},
		{"clipboard", es.Clipboard, "clipboard"},
		{"snippets", es.Snippets, "snippets"},
		{"pinned", es.Pinned, "pinned"},
		{"archived", es.Archived, "archived"},
	}
	for _, p := range pairs {
		want := svc.store.CountEntriesMatching(byID[p.id])
		if p.stat != want {
			t.Errorf("%s 角标 = %d，筛选器能列出 %d 条（必须一致）", p.name, p.stat, want)
		}
		if n := len(svc.store.ListEntries(byID[p.id])); n != want {
			t.Errorf("%s 列表返回 %d 条，计数是 %d", p.name, n, want)
		}
	}

	// 「我写的」+「剪贴板」= 全部（两者是互补的入口，都按来源划分）
	if got := es.Manual + es.Clipboard; got != es.Total {
		t.Errorf("我写的(%d) + 剪贴板(%d) = %d，但全部是 %d", es.Manual, es.Clipboard, got, es.Total)
	}
	// 今天：今天造的非归档条目一条都不能漏（归档那条不进「今天」）
	if es.Today != es.Total {
		t.Errorf("today = %d, want %d（本用例的非归档条目都是今天建的）", es.Today, es.Total)
	}
}

func TestClipboardFilterCountsBySourceNotCapability(t *testing.T) {
	svc := mixedStore(t)
	// actions 空但来源是剪贴板：以前既不在「剪贴板」里、也不在快速粘贴窗里。
	if got, want := svc.store.CountEntriesMatching(EntryQuery{Source: string(SourceClipboard)}), 4; got != want {
		t.Errorf("按来源统计的剪贴板条目 = %d, want %d", got, want)
	}
	clipQ := EntryQuery{}
	for _, f := range BuiltinFilters() {
		if f.ID == "clipboard" {
			clipQ = f.Query
		}
	}
	if clipQ.Source != string(SourceClipboard) {
		t.Errorf("「剪贴板」应当按来源筛（Source），实际是 %+v", clipQ)
	}
}

func TestSidebarCountsEmitOnEdits(t *testing.T) {
	var events []string
	svc, err := NewClipService(t.TempDir(), Hooks{Emit: func(name string, _ any) {
		events = append(events, name)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Shutdown)

	sawStats := func(label string, fn func()) {
		events = nil
		fn()
		for _, e := range events {
			if e == "clip:stats" {
				return
			}
		}
		t.Errorf("%s 之后没有发 clip:stats，侧栏数字不会刷新（收到的事件：%v）", label, events)
	}

	e, err := svc.SaveEntry(Entry{Content: "emit me", Kind: KindText, Source: SourceManual, Hash: "h-emit"})
	if err != nil {
		t.Fatal(err)
	}
	sawStats("新建/编辑条目", func() {
		if _, err := svc.SaveEntry(e); err != nil {
			t.Fatal(err)
		}
	})
	sawStats("置顶", func() { _, _ = svc.SetEntryPinned(e.ID, true) })
	sawStats("归档", func() { _, _ = svc.SetEntryArchived(e.ID, true) })
	sawStats("删除", func() { _ = svc.DeleteEntry(e.ID) })
}

func TestBackfillRestoresClipboardCapability(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewClipService(dir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	e, _, err := svc.store.SaveCapture(Captured{Kind: KindText, Text: "lost capability", Preview: "p", Hash: "h-lost", Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟老数据：来源是剪贴板，actions 却是空的；同时抹掉"已完成"标记。
	if _, err := svc.store.db.Exec(`UPDATE entries SET actions = '' WHERE id = ?`, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.db.Exec(`DELETE FROM meta WHERE k = ?`, clipboardCapabilityBackfillKey); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown()

	reopened, err := NewClipService(dir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown()

	got, ok := reopened.store.GetEntry(e.ID)
	if !ok {
		t.Fatal("条目不见了")
	}
	if !got.HasAction(ActionClipboard) {
		t.Errorf("回填没生效：actions = %v（快速粘贴窗按 action 取列表，这条会消失）", got.Actions)
	}
	// 幂等：再开一次不该重复追加
	reopened.Shutdown()
	again, err := NewClipService(dir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	if got, _ := again.store.GetEntry(e.ID); len(got.Actions) != 1 {
		t.Errorf("回填不幂等：actions = %v", got.Actions)
	}
	_ = time.Now()
}
