// ocrdemo：把一张 PNG 当成"刚复制的图片"存进库，跑一次真实 OCR，再验证能不能搜到。
//
//	go run ./scratch/ocrdemo <数据目录> <png 路径>
//
// 走的是应用里同一条路：SaveCapture（采集）→ OcrEntry（识别）→ FTS 搜索。
// 不启动 GUI，所以可以在跑着安装版的时候用（不碰系统剪贴板）。
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/ocrdemo <数据目录> <png>")
		os.Exit(2)
	}
	dir, pngPath := os.Args[1], os.Args[2]
	png, err := os.ReadFile(pngPath)
	if err != nil {
		panic(err)
	}
	st, err := clip.Open(dir)
	if err != nil {
		panic(err)
	}
	defer st.Close()
	e, isNew, err := st.SaveCapture(clip.Captured{
		Kind: clip.KindImage, PNG: png, Width: 760, Height: 220, Size: len(png),
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("入库: id=%s new=%v kind=%s\n", e.ID, isNew, e.Kind)

	svc, err := clip.NewClipService(dir, clip.Hooks{})
	if err != nil {
		panic(err)
	}
	defer svc.Shutdown()
	text, err := svc.OcrEntry(e.ID, true)
	if err != nil {
		fmt.Printf("OCR 失败: %v\n", err)
		os.Exit(1)
	}
	entry, _ := st.GetEntry(e.ID)
	fmt.Printf("状态: %s  字数: %d\n", entry.OcrState, len([]rune(text)))
	short := text
	if len([]rune(short)) > 160 {
		short = string([]rune(short)[:160]) + "…"
	}
	fmt.Printf("--- 识别文字 ---\n%s\n", short)

	for _, kw := range []string{"坚果云", "447043705", "同步"} {
		hits := st.ListEntries(clip.EntryQuery{Text: kw})
		found := false
		for _, h := range hits {
			if h.ID == e.ID {
				found = true
			}
		}
		fmt.Printf("搜索 %-10s → 命中 %d 条（含本条: %v）\n", kw, len(hits), found)
	}
	_ = strings.TrimSpace
}
