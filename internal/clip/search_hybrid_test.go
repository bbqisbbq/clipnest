package clip

import (
	"strings"
	"testing"

	"github.com/bbqisbbq/clipnest/internal/embed"
)

// 混合检索的三块各自可测：
//   · RRF 融合公式（纯函数，错了会让结果莫名偏，界面上看不出来）；
//   · RankIDs 怎么进 SQL 过滤与排序；
//   · 语义那一路没准备好时必须"什么都不做"（降级靠不做事，不靠分支）。
//
// 需要真模型的端到端验证在 embed_live_test.go（要下载 23MB 资源才跑）。

func TestFuseRRFOrdering(t *testing.T) {
	// a 在两路都排第一 → 稳赢；b 一路第二一路缺席；c 两路都第二
	got := fuseRRF([]string{"a", "c"}, []string{"a", "c", "b"})
	if len(got) != 3 || got[0] != "a" {
		t.Fatalf("融合结果 = %v，期望 a 第一", got)
	}
	// c 出现在两路 → 必须排在只出现一路的 b 前面
	if got[1] != "c" {
		t.Errorf("两路都命中的 c 应排第二，得到 %v", got)
	}
	if got[2] != "b" {
		t.Errorf("只命中一路的 b 应排最后，得到 %v", got)
	}
}

func TestFuseRRFEmptyLegs(t *testing.T) {
	if got := fuseRRF(nil, nil); len(got) != 0 {
		t.Errorf("两路都空应返回空，得到 %v", got)
	}
	// 只有一路也能用（字面命中但语义没命中是常态）
	got := fuseRRF([]string{"x", "y"}, nil)
	if len(got) != 2 || got[0] != "x" {
		t.Errorf("单路融合 = %v，期望 [x y]", got)
	}
}

func TestFuseRRFCapsAtLimit(t *testing.T) {
	long := make([]string, hybridTotalLimit+50)
	for i := range long {
		long[i] = string(rune('a' + i%26)) + string(rune('a'+i/26))
	}
	// 用不同 id 避免重复
	ids := make([]string, len(long))
	for i := range ids {
		ids[i] = long[i] + string(rune('0'+i%10))
	}
	if got := fuseRRF(ids); len(got) > hybridTotalLimit {
		t.Errorf("融合结果 %d 条，超过上限 %d", len(got), hybridTotalLimit)
	}
}

// RankIDs 一旦有值：按 id 过滤 + 按名次排（**倒着**：最好的在末尾，贴输入框）。
func TestRankIDsFilterAndOrder(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	var ids []string
	for _, c := range []string{"第一条 甲甲甲", "第二条 乙乙乙", "第三条 丙丙丙"} {
		e, err := svc.store.SaveEntry(Entry{Content: c, Source: SourceManual})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}

	// 名次：ids[2] 最好、ids[0] 次之；ids[1] 不在名次里，不该出现
	q := EntryQuery{RankIDs: []string{ids[2], ids[0]}, Limit: 50, Tail: true}
	got := svc.store.ListEntries(q)
	if len(got) != 2 {
		t.Fatalf("期望 2 条，得到 %d 条", len(got))
	}
	if got[len(got)-1].ID != ids[2] {
		t.Errorf("名次第 1 的应排在末尾（贴输入框），得到 %s", got[len(got)-1].Content)
	}
	if got[0].ID != ids[0] {
		t.Errorf("名次第 2 的应排在前面，得到 %s", got[0].Content)
	}
	// 名次之外的条目不该出现（第三条被排除了：它不在 RankIDs 里）
	for _, e := range got {
		if e.ID == ids[1] {
			t.Error("RankIDs 之外的条目混进来了")
		}
	}
}

