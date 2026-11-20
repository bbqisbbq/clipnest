package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// buildTray builds the tray icon and its menu.
//
// 菜单只放"托盘上干、在主窗口里干不方便"的动作，一共三条思路：
//   1. 快捷动作（快速粘贴、暂停收集）——不用切窗口；
//   2. 常用开关/入口（开机自启、打开设置、打开数据目录）；
//   3. 退出。
// 有主窗口就能做的事（显示窗口、检查更新、清空历史）不在这里重复：
// 左键点图标就是"显示/聚焦窗口"，其余都在设置页里，并且带确认。
//
// 菜单项里的 pauseItem / autoStartItem 会被别处改标签或勾选状态（暂停收集、
// 设置页改开机自启），所以存回 wiring 上，而不是留成 buildTray 的局部变量。
func (w *wiring) buildTray(preSettings clip.Settings) {
	tray := w.app.SystemTray.New()
	tray.SetIcon(appIcon)
	// 菜单里的"显示管理窗口"已经去掉了，提示里补一句左键的作用，
	// 否则新用户不知道点图标能唤回窗口。
	tray.SetTooltip("ClipNest 剪贴板管理器（左键打开）")
	tray.OnClick(w.showWindow)

	menu := application.NewMenu()
	menu.Add("快速粘贴（光标处）").OnClick(func(_ *application.Context) {
		if err := w.svc.ShowPicker(); err != nil {
			clip.Logf(w.dataDir, "打开快速粘贴窗失败: %v", err)
		}
	})

	w.pauseItem = menu.Add(pauseLabel(preSettings.Capture))
	w.pauseItem.OnClick(func(_ *application.Context) {
		st := w.svc.GetSettings()
		updated, err := w.svc.SetCapture(!st.Capture)
		if err != nil {
			// 以前这里静默 return：点了菜单毫无反应，日志里也没有一条。
			clip.Logf(w.dataDir, "托盘切换收集开关失败: %v", err)
			return
		}
		setPauseLabel(w.pauseItem, updated.Capture)
	})

	menu.AddSeparator()
	w.autoStartItem = menu.AddCheckbox("开机自动启动", w.svc.IsAutoStartEnabled())
	w.autoStartItem.OnClick(func(_ *application.Context) {
		enabled := !w.svc.IsAutoStartEnabled()
		if _, err := w.svc.SetAutoStart(enabled); err != nil {
			clip.Logf(w.dataDir, "切换开机启动失败: %v", err)
		}
		w.autoStartItem.SetChecked(w.svc.IsAutoStartEnabled())
	})
	menu.Add("打开设置").OnClick(func(_ *application.Context) {
		w.showWindow()
		w.navigate("settings")
	})
	menu.Add("打开数据目录").OnClick(func(_ *application.Context) {
		_ = w.svc.OpenDataDir()
	})

	menu.AddSeparator()
	menu.Add("退出 ClipNest").OnClick(func(_ *application.Context) { w.quit() })
	tray.SetMenu(menu)
}

// navigate 让主窗口切到指定视图（前端 App 监听 ui:navigate）。
//
// 事件在窗口刚 Show 出来时发，前端还没挂监听就会丢——所以窗口本来就是
// 可见的常见情况下它照样送达；窗口是刚唤回来的，前端本来就在跑（只是隐藏），
// 监听一直都在。两种情况都不需要重试。
func (w *wiring) navigate(target string) {
	if w.app != nil {
		w.app.Event.Emit("ui:navigate", target)
	}
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
