// 一次性探针：把「侧栏角标」和「筛选器实际能列出来的条数」并排打出来。
//
//	go run ./scratch/filterprobe "C:\path\to\copied\dataDir"
//
// 数据目录请用**副本**（含 clipnest.db 与 -wal）：Open 会应用 WAL 并跑迁移。
// 筛选器直接迭代 BuiltinFilters()，所以探针本身永远不会和界面漂移。
package main

import (
	"fmt"
	"os"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	st, err := clip.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer st.Close()

	es := st.EntryStats()
	fmt.Printf("EntryStats: total=%d manual=%d clipboard=%d snippets=%d pinned=%d archived=%d today=%d todoOpen=%d todoDone=%d\n\n",
		es.Total, es.Manual, es.Clipboard, es.Snippets, es.Pinned, es.Archived, es.Today, es.TodoOpen, es.TodoDone)

	// 侧栏角标 → 内置筛选器（与 Sidebar.tsx 的映射一致）
	badge := map[string]int{
		"all":       es.Total,
		"mine":      es.Manual,
		"clipboard": es.Clipboard,
		"snippets":  es.Snippets,
		"pinned":    es.Pinned,
		"archived":  es.Archived,
	}

	fmt.Println("筛选器           角标  计数  列表   判断")
	for _, f := range clip.BuiltinFilters() {
		lq := f.Query
		lq.Limit = 500
		n := st.CountEntriesMatching(f.Query)
		rows := len(st.ListEntries(lq))
		verdict := "ok"
		if n != rows {
			verdict = "计数≠列表"
		}
		if b, ok := badge[f.ID]; ok {
			fmt.Printf("  %-12s %3d  %4d  %4d   %s\n", f.ID, b, n, rows, verdict)
			continue
		}
		fmt.Printf("  %-12s   -  %4d  %4d   %s（侧栏无角标）\n", f.ID, n, rows, verdict)
	}

	fmt.Println("\n全部条目（含归档）")
	for _, e := range st.ListEntries(clip.EntryQuery{Archived: true, Limit: 500}) {
		fmt.Printf("  archived=1 %s source=%-9s actions=%-12v trigger=%q pinned=%v kind=%s\n",
			short(e.ID), e.Source, e.Actions, e.TriggerKey, e.Pinned, e.Kind)
	}
	for _, e := range st.ListEntries(clip.EntryQuery{Limit: 500}) {
		fmt.Printf("  archived=0 %s source=%-9s actions=%-12v trigger=%q pinned=%v kind=%s\n",
			short(e.ID), e.Source, e.Actions, e.TriggerKey, e.Pinned, e.Kind)
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
