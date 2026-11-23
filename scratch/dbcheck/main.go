// dbcheck：对着任意数据目录开一次库（会跑完所有迁移），把关键计数打出来。
//
//	go run ./scratch/dbcheck <数据目录>
//
// 用途：① 线上库出问题时看真实数据长什么样；② 手动触发一次迁移（例如给老库补列）。
// 只读之外只有迁移会写 —— 不新增、不删除任何条目。
package main

import (
	"fmt"
	"os"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/dbcheck <数据目录>")
		os.Exit(2)
	}
	dir := os.Args[1]
	st, err := clip.Open(dir)
	if err != nil {
		fmt.Printf("打开失败: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	all := st.ListEntries(clip.EntryQuery{Limit: 5})
	fmt.Printf("最近 5 条：\n")
	for _, e := range all {
		title := e.Title
		if title == "" {
			title = "（无标题）"
		}
		fmt.Printf("  %s  %-6s %-30q ocr=%q/%q\n", e.CreatedAt.Format("01-02 15:04"), e.Kind, title, e.OcrState, e.OcrText)
	}

	total := len(st.ListEntries(clip.EntryQuery{}))
	fmt.Printf("\nListEntries 总数（默认上限内）: %d\n", total)
	fmt.Printf("CountAllEntries: %d\n", st.CountAllEntries())
	done, pending, failed := st.OCRStats()
	fmt.Printf("OCR: done=%d pending=%d failed/unsupported=%d\n", done, pending, failed)
	if ids := st.EntryIDsNeedingOCR(5); len(ids) > 0 {
		fmt.Printf("待识别（前 5）: %v\n", ids)
	}
	fmt.Printf("数据目录: %s\n", st.Dir())
}
