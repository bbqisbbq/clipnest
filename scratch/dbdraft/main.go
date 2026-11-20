// 打印一份库副本里的快捷输入草稿（meta composer.draft / composer.draftAt）。
//
// 用法：go run ./scratch/dbdraft <db目录>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	abs, _ := filepath.Abs(os.Args[1])
	st, err := clip.Open(abs)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer st.Close()
	draft := st.Meta("composer.draft")
	at := st.Meta("composer.draftAt")
	fmt.Printf("draftAt=%s\n", at)
	fmt.Printf("draft=<%s>\n", draft)
}
