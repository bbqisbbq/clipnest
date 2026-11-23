// 给沙箱数据目录塞几条带标签的条目，供「打 # 弹标签候选」的 e2e 用。
//
//	go run ./scratch/seedtags "C:\path\to\sandbox\ClipNest"
package main

import (
	"fmt"
	"os"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/seedtags <数据目录>")
		os.Exit(1)
	}
	st, err := clip.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer st.Close()

	// 注意：手写条目的 tags 列是**从正文的 `#tag` 推导**的（Store.SaveEntry：
	// `in.Tags = ExtractTags(in.Content)`），传进来的 Tags 会被丢掉 —— 所以标签
	// 必须写进正文，这也正是用户实际输入的方式。
	seeds := []struct {
		text string
		tags []string
	}{
		{"#work e2e 关于工作", []string{"work"}},
		{"#writing e2e 关于写作", []string{"writing"}},
		{"#work #writing e2e 两个标签", []string{"work", "writing"}},
		{"#reading e2e 关于读书", []string{"reading"}},
	}
	for _, s := range seeds {
		if _, err := st.SaveEntry(clip.Entry{
			Content: s.text,
			Kind:    clip.KindText,
			Source:  clip.SourceManual,
			Tags:    s.tags,
		}); err != nil {
			panic(err)
		}
	}
	fmt.Printf("seeded %d entries; tags now: %v\n", len(seeds), st.EntryTags())
}
