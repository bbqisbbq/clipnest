package clip

// 老库升级的回归测试。
//
// 为什么要有这一条：2.9.5 给 entries 加了 ocr_text / ocr_state，但**只改了建表语句、
// 漏了 ensureColumn 迁移** —— 全新的库没事（CREATE TABLE 里就有这两列），老库则
// SELECT 与 INSERT 全报 `no such column: ocr_text`，时间线直接空掉。
// 所有单元测试用的都是新库，所以当时全绿。这条测试专门模拟"2.9.4 的库"。

import (
	"testing"
)

func TestOpenMigratesOCRColumns(t *testing.T) {
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("首次 Open: %v", err)
	}
	// 造一条数据，然后把这轮新加的两列删掉 —— 这就是 2.9.4 的库长什么样
	if _, err := st.SaveEntry(Entry{Content: "老库里的条目", Source: SourceManual}); err != nil {
		t.Fatalf("写老数据: %v", err)
	}
	for _, col := range []string{"ocr_text", "ocr_state"} {
		if _, err := st.db.Exec(`ALTER TABLE entries DROP COLUMN ` + col); err != nil {
			t.Skipf("这个 SQLite 版本不支持 DROP COLUMN（%v），跳过", err)
		}
	}
	st.Close()

	// 再开一次：迁移必须把列补回来
	st2, err := Open(dir)
	if err != nil {
		t.Fatalf("老库升级时 Open 失败: %v", err)
	}
	defer st2.Close()

	got := st2.ListEntries(EntryQuery{})
	if len(got) != 1 {
		t.Fatalf("老库升级后列不出条目（拿到 %d 条）", len(got))
	}
	if got[0].OcrText != "" || got[0].OcrState != "" {
		t.Errorf("新列默认值应为空: %q / %q", got[0].OcrText, got[0].OcrState)
	}

	// 升级后写入与 OCR 落库都要正常（INSERT 里带这两个列）
	e, err := st2.SaveEntry(Entry{Content: "# 升级后写的\n正文", Source: SourceManual})
	if err != nil {
		t.Fatalf("老库升级后 SaveEntry 失败: %v", err)
	}
	if err := st2.SetEntryOCR(e.ID, "识别出来的字", OcrStateDone); err != nil {
		t.Fatalf("老库升级后 SetEntryOCR 失败: %v", err)
	}
	if ids := st2.ListEntries(EntryQuery{Text: "识别出来"}); len(ids) != 1 {
		t.Errorf("老库升级后 OCR 文字搜不到: %v", ids)
	}
}
