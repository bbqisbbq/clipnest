package clip

import (
	"testing"

	"github.com/bbqisbbq/clipnest/internal/embed"
)

// 回归：**"还没有向量"的状态必须真的写得进库**。
//
// 2.13.0 装出来就是这么翻的：`entry_vectors.vec` 是 `BLOB NOT NULL`，而
// SaveEntryVector 在 pending / failed / skipped / unsupported 四种状态下传的是
// nil []byte —— Go 的 nil slice 绑到 SQLite 上是 NULL，被 NOT NULL 直接拒掉。
// 于是整个表一行都没有，设置页永远显示「已索引 0 条」，条目也永远不会被编码。
//
// 为什么单测当初没抓到：`runEmbedForEntry` 里那次 pending 写失败被 `_ =` 忽略了，
// 而**有资源**的情况下紧接着真编码会成功、done 带着真 blob 写进去，用例照样绿。
// 只有"资源缺失/编码失败"这条路上才会露出来 —— 也就是用户第一次装完、
// 刚点下载还没下完的那段时间。所以这里直接盯存储层，不绕道编码。
func TestSaveEntryVectorAcceptsNilVec(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, err := svc.store.SaveEntry(Entry{Content: "随便一条", Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}

	for _, state := range []string{
		VectorStatePending,
		VectorStateFailed,
		VectorStateSkipped,
		VectorStateUnsupported,
	} {
		// nil 是"还没有向量"的自然写法，存储层必须兜住
		if err := svc.store.SaveEntryVector(e.ID, "h", state, nil); err != nil {
			t.Fatalf("state=%s 写 nil 向量失败: %v", state, err)
		}
		row, ok := svc.store.EntryVector(e.ID)
		if !ok {
			t.Fatalf("state=%s 写完读不回来", state)
		}
		if row.State != state {
			t.Errorf("state 存成了 %q，期望 %q", row.State, state)
		}
		if len(row.Vec) != 0 {
			t.Errorf("state=%s 的空向量应该存成 0 字节，实际 %d 字节", state, len(row.Vec))
		}
	}

	// 空 BLOB 不能被当成有效向量参与检索
	if v, ok := embed.VectorFromBlob(nil); ok {
		t.Errorf("空 blob 不该反序列化出向量，得到 %v", v.Scale)
	}
	if _, ok := embed.DotBlob([]byte{}, embed.Vector{Q: make([]int8, embed.Dim), Scale: 1}); ok {
		t.Error("空 blob 不该能算点积")
	}
}

// 有向量之后 done 状态正常，并且 LiveVectors 只收 done 的那些。
func TestSaveEntryVectorDoneVisible(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, err := svc.store.SaveEntry(Entry{Content: "会被编码的", Source: SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	vec := make([]float32, embed.Dim)
	vec[3] = 1
	blob := embed.Quantize(vec).Blob()
	if err := svc.store.SaveEntryVector(e.ID, "h1", VectorStateDone, blob); err != nil {
		t.Fatalf("写 done 向量失败: %v", err)
	}
	if hits := svc.store.LiveVectors(); len(hits) != 1 || hits[0].ID != e.ID {
		t.Fatalf("LiveVectors = %v，期望只有 %s", hits, e.ID)
	}
	done, pending, failed := svc.store.VectorStats()
	if done != 1 || pending != 0 || failed != 0 {
		t.Errorf("VectorStats = done %d / pending %d / failed %d，期望 1/0/0", done, pending, failed)
	}
}
