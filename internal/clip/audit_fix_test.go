package clip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这一组用例钉住 2026-11-20 审计里修掉的几处「说了但不工作」：
// 私有格式的字节、同步包取最新 N 条、同内容不同 id 的去重、附件回收、
// 片段的执行类型、以及删掉内置筛选器后的还原。

// TestCustomFormatPayloadIsStoredAndRecoverable：私有格式的字节必须真的落库，
// 而且两个格式名相同、内容不同的捕获不能被内容哈希并成一条。
func TestCustomFormatPayloadIsStoredAndRecoverable(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	first := []byte{0x01, 0x02, 0x03, 0xFE}
	second := []byte{0x09, 0x08, 0x07, 0xFD, 0xFC}

	for _, payload := range [][]byte{first, second} {
		if _, _, err := svc.store.SaveCapture(Captured{
			Kind:       KindCustom,
			Binary:     payload,
			FormatName: "Excel Range",
			FormatID:   49161,
			Text:       "Excel Range",
			Hash:       "cap-" + string(rune('a'+len(payload))),
			Size:       len(payload),
		}); err != nil {
			t.Fatalf("SaveCapture: %v", err)
		}
	}

	entries := svc.store.ListEntries(EntryQuery{Kind: string(KindCustom)})
	if len(entries) != 2 {
		t.Fatalf("custom entries = %d, want 2（同格式名的两份数据不能被并成一条）", len(entries))
	}

	seen := map[string]bool{}
	for _, e := range entries {
		data, _, err := svc.store.LoadEntryBlob(e.Hash)
		if err != nil {
			t.Fatalf("LoadEntryBlob(%s): %v —— 字节没落库", e.Hash, err)
		}
		seen[string(data)] = true
	}
	if !seen[string(first)] || !seen[string(second)] {
		t.Errorf("落库的 payload 不对: %d 种", len(seen))
	}
}

// TestCustomFormatEmptyPayloadIsRejected：没有字节的私有格式条目没有意义，
// 存进去只会让回填报"原始数据已不可用"。
func TestCustomFormatEmptyPayloadIsRejected(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	if _, _, err := svc.store.SaveCapture(Captured{
		Kind: KindCustom, FormatName: "Excel Range", Text: "Excel Range", Hash: "cap-empty",
	}); err == nil {
		t.Fatal("空 payload 应该报错")
	}
}

// TestSyncPackageCarriesNewestEntries：包里必须是"最新的 N 条"。
// 取最旧的那批会让最近复制的内容永远同步不出去。
func TestSyncPackageCarriesNewestEntries(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	for i := 0; i < syncMaxEntries+10; i++ {
		if _, err := svc.store.SaveEntry(Entry{
			Content: "entry-" + pad(i),
			Source:  SourceManual,
		}); err != nil {
			t.Fatal(err)
		}
	}

	pkg, _, err := svc.buildOwnPackage(map[string]bool{}, nil, svc.store.Settings())
	if err != nil {
		t.Fatalf("buildOwnPackage: %v", err)
	}
	parsed, err := parsePackage(pkg)
	if err != nil {
		t.Fatalf("parsePackage: %v", err)
	}
	if len(parsed.entries) != syncMaxEntries {
		t.Fatalf("package holds %d entries, want %d", len(parsed.entries), syncMaxEntries)
	}
	var hasNewest, hasOldest bool
	for _, e := range parsed.entries {
		if e.Content == "entry-"+pad(syncMaxEntries+9) {
			hasNewest = true
		}
		if e.Content == "entry-"+pad(0) {
			hasOldest = true
		}
	}
	if !hasNewest {
		t.Error("最新的条目不在包里")
	}
	if hasOldest {
		t.Error("最旧的条目不该在包里（说明仍然取了最旧的 N 条）")
	}
}

