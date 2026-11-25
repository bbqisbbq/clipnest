// 探针：MyGo 原生窗口（ui 包自绘）能不能做"真半透明圆角卡片"——也就是不用 DWM 磨砂就
// 让桌面透出来。这是"记一笔弹窗改用原生"是否成立的唯一技术前提，用像素说话。
//
//	go run ./scratch/mygo-native-popup            # 弹一个 460x200 的原生窗口，8 秒后自己退出
//
// 判定：窗口背后摆一块已知颜色的窗口，截图后看卡片中心的像素有没有朝那个颜色移动。
package main

import (
	"log"
	"os"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var text = "随手记点什么… 中文测试"

func view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		// 卡片：半透明底 + 圆角。0.72 的 alpha 若真的与桌面合成，白底上应量到 ~86 而不是 ~20。
		ui.Box(c).Fill().Radius(12).Background(ui.RGBA(20, 22, 28, 0.72)).Padding(14).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "记一笔 · 原生窗口").FontSize(13).TextColor(t.Text)
				ui.Text(c, "Enter 保存 · Esc 关闭").FontSize(12).TextColor(t.TextMuted)
			})
			ui.TextArea(c, &text).Height(64)
			ui.Row(c).Gap(8).Children(func() {
				ui.PrimaryButton(c.Key("save"), "保存")
				ui.Button(c.Key("close"), "取消").Clicked()
			})
		})
	})
}

func main() {
	mygo.App.SetName("clipnest-native-probe")
	mygo.App.WhenReady(func() {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:           "clipnest native popup probe",
			Width:           460,
			Height:          200,
			Frameless:       true,
			Transparent:     true, // 关键：窗口本身要允许透
			AlwaysOnTop:     true,
			Panel:           true,
			Content:         ui.View(view),
			BackgroundColor: "transparent",
		})
		w.SetPosition(700, 400)
		w.Show()
		if os.Getenv("KEEP") == "" {
			time.AfterFunc(9*time.Second, func() { mygo.App.Quit() })
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

