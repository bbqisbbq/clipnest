package main

import (
	"os"
	"time"

	"github.com/egoist/mygo"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// debugEval 是换壳期间的临时调试口：设了 CLIPNEST_EVAL_JS 就在主窗里跑一段 JS，
// 把结果写进应用日志（页面里的报错、DOM 现状都能这样捞出来）。
func debugEval(win *mygo.Window, dataDir string) {
	code := os.Getenv("CLIPNEST_EVAL_JS")
	if code == "" || win == nil {
		return
	}
	delay := 3 * time.Second
	go func() {
		time.Sleep(delay)
		page := win.Page()
		if page == nil {
			clip.Logf(dataDir, "debug: 主窗没有 Page（原生 UI 窗口？）")
			return
		}
		v, err := page.Eval(code)
		if err != nil {
			clip.Logf(dataDir, "debug eval 失败: %v", err)
			return
		}
		clip.Logf(dataDir, "debug eval => %v", v)
	}()
}