// 相关性排序没有时间游标：翻第二页时应该明确"没有更多了"，而不是返回重复条目。
func TestRankedQueryIgnoresBeforeCursor(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, err := svc.store.SaveEntry(Entry{Content: "只有一条", Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	got := svc.store.ListEntries(EntryQuery{RankIDs: []string{e.ID}, Before: encodeTime(e.CreatedAt), Limit: 50})
	if len(got) != 0 {
		t.Errorf("带游标的相关性查询应返回空，得到 %d 条", len(got))
	}
}

// 向量随"被编码的正文"失效：正文、OCR 文字、标签任一变化都要重编。
func TestVectorHashInvalidation(t *testing.T) {
	base := Entry{Content: "原始正文", OcrText: "", Tags: []string{"a"}}
	h1 := entryVectorHashFields(base)

	changed := base
	changed.Content = "改过的正文"
	if entryVectorHashFields(changed) == h1 {
		t.Error("正文变了 hash 却没变")
	}

	ocr := base
	ocr.OcrText = "图里认出来的字"
	if entryVectorHashFields(ocr) == h1 {
		t.Error("OCR 文字变了 hash 却没变")
	}

	tags := base
	tags.Tags = []string{"a", "b"}
	if entryVectorHashFields(tags) == h1 {
		t.Error("标签变了 hash 却没变")
	}
}

// 纯图片（没有正文也没有 OCR）不该入队：编出来也是噪声。
//
// 这条不只是"省点算力"：用户库里 90% 是截图，正文只有 `![](memoimg:xxx)`。
// 不剥掉令牌的话，每条图片都会被编成"一串哈希字符"的向量，彼此高度相似、
// 和任何查询都半像不像，直接霸占语义结果头部（实测前五条里四条是空图）。
func TestEntryVectorTextSkipsEmpty(t *testing.T) {
	if got := entryVectorText(Entry{Content: "  \n "}); got != "" {
		t.Errorf("空白正文应返回空串，得到 %q", got)
	}
	// 只有图片令牌 == 没有内容（OCR 还没跑或者图里没字）
	for _, only := range []string{
		"![](memoimg:3aa4da16298fdeeb2edc7e1e)",
		`<img src="memoimg:3aa4da16298fdeeb2edc7e1e">`,
		"  ![](memoimg:abc)  \n",
	} {
		if got := entryVectorText(Entry{Content: only}); got != "" {
			t.Errorf("只有图片令牌应返回空串，得到 %q（输入 %q）", got, only)
		}
	}
	// 有 OCR 文字的图片条目仍然可编码（文字优先）
	if got := entryVectorText(Entry{Content: "![](memoimg:abc)", OcrText: "发票 1200 元"}); got != "发票 1200 元" {
		t.Errorf("有 OCR 文字时应只编码文字，得到 %q", got)
	}
	// 图文混排：文字留下、令牌消失
	got := entryVectorText(Entry{Content: "第一段\n\n![](memoimg:abc)\n\n第二段"})
	if strings.Contains(got, "memoimg") {
		t.Errorf("令牌没被剥掉：%q", got)
	}
	if !strings.Contains(got, "第一段") || !strings.Contains(got, "第二段") {
		t.Errorf("正文文字被误删：%q", got)
	}
}

// 语义那一路：给几条"只在一个维度上有值"的向量，最近邻必须是它自己那条。
func TestNearestIDs(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	mk := func(content string, axis int) Entry {
		e, err := svc.store.SaveEntry(Entry{Content: content, Source: SourceManual})
		if err != nil {
			t.Fatal(err)
		}
		vec := make([]float32, embed.Dim)
		vec[axis] = 1
		if err := svc.store.SaveEntryVector(e.ID, "h", VectorStateDone, embed.Quantize(vec).Blob()); err != nil {
			t.Fatal(err)
		}
		return e
	}
	a := mk("关于甲", 0)
	b := mk("关于乙", 1)
	c := mk("关于丙", 2)

	q := make([]float32, embed.Dim)
	q[1] = 1 // 指向 b
	got := svc.store.NearestIDs(embed.Quantize(q), 3)
	if len(got) != 3 {
		t.Fatalf("期望 3 条，得到 %d", len(got))
	}
	if got[0] != b.ID {
		t.Errorf("最近邻应是 b，得到 %s（a=%s b=%s c=%s）", got[0], a.ID, b.ID, c.ID)
	}
}

// 归档的条目不该出现在语义候选里（它不在时间线上）。
func TestNearestIDsSkipsArchived(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, err := svc.store.SaveEntry(Entry{Content: "会被归档的", Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	vec := make([]float32, embed.Dim)
	vec[0] = 1
	if err := svc.store.SaveEntryVector(e.ID, "h", VectorStateDone, embed.Quantize(vec).Blob()); err != nil {
		t.Fatal(err)
	}
	if got := svc.store.NearestIDs(embed.Quantize(vec), 5); len(got) != 1 {
		t.Fatalf("归档前应有 1 条，得到 %d", len(got))
	}
	e.Archived = true
	if _, err := svc.store.SaveEntry(e); err != nil {
		t.Fatal(err)
	}
	if got := svc.store.NearestIDs(embed.Quantize(vec), 5); len(got) != 0 {
		t.Errorf("归档后不该再进候选，得到 %v", got)
	}
}

// 条目删掉时向量也要跟着删：留着占地方，而且 id 复用时会让新条目"继承"旧向量。
func TestDeleteEntryDropsVector(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, err := svc.store.SaveEntry(Entry{Content: "要删的", Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	vec := make([]float32, embed.Dim)
	vec[0] = 1
	if err := svc.store.SaveEntryVector(e.ID, "h", VectorStateDone, embed.Quantize(vec).Blob()); err != nil {
		t.Fatal(err)
	}
	if !svc.store.DeleteEntry(e.ID) {
		t.Fatal("删除失败")
	}
	if _, ok := svc.store.EntryVector(e.ID); ok {
		t.Error("条目删了，向量还在")
	}
}

// 没有模型资源时 applyHybrid 必须什么都不做：RankIDs 保持为空，
// 查询原样走字面路径（这就是"没下载模型时行为完全不变"的保证）。
func TestApplyHybridDegradesWithoutAssets(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	st := svc.store.Settings()
	st.EmbedSearch = true
	if err := svc.store.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	e, err := svc.store.SaveEntry(Entry{Content: "坚果云同步失败", Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	q := EntryQuery{Text: "坚果云"}
	svc.applyHybrid(&q)
	if len(q.RankIDs) != 0 {
		t.Errorf("没有模型资源时不该设 RankIDs，得到 %v", q.RankIDs)
	}
	// 字面路径仍然能搜到
	got := svc.GetEntries(EntryQuery{Text: "坚果云", Limit: 10})
	if len(got) != 1 || got[0].ID != e.ID {
		t.Errorf("降级后字面搜索失效：%v", got)
	}
}

