package clip

import (
	"os"
	"path/filepath"
	"testing"
)

// 端到端：真模型 + 真检索。要 23MB 资源才跑，所以默认跳过。
//
//	$env:CLIPNEST_EMBED_LIVE = "1"; go test ./internal/clip -run TestEmbedLive -v
//
// 它验的正是这个功能存在的理由：
//
//	同一句查询 —— FTS(trigram) 一条都找不到，混合检索把目标排到第一。
//
// 单测（search_hybrid_test.go）用假向量验的是管道与公式；这里验的是"真的能用"。
func TestEmbedLiveSearch(t *testing.T) {
	if os.Getenv("CLIPNEST_EMBED_LIVE") != "1" {
		t.Skip("设 CLIPNEST_EMBED_LIVE=1 才跑（需要 23MB 模型资源）")
	}
	if !embedAvailableForLive() {
		t.Skip("没有模型资源（scratch/embed-model/），跳过")
	}

	// 资源目录**不能**用 t.TempDir()：onnxruntime.dll 一旦被进程加载，
	// Windows 不允许删除这个文件（LoadLibrary 不带 FILE_SHARE_DELETE），
	// 用例本身会通过、却在清理阶段报 "Access is denied"。所以放到仓库里一个
	// gitignore 掉的固定位置，跨次复用（与产品"模型装一次反复用"也一致）。
	dir := filepath.Join(liveAssetsDir(), "live-data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 上一次的库会把条目带过来：每次跑之前清掉数据文件，但**保留资源**
	for _, name := range []string{"clipnest.db", "clipnest.db-wal", "clipnest.db-shm"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	copyLive := func(src, dst string) {
		if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
			return // 已经有一份了，别每次重复拷 21MB
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("读 %s: %v", src, err)
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			t.Fatalf("写 %s: %v", dst, err)
		}
	}
	assets := filepath.Join(dir, "embed")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	copyLive(liveModelPath(), filepath.Join(assets, "embed_model.onnx"))
	copyLive(liveVocabPath(), filepath.Join(assets, "embed_vocab.txt"))
	copyLive(liveLibraryPath(), filepath.Join(assets, "onnxruntime.dll"))

	svc := newTestService(t, dir)
	st := svc.store.Settings()
	st.EmbedSearch = true
	if err := svc.store.SaveSettings(st); err != nil {
		t.Fatal(err)
	}

	corpus := []string{
		"webdav 坚果云 同步失败 检查服务器地址是否一字不差",
		"docker run -d --name pg -p 5432:5432 postgres:16",
		"sudo systemctl restart nginx",
		"明天下午三点和产品对齐下版本需求",
		"记得买牛奶和鸡蛋",
	}
	ids := make(map[string]string, len(corpus))
	for _, c := range corpus {
		e, err := svc.store.SaveEntry(Entry{Content: c, Source: SourceManual})
		if err != nil {
			t.Fatal(err)
		}
		ids[c] = e.ID
		if err := svc.runEmbedForEntry(e.ID); err != nil {
			t.Fatalf("编码 %q: %v", c, err)
		}
	}

	done, _, failed := svc.store.VectorStats()
	if done != len(corpus) || failed != 0 {
		t.Fatalf("向量落库不对：done=%d failed=%d（期望 %d/0）", done, failed, len(corpus))
	}

	cases := []struct {
		query  string
		target string
		// topN 是容许的名次（1 = 必须第一）
		topN int
	}{
		// 这句和"同步失败/服务器地址"一个词都不重，FTS 必死
		{"云端传不上去", "webdav 坚果云 同步失败 检查服务器地址是否一字不差", 1},
		{"重启网页服务器", "sudo systemctl restart nginx", 2},
		{"本地起一个数据库容器", "docker run -d --name pg -p 5432:5432 postgres:16", 2},
	}

	for _, c := range cases {
		// 先确认字面那一路确实找不到（否则这条用例说明不了问题）
		literal := svc.store.ListEntries(EntryQuery{Text: c.query, Limit: 50, Tail: true})
		got := svc.GetEntries(EntryQuery{Text: c.query, Limit: 50, Tail: true})

		t.Logf("%-16q 字面命中 %d 条 → 混合返回 %d 条", c.query, len(literal), len(got))
		if len(got) == 0 {
			t.Errorf("「%s」混合检索一条都没返回", c.query)
			continue
		}
		// 结果按名次**倒着**排（最好的贴着输入框，即数组末尾）——所以名次要从后往前数。
		// 见 Store.ListEntries 里 ranked 分支的说明。
		rank := -1
		for i, e := range got {
			if e.ID == ids[c.target] {
				rank = len(got) - i
				break
			}
		}
		t.Logf("   「%s」目标名次 = %d（要求 ≤%d）", c.query, rank, c.topN)
		if rank < 0 || rank > c.topN {
			t.Errorf("「%s」目标名次 = %d（期望 ≤%d）；最后三条（相关度最高）是：", c.query, rank, c.topN)
			from := len(got) - 3
			if from < 0 {
				from = 0
			}
			for i := from; i < len(got); i++ {
				t.Errorf("   %d. %s", len(got)-i, got[i].Content)
			}
		}
	}

	// 关掉开关之后必须回到纯字面（同一个查询变成 0 条）
	st.EmbedSearch = false
	if err := svc.store.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := svc.GetEntries(EntryQuery{Text: "云端传不上去", Limit: 50}); len(got) != 0 {
		t.Errorf("关掉语义搜索后仍返回 %d 条（应回到字面，即 0 条）", len(got))
	}
}

// ---- 资源路径（本机下载过才有）------------------------------------------

func liveAssetsDir() string {
	if p := os.Getenv("CLIPNEST_EMBED_MODEL_DIR"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "scratch", "embed-model")
}

func liveModelPath() string {
	p := filepath.Join(liveAssetsDir(), "embed_model.onnx")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return filepath.Join(liveAssetsDir(), "model_quantized.onnx")
}

func liveVocabPath() string { return filepath.Join(liveAssetsDir(), "vocab.txt") }

// liveLibraryPath 用本机已装的 OCR 资源里那份 onnxruntime.dll（和产品里
// embed.LibraryPath 的"复用 OCR 那份"是同一个思路）。
func liveLibraryPath() string {
	if p := os.Getenv("CLIPNEST_ONNXRUNTIME"); p != "" {
		return p
	}
	return filepath.Join(os.Getenv("APPDATA"), "ClipNest", "ocr", "onnxruntime.dll")
}

func embedAvailableForLive() bool {
	for _, p := range []string{liveModelPath(), liveVocabPath(), liveLibraryPath()} {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}
