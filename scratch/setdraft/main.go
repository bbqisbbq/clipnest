// 往库里写一条快捷输入草稿（e2e 用：不打字也能让输入框带着 `#wr` 起来）。
// 走的是 meta（composer.draft / composer.draftAt），前端启动时会读它。
//
//	go run ./scratch/setdraft "C:\path\to\dataDir" "#wr"
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/setdraft <数据目录> <草稿文本>")
		os.Exit(1)
	}
	st, err := clip.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer st.Close()

	if err := st.SetMeta("composer.draft", os.Args[2]); err != nil {
		panic(err)
	}
	if err := st.SetMeta("composer.draftAt", time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")); err != nil {
		panic(err)
	}
	fmt.Printf("draft=<%s>\n", st.Meta("composer.draft"))
}
