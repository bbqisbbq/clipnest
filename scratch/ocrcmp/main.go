// ocrcmp：同一张图，两个引擎各认一遍，把结果并排打出来。
//
//	go run ./scratch/ocrcmp <数据目录> <png> [ppocr 资源目录]
//
// 用途：① 验证设置里的引擎选择真的生效；② 改动后快速看精度有没有退化。
// PP-OCR 的资源目录默认取 <数据目录>/ocr（应用就是这么找的，见 OcrAssetsDir）。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/ocrcmp <数据目录> <png> [ppocr 资源目录]")
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

	// 资源目录：默认就是应用找的位置
	assetsDir := filepath.Join(dir, "ocr")
	if len(os.Args) > 3 {
		assetsDir = os.Args[3]
	}

	e, _, err := st.SaveCapture(clip.Captured{Kind: clip.KindImage, PNG: png, Width: 760, Height: 220, Size: len(png)})
	if err != nil {
		panic(err)
	}
	svc, err := clip.NewClipService(dir, clip.Hooks{})
	if err != nil {
		panic(err)
	}
	defer svc.Shutdown()

	run := func(engine string) {
		settings := st.Settings()
		settings.OcrEngine = engine
		if err := st.SaveSettings(settings); err != nil {
			panic(err)
		}
		// 引擎实例是按设置懒加载的；换引擎要换一个服务实例，否则会命中旧缓存。
		s2, err := clip.NewClipService(dir, clip.Hooks{})
		if err != nil {
			panic(err)
		}
		defer s2.Shutdown()
		text, err := s2.OcrEntry(e.ID, true)
		fmt.Printf("\n=== %s ===\n", engine)
		if err != nil {
			fmt.Printf("失败: %v\n", err)
			return
		}
		lines := strings.Split(text, "\n")
		for _, l := range lines {
			fmt.Println("  " + l)
		}
		fmt.Printf("  （%d 字，%d 行）\n", len([]rune(text)), len(lines))
	}

	chosen, ready, reason := svc.OcrEngineStatus()
	fmt.Printf("当前设置: %s；ppocr 资源目录 %s（ready=%v %s）\n", chosen, assetsDir, ready, reason)
	run(clip.OcrEngineSystem)
	// 把资源目录指到应用期望的位置（复制一份成本太高，这里用联接/junction 更省事）
	if fi, err := os.Stat(assetsDir); err != nil || !fi.IsDir() {
		fmt.Printf("\n跳过 ppocr：%s 不是目录（先把 det/rec/dll/字典放进去）\n", assetsDir)
		return
	}
	run(clip.OcrEnginePPOCR)

	// 收尾：把设置改回默认，别把沙箱留在 ppocr 上
	settings := st.Settings()
	settings.OcrEngine = clip.OcrEngineSystem
	_ = st.SaveSettings(settings)
}
