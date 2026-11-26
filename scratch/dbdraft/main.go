// 打印一份库里未保存的草稿（meta composer.draft / capture.draft）。
//
// 用法：
//
//	go run ./scratch/dbdraft <db目录>              # 两个槽都打（默认）
//	go run ./scratch/dbdraft <db目录> composer     # 只看主窗口输入框
//	go run ./scratch/dbdraft <db目录> capture      # 只看「记一笔」
//
// 2.12.6 起草稿是两份（见 internal/clip/draft.go）：主窗口底部「记一条」和
// 快速输入窗「记一笔」各一把键。e2e 要能分别断言，所以这里不再写死 composer。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/dbdraft <db目录> [composer|capture]")
		os.Exit(2)
	}
	abs, _ := filepath.Abs(os.Args[1])
	st, err := clip.Open(abs)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer st.Close()

	which := "all"
	if len(os.Args) >= 3 {
		which = os.Args[2]
	}
	if which == "all" || which == "composer" {
		fmt.Printf("draftAt=%s\n", st.Meta("composer.draftAt"))
		fmt.Printf("draft=<%s>\n", st.Meta("composer.draft"))
	}
	if which == "all" || which == "capture" {
		fmt.Printf("captureDraftAt=%s\n", st.Meta("capture.draftAt"))
		fmt.Printf("captureDraft=<%s>\n", st.Meta("capture.draft"))
	}
}
