package main

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// buildTray builds the tray icon and its menu.
//
// 菜单项里的 pauseItem / autoStartItem 会被别处改标签或勾选状态（暂停收集、
// 设置页改开机自启），所以存回 wiring 上，而不是留成 buildTray 的局部变量。
func (w *wiring) buildTray(preSettings clip.Settings) {
	tray := w.app.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("ClipNest 剪贴板管理器")
	tray.OnClick(w.showWindow)

	menu := application.NewMenu()
	menu.Add("显示管理窗口").OnClick(func(_ *application.Context) { w.showWindow() })
	menu.Add("快速粘贴（光标处）").OnClick(func(_ *application.Context) {
		if err := w.svc.ShowPicker(); err != nil {
			log.Printf("打开快速粘贴窗失败: %v", err)
		}
	})

	w.pauseItem = menu.Add(pauseLabel(preSettings.Capture))
	w.pauseItem.OnClick(func(_ *application.Context) {
		st := w.svc.GetSettings()
		updated, err := w.svc.SetCapture(!st.Capture)
		if err == nil {
			setPauseLabel(w.pauseItem, updated.Capture)
		}
	})

	// 记一笔：把剪贴板当前文本直接存成一条手写条目，不弹窗、不抢焦点
	menu.Add("记一笔（剪贴板内容）").OnClick(func(_ *application.Context) {
		go func() {
			if _, err := w.svc.QuickMemo(); err != nil {
				log.Printf("记一笔失败: %v", err)
			}
		}()
	})

	w.autoStartItem = menu.AddCheckbox("开机自动启动", w.svc.IsAutoStartEnabled())
	w.autoStartItem.OnClick(func(_ *application.Context) {
		enabled := !w.svc.IsAutoStartEnabled()
		if _, err := w.svc.SetAutoStart(enabled); err != nil {
			log.Printf("切换开机启动失败: %v", err)
		}
		w.autoStartItem.SetChecked(w.svc.IsAutoStartEnabled())
	})
	menu.AddSeparator()
	checkUpdateItem := menu.Add("检查更新…")
	checkUpdateItem.OnClick(func(_ *application.Context) {
		go func() {
			info := w.svc.CheckUpdate()
			w.showWindow()
			if info.Error != "" {
				log.Printf("检查更新失败: %s", info.Error)
			}
		}()
	})
	menu.Add("清空未收藏的历史").OnClick(func(_ *application.Context) {
		_, _ = w.svc.DeleteAll()
	})
	sub := menu.AddSubmenu("更多")
	sub.Add("打开数据目录").OnClick(func(_ *application.Context) {
		_ = w.svc.OpenDataDir()
	})
	sub.Add("打开设置").OnClick(func(_ *application.Context) {
		w.showWindow()
		w.app.Event.Emit("ui:navigate", "settings")
	})
	menu.AddSeparator()
	menu.Add("退出 ClipNest").OnClick(func(_ *application.Context) { w.quit() })
	tray.SetMenu(menu)
}

func pauseLabel(capturing bool) string {
	if capturing {
		return "暂停收集"
	}
	return "继续收集"
}

func setPauseLabel(item *application.MenuItem, capturing bool) {
	if item == nil {
		return
	}
	item.SetLabel(pauseLabel(capturing))
}
