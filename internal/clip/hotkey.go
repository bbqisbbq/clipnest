package clip

// 加速键的公共入口：Windows 上有真正的"撞车预检"（见 hotkey_windows.go），
// 其它平台恒为 false——热键注册本身在这些平台上也只是桩实现。
// 这个函数是**导出给 main 包用的**（wire.go 在注册前先问一句），不是给前端绑定的 API。

// HotkeyTaken reports whether the accelerator is already owned by another program.
//
// 拿它把"设置页说保存成功、按键却毫无反应"挡在注册之前：Wails 的 GlobalShortcut
// 在 app.Run() 之前只是排队，真正的 RegisterHotKey 在启动后 flush，失败只走错误处理器，
// 调用方拿到的是 nil（见 hotkey_windows.go 的说明）。
func HotkeyTaken(accel string) bool { return hotkeyTaken(accel) }
