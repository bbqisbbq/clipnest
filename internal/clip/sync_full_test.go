package clip

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 「同步包全量」相关的用例：条目不再按 500 截断、附件预算改成可轮转、
// 以及自动同步间隔的上限。这三件事是一起改的——包里放全量之后，隔半小时
// 重推一个越来越大的包才是新的风险，所以要靠"放宽间隔 + 内容没变就不推"兜住。

// TestAllEntriesIgnoresPageLimit：AllEntries 不能被 ListEntries 的 500 上限截断，
// 否则"全量同步"只是个说法。
func TestAllEntriesIgnoresPageLimit(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	const total = 620
	for i := 0; i < total; i++ {
		if _, err := svc.store.SaveEntry(Entry{Content: "all-" + pad(i), Source: SourceManual}); err != nil {
			t.Fatal(err)
		}
	}

	all := svc.store.AllEntries()
	if len(all) != total {
		t.Fatalf("AllEntries returned %d, want %d", len(all), total)
	}
	if all[0].Content != "all-"+pad(0) {
		t.Errorf("AllEntries 顺序应为最旧在前，实际首条是 %q", all[0].Content)
	}
	if all[len(all)-1].Content != "all-"+pad(total-1) {
		t.Errorf("AllEntries 末条应为最新，实际是 %q", all[len(all)-1].Content)
	}
	// 界面那条路仍然有自己的上限：全量是同步专用的，不该顺手把列表页也放大。
	if page := svc.store.ListEntries(EntryQuery{Limit: 0}); len(page) != 100 {
		t.Errorf("ListEntries(Limit:0) returned %d, want 100 (既有分页语义不能变)", len(page))
	}
}

// TestPlanBlobUploadPrefersOwed：上一轮没发出去的附件必须排在下一轮最前面。
// 否则"按条目顺序扣预算"会让排后面的附件永远轮不上。
func TestPlanBlobUploadPrefersOwed(t *testing.T) {
	cands := []blobCandidate{
		{key: "a", size: 60},
		{key: "b", size: 60},
		{key: "c", size: 30},
	}

	first := planBlobUpload(cands, 100, nil)
	if !sameKeys(first.chosen, "a", "c") {
		t.Fatalf("第一轮选了 %v，want [a c]", keysOf(first.chosen))
	}
	if !first.deferred["b"] || len(first.deferred) != 1 {
		t.Fatalf("第一轮 deferred = %v，want 只有 b", first.deferred)
	}

	second := planBlobUpload(cands, 100, first.deferred)
	if !sameKeys(second.chosen, "b", "c") {
		t.Fatalf("第二轮选了 %v，want [b c]（欠下的 b 优先）", keysOf(second.chosen))
	}
	if second.deferred["a"] != true {
		t.Fatalf("第二轮 deferred = %v，want 含 a", second.deferred)
	}

	third := planBlobUpload(cands, 100, second.deferred)
	if !sameKeys(third.chosen, "a", "c") {
		t.Fatalf("第三轮选了 %v，want [a c]", keysOf(third.chosen))
	}
}

// TestPlanBlobUploadOversize：单个附件就超过整个预算时，不能一直占着 deferred
// 名额冒充"下轮再传"——它永远传不出去，必须单独报出来。
func TestPlanBlobUploadOversize(t *testing.T) {
	cands := []blobCandidate{
		{key: "huge", size: 500},
		{key: "small", size: 10},
	}
	plan := planBlobUpload(cands, 100, nil)
	if plan.oversize != 1 {
		t.Fatalf("oversize = %d, want 1", plan.oversize)
	}
	if plan.deferred["huge"] {
		t.Error("超大附件不该进 deferred（那是给预算不够用的）")
	}
	if !sameKeys(plan.chosen, "small") {
		t.Fatalf("选了 %v，want [small]", keysOf(plan.chosen))
	}
}

// TestPlanBlobUploadZeroBudget：预算为 0 时全部挂账，一个都不发，也不崩。
func TestPlanBlobUploadZeroBudget(t *testing.T) {
	plan := planBlobUpload([]blobCandidate{{key: "a", size: 1}}, 0, nil)
	if len(plan.chosen) != 0 || !plan.deferred["a"] {
		t.Fatalf("预算 0 时应全部 deferred，实际 chosen=%v deferred=%v",
			keysOf(plan.chosen), plan.deferred)
	}
}

// TestDeferredBlobKeysRoundTrip：挂账清单存进 meta 再读出来必须一致（空串=没有）。
func TestDeferredBlobKeysRoundTrip(t *testing.T) {
	if got := deferredBlobKeys(""); len(got) != 0 {
		t.Fatalf("空串应解析为空集，得到 %v", got)
	}
	in := map[string]bool{"entry-abc.png": true, "entry-def.bin": true}
	out := deferredBlobKeys(encodeDeferredBlobs(in))
	if len(out) != len(in) {
		t.Fatalf("往返后 %d 个，want %d", len(out), len(in))
	}
	for k := range in {
		if !out[k] {
			t.Errorf("往返后丢了 %q", k)
		}
	}
}

