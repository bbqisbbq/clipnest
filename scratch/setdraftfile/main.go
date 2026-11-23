// 从文件读草稿写进库（e2e 用）。
//
// 为什么不用 scratch/setdraft：它把草稿当命令行参数传，Windows 的引号规则会把多行
// 参数里的换行吃掉（实测三段草稿写进库变成了一段 74 字的单行），而"多段"正是隐私
// 模式要验的前提。
//
//	go run ./scratch/setdraftfile <数据目录> <草稿文件>
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/setdraftfile <数据目录> <草稿文件>")
		os.Exit(1)
	}
	blob, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	text := string(blob)
	st, err := clip.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer st.Close()
	if err := st.SetMeta("composer.draft", text); err != nil {
		panic(err)
	}
	if err := st.SetMeta("composer.draftAt", time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")); err != nil {
		panic(err)
	}
	fmt.Printf("draft=%q\n", st.Meta("composer.draft"))
}
