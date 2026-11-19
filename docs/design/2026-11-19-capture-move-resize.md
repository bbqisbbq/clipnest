# 「记一笔」浮层：可拖动 / 可改大小（2026-11-19）

> 起因：用户反馈「弹出的快捷输入框要可以拖动位置 / 拖动改变大小」。
> 对象只指 `Ctrl+Shift+N` 的**快速输入窗**（`?mode=capture`），快速粘贴列表（picker）不动。

## 1. 之前为什么不能拖也不能拉

- **不能拖**：无边框窗，标题栏是 Self-drawn 的，页面里没有任何 `--wails-draggable: drag` 区域，
  系统不知道哪块该当标题条。
- **不能拉**：窗口选项写着 `DisableResize: true`。这一条不是「保险起见」——Wails v3 在
  Windows 上跑 frameless 时，`WM_NCCALCSIZE` 把标准边框整个让给了客户区
  （见上游 `webview_window_windows.go` 里关于 `WS_THICKFRAME` 的注释），
  **「OS 自己会给拉伸边」这个假设不成立**；拉伸全靠前端运行时：
  `wails:runtime:ready` 时 Go 侧调 `setResizable(!DisableResize)`，前端 `drag.ts`
  才在窗口四周 5px 内挂 `resizeEdge`，按下后发 `wails:resize:<edge>` 回到原生 `WM_NCLBUTTONDOWN`。

结论：把 `DisableResize` 去掉就够了，**前端不需要自己算鼠标位移去改大小**。

## 2. 改了什么

| 位置 | 改动 |
| --- | --- |
| `main.go` capture 窗 | 去掉 `DisableResize`；补 `MinWidth/MinHeight`；宽高改用 `svc.CaptureSize()` |
| `main.go` `showCaptureAt` | 夹屏幕边距时用窗口**当前**尺寸（不再用 620×220 常量），日志带上 `size=` |
| `main.go`（窗口事件） | `WindowEndResize` → `svc.SaveCaptureSize(w,h)`（隐藏中不记，见 §3） |
| `internal/clip/service_capture.go` | `CaptureMin/MaxWidth/Height`、`CaptureSize()` / `SaveCaptureSize()`、`capture.size` meta |
| `frontend/src/Capture.tsx` | 抬头 `.capture-head` 加 `style={drag}`，关闭按钮 `style={noDrag}` |

## 3. 尺寸记忆的四个约束

1. **只记尺寸，不记位置**。位置跟着光标是这个窗口的立身之本；用户明确选了「只记大小」。
2. **存在 meta 表**（`capture.size` = `WxH`），不进 `settings.json`：设置会随 WebDAV 包同步，
   把一台机器的窗口尺寸推给另一台毫无意义。
3. **小于下限的值直接丢弃**（`w < 420 || h < 160`），不是夹到下限：隐藏/刚创建时读到的
   瞬时小矩形若被夹成 420×160，就会把默认尺寸改成最小值——真机踩过这个坑。
4. **拖位置也会触发 `WindowEndResize`**，所以写入前比一下旧值，尺寸没变就不写库、不刷日志。

上限（1600×1200）只用于「记住的尺寸来自更大显示器」这一种情况，恢复时收一收。

## 4. 验证

`scratch/e2e-capture-resize.ps1`（真鼠标：`SetCursorPos` + `mouse_event`，不是发窗口消息）：

```
pwsh -STA -NoProfile -File scratch\e2e-capture-resize.ps1
```

它做八件断言：默认 620×220 → 拖标题条窗口跟着走 → 拖右边缘变宽 → 拖下边缘变高 →
Esc 关闭 → 重启后尺寸还在 → 位置仍在光标处 → 硬缩到 420×160 停住。
脚本开头会删掉 `capture.size`（从默认起跑），结尾再删一次并把应用留在默认状态。
截图：`scratch/shot-capture-before.png` / `-resized.png` / `-after-restart.png` / `-min.png`。
