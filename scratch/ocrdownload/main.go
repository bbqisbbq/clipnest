// ocrdownload：验证「一键下载 PP-OCR 资源」这条链路（下载 → 校验 → 解压 → 能识别）。
//
//	go run ./scratch/ocrdownload <数据目录>
//
// 走的是设置页那个按钮背后的同一条路径：ClipService.DownloadOCRAssets()。
// 跑之前记得让 Go 能上网（这台机器上要 HTTPS_PROXY，见 ARCHITECTURE 的备注）。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bbqisbbq/clipnest/internal/clip"
	"github.com/bbqisbbq/clipnest/internal/ppocr"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/ocrdownload <数据目录>")
		os.Exit(2)
	}
	dir := os.Args[1]
	svc, err := clip.NewClipService(dir, clip.Hooks{})
	if err != nil {
		panic(err)
	}
	defer svc.Shutdown()

	assets := svc.OcrAssetsDir()
	fmt.Printf("资源目录: %s\n", assets)
	if err := ppocr.Ready(assets); err != nil {
		fmt.Printf("下载前: %v\n", err)
	} else {
		fmt.Printf("下载前: 已就绪（跳过下载）\n")
		return
	}

	msg, err := svc.DownloadOCRAssets()
	if err != nil {
		fmt.Printf("下载失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("下载结果: %s\n", msg)

	var total int64
	for _, name := range []string{ppocr.FileLibrary, ppocr.FileDet, ppocr.FileRec, ppocr.FileKeys} {
		st, err := os.Stat(filepath.Join(assets, name))
		if err != nil {
			fmt.Printf("  %-18s 缺失\n", name)
			continue
		}
		total += st.Size()
		fmt.Printf("  %-18s %d 字节\n", name, st.Size())
	}
	fmt.Printf("合计 %.1f MB\n", float64(total)/(1<<20))

	if err := ppocr.Ready(assets); err != nil {
		fmt.Printf("Ready 仍失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Ready: 通过")

	// 真识别一遍：证明装下来的东西真的能用
	eng := ppocr.New(assets, ppocr.DefaultOptions())
	defer eng.Close()
	if err := eng.Init(); err != nil {
		fmt.Printf("引擎加载失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("引擎加载: 通过（模型与 onnxruntime 都能起来）")
}
