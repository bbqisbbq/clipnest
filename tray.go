package main

import (
	"github.com/egoist/mygo"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// buildTray builds the tray icon and its menu.
//
// 菜单只放"托盘上干、在主窗口里干不方便"的动作，一共三条思路：
//  1. 快捷动作（快速粘贴、截图、暂停收集）——不用切窗口；
//  2. 常用开关/入口（开机自启、打开设置、打开数据目录）；
//  3. 退出。
//
// 左键点图标 = 显示/聚焦窗口；菜单走右键。所以这里**不设 TrayOptions.Menu**
// （设了之后单击会直接弹菜单，Windows 上尤其如此）。
func (w *wiring) buildTray(preSettings clip.Settings) {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:    appIcon,
		ToolTip: "ClipNest 剪贴板管理器（左键打开）",
	})
	if err != nil {
		clip.Logf(w.dataDir, "托盘图标创建失败: %v", err)
		return
	}
	w.tray = tray
	tray.OnClick(w.showWindow)

	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "快速粘贴（光标处）", Click: func(*mygo.MenuItem, *mygo.Window) {
			if err := w.svc.ShowPicker(); err != nil {
				clip.Logf(w.dataDir, "打开快速粘贴窗失败: %v", err)
			}
		}},
		// 截图热键撞了别的软件时（或者干脆没设），托盘要有入口——否则这个功能等于不存在。
		{Label: "截图（框选）", Click: func(*mygo.MenuItem, *mygo.Window) {
			if err := w.svc.StartShot(); err != nil {
				clip.Logf(w.dataDir, "托盘截图失败: %v", err)
			}
		}},
		{Label: pauseLabel(preSettings.Capture), ID: "pause", Click: func(it *mygo.MenuItem, _ *mygo.Window) {
			st := w.svc.GetSettings()
			updated, err := w.svc.SetCapture(!st.Capture)
			if err != nil {
				clip.Logf(w.dataDir, "托盘切换收集开关失败: %v", err)
				return
			}
			setPauseLabel(it, updated.Capture)
		}},
		mygo.Separator(),
		{Label: "开机自动启动", ID: "autostart", Type: mygo.MenuItemCheckbox,
			Checked: w.svc.IsAutoStartEnabled(),
			Click: func(it *mygo.MenuItem, _ *mygo.Window) {
				enabled := !w.svc.IsAutoStartEnabled()
				if _, err := w.svc.SetAutoStart(enabled); err != nil {
					clip.Logf(w.dataDir, "切换开机启动失败: %v", err)
				}
				it.SetChecked(w.svc.IsAutoStartEnabled())
			}},
		{Label: "打开设置", Click: func(*mygo.MenuItem, *mygo.Window) {
			w.showWindow()
			w.navigate("settings")
		}},
		{Label: "打开数据目录", Click: func(*mygo.MenuItem, *mygo.Window) {
			_ = w.svc.OpenDataDir()
		}},
		mygo.Separator(),
		{Label: "退出 ClipNest", Click: func(*mygo.MenuItem, *mygo.Window) { w.quit() }},
	})
	w.pauseItem = menu.ItemByID("pause")
	w.autoStartItem = menu.ItemByID("autostart")

	tray.OnRightClick(func() {
		// 只在右键时挂菜单，平时让左键留给"开窗口"。
		tray.SetMenu(menu)
		defer tray.SetMenu(nil)
		tray.PopUpMenu()
	})
}

// navigate 让主窗口切到指定视图（前端 App 监听 ui:navigate）。
func (w *wiring) navigate(target string) {
	_ = evNavigate.Broadcast(target)
}

func pauseLabel(capturing bool) string {
	if capturing {
		return "暂停收集"
	}
	return "继续收集"
}

func setPauseLabel(item *mygo.MenuItem, capturing bool) {
	if item == nil {
		return
	}
	item.SetLabel(pauseLabel(capturing))
}
