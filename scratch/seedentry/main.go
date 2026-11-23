// seedentry：往库里塞一条手写条目（e2e 用，内容从文件读，避免命令行引号吃掉换行）。
//
//	go run ./scratch/seedentry <数据目录> <正文文件> [标题]
//
// 不给标题时走后端的自动派生（正文里第一个 `# 一级标题`）。
package main

import (
	"fmt"
	"os"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/seedentry <数据目录> <正文文件> [标题]")
		os.Exit(2)
	}
	blob, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	title := ""
	if len(os.Args) > 3 {
		title = os.Args[3]
	}
	st, err := clip.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer st.Close()

	e, err := st.SaveEntry(clip.Entry{
		Content: string(blob),
		Title:   title,
		Source:  clip.SourceManual,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("saved id=%s title=%q source=%q\n", e.ID, e.Title, e.TitleSource)
}
