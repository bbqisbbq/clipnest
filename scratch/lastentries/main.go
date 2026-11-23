// 打印库里最新的几条条目正文（e2e 断言用）。
//
//	go run ./scratch/lastentries "C:\path\to\dataDir" [n]
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/lastentries <数据目录> [条数]")
		os.Exit(1)
	}
	n := 5
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil && v > 0 {
			n = v
		}
	}
	st, err := clip.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer st.Close()

	for _, e := range st.ListEntries(clip.EntryQuery{Limit: n, Tail: true}) {
		fmt.Printf("%s | source=%s | tags=%v | %q\n", e.CreatedAt.Format("15:04:05"), e.Source, e.Tags, e.Content)
	}
}