// TestSyncIntervalClamped：间隔来自设置页与手改的 settings.json，两头都要钳。
// 0 = 只在手动点同步；上限一天，避免"每半小时重推一个全量包"。
func TestSyncIntervalClamped(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	for _, tc := range []struct {
		in   int
		want int
	}{
		{in: 120, want: 120},
		{in: 0, want: 0},
		{in: -30, want: 0},
		{in: MaxSyncIntervalMin + 1, want: MaxSyncIntervalMin},
		{in: 100000, want: MaxSyncIntervalMin},
	} {
		st := svc.store.Settings()
		st.SyncIntervalMin = tc.in
		got, err := svc.SaveSettings(st)
		if err != nil {
			t.Fatalf("SaveSettings(interval=%d): %v", tc.in, err)
		}
		if got.SyncIntervalMin != tc.want {
			t.Fatalf("SaveSettings(interval=%d) stored %d, want %d", tc.in, got.SyncIntervalMin, tc.want)
		}
	}
}

// TestLoadSettingsClampsSyncInterval：手改 settings.json 写成 99999 分钟，读出时
// 必须落到上限，而不是让定时器等上两个月。
func TestLoadSettingsClampsSyncInterval(t *testing.T) {
	dir := t.TempDir()
	st := DefaultSettings()
	st.SyncIntervalMin = 99999
	if err := writeSettingsFile(dir, st); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	peeked, err := PeekSettings(dir)
	if err != nil {
		t.Fatalf("PeekSettings: %v", err)
	}
	if peeked.SyncIntervalMin != MaxSyncIntervalMin {
		t.Fatalf("PeekSettings interval = %d, want %d", peeked.SyncIntervalMin, MaxSyncIntervalMin)
	}
}

// TestDefaultSyncIntervalIsRelaxed：全量包 + 30 分钟的组合是浪费配额的老配置。
func TestDefaultSyncIntervalIsRelaxed(t *testing.T) {
	if got := DefaultSettings().SyncIntervalMin; got < 60 {
		t.Fatalf("默认同步间隔 = %d 分钟，全量包下应当放宽到 ≥60", got)
	}
}

// TestPackageSizeProbe 不是回归用例，是一把"量尺"：给真实库的快照算一遍全量包多大，
// 好回答"全量之后我的包会不会大到传不动"。
//
// 默认跳过。用法：
//
//	$env:CLIPNEST_PKGSIZE_DIR = "C:\temp\snapshot"   # 里面放 VACUUM INTO 出来的 clipnest.db
//	go test ./internal/clip -run TestPackageSizeProbe -v
//
// 快照要用 VACUUM INTO 生成，且把 entry_files\ 一起拷过去（storage='file' 的附件要靠它）。
func TestPackageSizeProbe(t *testing.T) {
	dir := os.Getenv("CLIPNEST_PKGSIZE_DIR")
	if dir == "" {
		t.Skip("未设置 CLIPNEST_PKGSIZE_DIR（这是一把量尺，不是回归用例）")
	}
	svc := newTestService(t, dir)
	total := svc.store.CountAllEntries()
	active := svc.store.CountEntries()
	pkg, _, _, err := svc.buildOwnPackage(map[string]bool{}, nil, svc.store.Settings())
	if err != nil {
		t.Fatalf("buildOwnPackage: %v", err)
	}
	parsed, err := parsePackage(pkg)
	if err != nil {
		t.Fatalf("parsePackage: %v", err)
	}
	t.Logf("库里 %d 条（其中可见 %d、归档 %d）→ 包内 %d 条 / %d 个附件 / %.2f MiB",
		total, active, total-active, len(parsed.entries), len(parsed.blobs), float64(len(pkg))/(1<<20))
	if total > 0 && len(parsed.entries) != total {
		t.Fatalf("包内条目 %d ≠ 库里 %d（全量没生效）", len(parsed.entries), total)
	}
}

// TestSyncCarriesArchivedEntries：归档条目以前**根本进不了包**。
//
// 老实现用 `ListEntries` 组包，而它的 where() 永远带一句 `archived = 0`：所以"归档"
// 在一台机器上是私事，另一台永远看不到（对方那台还能搜到、还能出现在快速粘贴窗）。
// 全量之后归档条目也跟着走，带的是 archived=1 这个标记本身。
func TestSyncCarriesArchivedEntries(t *testing.T) {
	base, _, _ := newTestDAV(t)
	a := syncedService(t, base)

	e, err := a.store.SaveEntry(Entry{Content: "归档过的备忘", Source: SourceManual})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if _, err := a.SetEntryArchived(e.ID, true); err != nil {
		t.Fatalf("SetEntryArchived: %v", err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	b := syncedService(t, base)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	archived := b.store.ListEntries(EntryQuery{Archived: true})
	if len(archived) != 1 || archived[0].Content != "归档过的备忘" {
		t.Fatalf("对端没拿到归档条目：%+v", archived)
	}
	if visible := b.store.CountEntries(); visible != 0 {
		t.Errorf("归档条目不该出现在可见列表里，CountEntries = %d", visible)
	}
}

func writeSettingsFile(dir string, st Settings) error {
	blob, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.json"), blob, 0o644)
}

func keysOf(cands []blobCandidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.key)
	}
	return out
}

func sameKeys(cands []blobCandidate, want ...string) bool {
	got := keysOf(cands)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
