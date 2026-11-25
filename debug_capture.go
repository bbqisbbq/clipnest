package main

import (
	"os"
	"time"

	"github.com/egoist/mygo"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// debugCapturePage 是换壳期间的临时自检：设了 CLIPNEST_CAPTURE_PAGE=<png 路径>
// 就等窗口画好之后用 MyGo 自己的 CapturePage 抓一帧下来。
//
// 为什么不用外部截屏：无边框窗口抢不到前台时，CopyFromScreen 抓到的是底下别的
// 程序，看不出页面到底渲染成什么样。
func debugCapturePage(win *mygo.Window, dataDir string) {
	path := os.Getenv("CLIPNEST_CAPTURE_PAGE")
	if path == "" {
		return
	}
	go func() {
		time.Sleep(2500 * time.Millisecond)
		png, err := win.CapturePage()
		if err != nil {
			clip.Logf(dataDir, "debug: CapturePage 失败: %v", err)
			return
		}
		if err := os.WriteFile(path, png, 0o644); err != nil {
			clip.Logf(dataDir, "debug: 写 %s 失败: %v", path, err)
			return
		}
		clip.Logf(dataDir, "debug: 已抓帧 %s (%d 字节)", path, len(png))
	}()
}
