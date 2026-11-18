// Command memoseed writes a few demo memos into the ClipNest database.
//
//	$env:APPDATA='<sandbox>'; go run ./scratch/memoseed
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	dir := filepath.Join(os.Getenv("APPDATA"), "ClipNest")
	store, err := clip.Open(dir)
	if err != nil {
		fmt.Printf("open store: %v\n", err)
		return
	}
	defer store.Close()

	seeds := []clip.Memo{
		{Content: "会议纪要：本周先把同步的设备面板收尾，下周开始做备忘模块。 #工作 #会议"},
		{Content: "读到一句话：「工具的价值不在于功能多，而在于让你少做一次重复的选择。」 #读书"},
		{Content: "TODO\n- 给备忘加标签筛选\n- 时间线按天分组\n- 快捷键浮窗 #工作 #todo", Pinned: true},
		{Content: "灵感：把「复制过的东西」和「自己写的东西」放在同一个内容池里，只是形态不同。 #灵感"},
	}
	for _, m := range seeds {
		saved, err := store.SaveMemo(m)
		if err != nil {
			fmt.Printf("save memo: %v\n", err)
			continue
		}
		fmt.Printf("memo %s tags=%v\n", saved.ID[:8], saved.Tags)
	}
}
