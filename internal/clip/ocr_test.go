package clip

import (
	"strings"
	"testing"
)

func getEntryForTest(t *testing.T, st *Store, id string) Entry {
	t.Helper()
	e, ok := st.GetEntry(id)
	if !ok {
		t.Fatalf("条目不存在: %s", id)
	}
	return e
}

// 系统 OCR 的两个通病必须在入库前修掉，否则检索就废了。
func TestNormalizeOCRText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"中文单字之间的空格合并", "坚 果 云 WebDAV", "坚果云 WebDAV"},
		{"连续多个空格也合并", "服 务 器 地 址", "服务器地址"},
		{"英文单词间的空格保留", "Total items: 36", "Total items: 36"},
		{"中英之间保留空格", "ClipNest 同步", "ClipNest 同步"},
		{"协议头误识别", "https.//dav.jianguoyun.com", "https://dav.jianguoyun.com"},
		{"协议头分号变体", "https;//a.b", "https://a.b"},
		{"行结构保留", "第一行\n第二行", "第一行\n第二行"},
		{"连续空行压成两行", "a\n\n\n\nb", "a\n\nb"},
		{"全角空格去掉", "　中文　", "中文"},
		{"CRLF 归一", "a\r\nb", "a\nb"},
		{"空串", "", ""},
	}
	for _, c := range cases {
		if got := normalizeOCRText(c.in); got != c.want {
			t.Errorf("%s: normalizeOCRText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// OCR 文本必须能搜到（这就是这功能的全部意义），而且**不能碰 updated_at**：
// 那是"内容变过"的等价物，动了它同步指纹和时间线都会乱。
func TestSetEntryOCRIndexesAndKeepsUpdatedAt(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, _, err := svc.store.SaveCapture(Captured{
		Kind: KindImage, PNG: tinyRGBA(t), Width: 4, Height: 3, Hash: "img-ocr", Size: 100,
	})
	if err != nil {
		t.Fatalf("SaveCapture: %v", err)
	}

	// 识别前：搜不到
	if got := svc.store.ListEntries(EntryQuery{Text: "坚果云"}); len(got) != 0 {
		t.Fatalf("还没识别就搜到了 %d 条", len(got))
	}
	if ids := svc.store.EntryIDsNeedingOCR(10); len(ids) != 1 || ids[0] != e.ID {
		t.Fatalf("待识别列表不对: %v", ids)
	}

	before := getEntryForTest(t, svc.store, e.ID)
	if err := svc.store.SetEntryOCR(e.ID, "坚果云 WebDAV 配置`n同步间隔 120 分钟`n用户名 447043705", OcrStateDone); err != nil {
		t.Fatalf("SetEntryOCR: %v", err)
	}

	// 识别后：按识别出来的文字搜得到
	hits := svc.store.ListEntries(EntryQuery{Text: "坚果云"})
	if len(hits) != 1 || hits[0].ID != e.ID {
		t.Fatalf("按 OCR 文字搜不到: %v", hits)
	}
	if hits := svc.store.ListEntries(EntryQuery{Text: "447043705"}); len(hits) != 1 {
		t.Errorf("按 OCR 里的数字搜不到")
	}
	// 两个字的词走不了 trigram（FTS5 trigram 至少 3 字），必须由 LIKE 分支兜住 ocr_text
	if hits := svc.store.ListEntries(EntryQuery{Text: "同步"}); len(hits) != 1 || hits[0].ID != e.ID {
		t.Errorf("两个字的关键词搜不到 OCR 文字: %v", hits)
	}

	after := getEntryForTest(t, svc.store, e.ID)
	if after.OcrState != OcrStateDone || !strings.Contains(after.OcrText, "坚果云") {
		t.Errorf("结果没落库: state=%q text=%q", after.OcrState, after.OcrText)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("写 OCR 结果改了 updated_at（会污染同步指纹与时间线）: %v -> %v",
			before.UpdatedAt, after.UpdatedAt)
	}

	// 识别完了就不该再出现在待识别列表里
	if ids := svc.store.EntryIDsNeedingOCR(10); len(ids) != 0 {
		t.Errorf("已识别的条目还在待识别列表里: %v", ids)
	}
	done, _, _ := svc.store.OCRStats()
	if done != 1 {
		t.Errorf("OCRStats done = %d, want 1", done)
	}
}

// 非图片条目不该进识别队列（runOCRForEntry 会标 skipped，省得反复试）。
func TestOCRRunSkipsNonImage(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, err := svc.store.SaveEntry(Entry{Content: "只是一段文字", Source: SourceManual})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if _, err := svc.runOCRForEntry(e.ID, false); err != nil {
		t.Fatalf("runOCRForEntry(文本条目) 不该报错: %v", err)
	}
	got := getEntryForTest(t, svc.store, e.ID)
	if got.OcrState != OcrStateSkipped {
		t.Errorf("state = %q, want %q", got.OcrState, OcrStateSkipped)
	}
}
