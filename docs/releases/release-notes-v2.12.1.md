# ClipNest 2.12.1

一句话：**换了内核，观感没变，内存少了一个数量级。**

## 换了什么

桌面壳从 Wails v3 换成了 [MyGo](https://mygo.egoist.dev)。窗口、托盘、全局热键、页面间通信
全部由 MyGo 提供，界面还是原来那套 React + WebView2 自绘，**按钮位置、主题、快捷键一个都没动**。
业务层（`internal/` 里 13000 行）一行没改——它本来就不认识宿主。

顺手把用不上的构建脚手架删干净：iOS/Android/macOS 的壳、Wails 的 `Taskfile`、`wails3` CLI 依赖
全部移除。现在打包是 `go-winres`（Windows 资源）+ NSIS（安装包），一条命令：

```powershell
pwsh -File scripts\package-mygo.ps1 -Version 2.12.1
```

## 内存：753MB → 120–180MB

真正省内存的不是换框架本身，是**弹窗按需销毁**：快速粘贴、记一笔、截图这三个窗口在失焦
1.5 秒后直接销毁，下次按热键再建。只有主窗时进程 52MB。

| | 2.11（Wails） | 2.12.1（MyGo） |
| --- | --- | --- |
| 常驻内存（主窗 + 三个弹窗都开过） | 753MB / 11 进程 | 120–180MB |
| 只有主窗 | — | 52MB |
| 快速粘贴：热键 → 可见 | 10–17ms | 10–25ms |
| 截图：热键 → 遮罩铺满 | 183ms | 118–138ms |

代价是弹窗第一次出现会慢 10–20ms（要重建窗口）。日常手感差别不大，截图那条反而更快了。

## 两个修

- **设置 → PP-OCR 资源一栏报 `TypeError: (intermediate value) is not iterable`**。
  换壳后 Go 的绑定只接受 `(T, error)`，几个多返回值方法在 Go 侧换成了结构体，前端却还按数组解构。
  涉及 `CaptureSize` / `ShotBounds` / `OcrEngineStatus` / `Match` / `Render` 五个方法，已修。
  受影响的用户表现是：这一栏看不到状态、下载按钮点了没反应。
- **从托盘或第二次双击图标唤回主窗时，窗口不一定到前台**。MyGo 的 `Focus()` 只是"请求"焦点，
  前台被别的程序占着时会被 Windows 丢掉；现在补一次 Win32 抢占。

## 已知问题

- 双击主窗标题栏所在的拖动区，仍然不会最大化（和以前一样）——这是有意的。
- 三个弹窗的窗口标题在页面上是同一个（共用 SPA），我们在建窗与每次显示后重新钉一次；
  极端情况下（页面加载很慢）任务栏里可能短暂显示成"ClipNest · 剪贴板"。
- 迁移的完整验收（15 条）、6 处 Wails/MyGo 语义差异、以及还没验的三项（更新检查、WebDAV、
  OCR 实引擎）都写在 `docs/design/MYGO-GATE2-REPORT.md`。

## 升级方式

直接装 `clipnest-2.12.1-amd64-setup.exe`（右键以管理员身份运行）。剪贴板历史与设置都在
`%APPDATA%\ClipNest`，**同一个库、表结构没动**，装完直接看到原来的条目。