// TestSyncSameContentDifferentIDDoesNotDuplicate：两台机器各自复制过同一段文字，
// hash 相同、id 不同。合并后必须是同一条，不能插出重复卡片。
func TestSyncSameContentDifferentIDDoesNotDuplicate(t *testing.T) {
	base, _, _ := newTestDAV(t)
	a := syncedService(t, base)
	b := syncedService(t, base)

	const text = "两边都复制过的同一段话"
	for _, svc := range []*ClipService{a, b} {
		if _, _, err := svc.store.SaveCapture(Captured{
			Kind: KindText, Text: text, Preview: text, Hash: "shared-hash", Size: len(text),
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync again: %v", err)
	}

	for name, svc := range map[string]*ClipService{"A": a, "B": b} {
		if n := svc.store.CountEntries(); n != 1 {
			t.Errorf("%s 有 %d 条，want 1（同内容被插成了多条）", name, n)
		}
	}
}

// TestDeleteEntryReclaimsBlob：删掉条目之后，附件行与落盘文件都要回收，
// 否则"数据占用"只增不减。
func TestDeleteEntryReclaimsBlob(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	entry, _, err := svc.store.SaveCapture(Captured{
		Kind: KindImage, PNG: tinyPNG(t), Width: 2, Height: 2, Preview: "2 × 2 图像", Hash: "img-1", Size: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := memoImageToken(entry.Content)
	if token == "" {
		t.Fatal("图片条目没有 memoimg 令牌")
	}
	if _, _, err := svc.store.LoadEntryBlob(token); err != nil {
		t.Fatalf("before delete: %v", err)
	}

	if !svc.store.DeleteEntry(entry.ID) {
		t.Fatal("delete failed")
	}
	if _, _, err := svc.store.LoadEntryBlob(token); err == nil {
		t.Error("附件应该跟着条目一起消失")
	}
	if left, err := os.ReadDir(filepath.Join(svc.store.Dir(), "entry_files")); err == nil && len(left) > 0 {
		names := make([]string, 0, len(left))
		for _, f := range left {
			names = append(names, f.Name())
		}
		t.Errorf("entry_files 还留着 %v", names)
	}
}

// TestSnippetKeepsExecutionKindApart：片段的"按类型执行"不能再写进 entries.kind，
// 而且要能在库里往返一次不丢。
func TestSnippetKeepsExecutionKindApart(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	saved, err := svc.SaveSnippet(Snippet{
		Kind:    SnippetCommand,
		Title:   "重启资源管理器",
		Trigger: "rex-",
		Body:    "taskkill /f /im explorer.exe",
	}, "")
	if err != nil {
		t.Fatalf("SaveSnippet: %v", err)
	}
	if saved.Kind != SnippetCommand {
		t.Errorf("saved kind = %q, want command", saved.Kind)
	}
	if saved.Title != "重启资源管理器" {
		t.Errorf("title 没存下来: %q", saved.Title)
	}

	raw, ok := svc.store.GetEntry(saved.ID)
	if !ok {
		t.Fatal("条目不见了")
	}
	if raw.Kind != KindText {
		t.Errorf("entries.kind = %q, want text（执行类型不该混进正文类型）", raw.Kind)
	}
	if raw.SnippetKind != string(SnippetCommand) {
		t.Errorf("snippet_kind = %q, want command", raw.SnippetKind)
	}

	// 从库里读回来的片段仍然知道该"执行命令"，不是"粘贴文本"
	again, ok := svc.store.GetSnippet(saved.ID)
	if !ok {
		t.Fatal("GetSnippet 找不到")
	}
	if again.Kind != SnippetCommand {
		t.Errorf("round-trip kind = %q", again.Kind)
	}
}

// TestSetSnippetFieldsKeepsContent：改片段设置不能把正文/标签捎带回滚。
func TestSetSnippetFieldsKeepsContent(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	e, err := svc.store.SaveEntry(Entry{Content: "正文 #标签", Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.SetSnippetFields(e.ID, SnippetFields{
		Trigger: "abc-", GroupName: "常用", Apps: []string{"notepad.exe"},
	})
	if err != nil {
		t.Fatalf("SetSnippetFields: %v", err)
	}
	if updated.Content != "正文 #标签" {
		t.Errorf("正文被改了: %q", updated.Content)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "标签" {
		t.Errorf("标签被改了: %v", updated.Tags)
	}
	if updated.TriggerKey != "abc-" || updated.GroupName != "常用" {
		t.Errorf("片段字段没写进去: %+v", updated)
	}
}

// TestRestoreFiltersBringsBackBuiltins：删掉内置「置顶」之后要能还原。
func TestRestoreFiltersBringsBackBuiltins(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	if err := svc.DeleteFilter("pinned"); err != nil {
		t.Fatalf("DeleteFilter: %v", err)
	}
	if hasFilter(svc.GetFilters(), "pinned") {
		t.Fatal("置顶应该已经被隐藏")
	}

	restored, err := svc.RestoreFilters()
	if err != nil {
		t.Fatalf("RestoreFilters: %v", err)
	}
	if !hasFilter(restored, "pinned") {
		t.Error("RestoreFilters 之后「置顶」应该回来")
	}
}

func hasFilter(list []EntryFilter, id string) bool {
	for _, f := range list {
		if f.ID == id {
			return true
		}
	}
	return false
}

// pad 把序号补成定宽字符串，好让"最新"在字典序上也成立。
func pad(i int) string {
	s := strings.Repeat("0", 4) + itoa(i)
	return s[len(s)-4:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
