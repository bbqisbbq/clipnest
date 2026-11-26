// 打印语义向量索引的状态，并可选地跑一次检索 —— 排障用。
//
//	go run ./scratch/embstate <数据目录>                 # 只看状态
//	go run ./scratch/embstate <数据目录> "云端传不上去"   # 状态 + 混合检索前 5 条
//
// 它回答的是"为什么设置页显示已索引 0 条"这类问题：能看到每个状态各多少条、
// 以及 FTS 与向量各自返回什么。只读打开，不改任何数据。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbqisbbq/clipnest/internal/clip"
	"github.com/bbqisbbq/clipnest/internal/embed"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/embstate <数据目录> [查询]")
		os.Exit(2)
	}
	st, err := clip.Open(os.Args[1])
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer st.Close()

	done, pending, failed := st.VectorStats()
	fmt.Printf("向量状态：done=%d pending=%d failed=%d\n", done, pending, failed)
	fmt.Printf("资源目录：%s\n", st.Dir())
	fmt.Printf("条目总数：%d（活着的 %d）\n", st.CountAllEntries(), st.CountEntries())

	rows := st.LiveVectors()
	fmt.Printf("可检索向量：%d 条\n", len(rows))

	// 图片条目的"有没有可编码内容"：这是判断"向量库被噪声占满"的关键一眼
	var imgs, imgWithOcr int
	for _, e := range st.AllEntries() {
		if e.Kind != clip.KindImage {
			continue
		}
		imgs++
		if strings.TrimSpace(e.OcrText) != "" {
			imgWithOcr++
		}
	}
	fmt.Printf("图片条目：%d 条，其中 OCR 出了文字 %d 条（其余正文只有 memoimg 令牌 → 应记 skipped）\n",
		imgs, imgWithOcr)
	if len(os.Args) >= 3 && os.Args[2] == "-imgs" {
		for _, e := range st.AllEntries() {
			if e.Kind == clip.KindImage && strings.TrimSpace(e.OcrText) == "" {
				state := "（无行）"
				if row, ok := st.EntryVector(e.ID); ok {
					state = row.State
				}
				fmt.Printf("  [%s] state=%s content=%q ocr_state=%q\n",
					e.ID[:8], state, oneLine(e.Content, 60), e.OcrState)
			}
		}
		return
	}

	if len(os.Args) < 3 {
		return
	}
	q := os.Args[2]
	fmt.Printf("\n查询：%s\n", q)

	fts := st.FTSIDs(q, 5, 0)
	fmt.Printf("-- FTS 字面命中 %d 条 --\n", len(fts))
	for i, id := range fts {
		if e, ok := st.GetEntry(id); ok {
			fmt.Printf("  %d. %s\n", i+1, show(e))
		}
	}

	// 语义那一路：直接在数据目录上建引擎（和产品里同一个包、同一份资源）
	eng := embed.New(filepath.Join(st.Dir(), "embed"))
	defer eng.Close()
	qv, err := eng.Encode(q)
	if err != nil {
		fmt.Printf("-- 语义：引擎不可用（%v）--\n", err)
		return
	}
	sem := st.NearestIDs(qv, 5)
	fmt.Printf("-- 语义近邻 %d 条 --\n", len(sem))
	for i, id := range sem {
		if e, ok := st.GetEntry(id); ok {
			fmt.Printf("  %d. %s\n", i+1, show(e))
		}
	}
}

// show 把一条条目打成一行：图片条目显示它的 OCR 文字（那才是它被编码的内容），
// 否则只看正文会以为"结果全是 memoimg 噪声"。
func show(e clip.Entry) string {
	c := oneLine(e.Content, 60)
	if strings.TrimSpace(e.OcrText) != "" {
		c = "[图] " + oneLine(e.OcrText, 60)
	}
	return c
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

