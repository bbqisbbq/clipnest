// 诊断小工具：打开一份 clipnest 库的**副本**，打印条目数、来源/类型分布、
// 最近与最早的条目时间，看数据有没有"莫名变少/变新"。
//
// 用法：go run ./scratch/dbsnap <db目录>
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	abs, _ := filepath.Abs(dir)
	st, err := clip.Open(abs)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer st.Close()

	fmt.Println("dir:", abs)
	fmt.Println("entries(all):", st.CountEntries())
	s := st.Stats()
	fmt.Printf("stats(archived excluded): total=%d pinned=%d storageBytes=%d lastCapture=%s\n", s.Total, s.Pinned, s.StorageBytes, s.LastCapture)
	keys := make([]string, 0, len(s.ByKind))
	for k, v := range s.ByKind {
		keys = append(keys, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(keys)
	fmt.Println("byKind:", keys)

	list := st.ListEntries(clip.EntryQuery{Limit: 500})
	bySource := map[string]int{}
	var oldest, newest time.Time
	for _, e := range list {
		bySource[string(e.Source)]++
		if oldest.IsZero() || e.CreatedAt.Before(oldest) {
			oldest = e.CreatedAt
		}
		if e.CreatedAt.After(newest) {
			newest = e.CreatedAt
		}
	}
	fmt.Println("listed:", len(list), "oldest:", oldest.Format(time.RFC3339), "newest:", newest.Format(time.RFC3339))
	keys = keys[:0]
	for k, v := range bySource {
		keys = append(keys, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(keys)
	fmt.Println("bySource:", keys)
}
