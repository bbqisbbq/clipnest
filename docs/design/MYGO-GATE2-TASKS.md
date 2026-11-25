# Gate 2 任务清单：把宿主从 Wails 换成 MyGo（UI 一行不改）

> 分支 `mygo-shell` · 依据 `MYGO-MIGRATION.md` §2/§3/§4
> 目标：**行为完全等价、可二分、可回滚**。这一步不产生用户可见收益，是投资。

## 进度

- [x] **Step 0：工具链升到 Go 1.27.3 / toolchain go1.27.4**（MyGo v0.10.0 要求）
      —— `go build` 65s 通过，`go test ./internal/...`、`go vet`、`check-platforms.ps1` 全绿，
      证明"升 Go 版本"和"换框架"可以分开做、各自可回退。
- [~] Step 1：宿主层换 MyGo —— **壳已跑起来**（能启动、四窗能建、热键能响、托盘在），
      剩下的页面内容要等 Step 2（前端还在调 Wails 运行时，页面报 `e.map is not a function`）
- [ ] Step 2：前端接线（事件 + 窗口控制 + bindings）
- [ ] Step 3：验收清单逐条过
- [ ] Step 4：发布链保持不动，只把 `go build` 当构建工具

## Step 1：宿主层（预计 5–7 天）

按 `MYGO-MIGRATION.md` §3 的映射表逐项换。落地顺序建议：

1. **App 骨架**：`mygo.App`（`SetName` / `OnWindowAllClosed` / `OnBeforeQuit` / `WhenReady`）
   + `RequestSingleInstanceLock` + `OnSecondInstance`（两次启动 → 唤回主窗）。
2. **主窗**：`mygo.NewWindow`（`Frameless`、`Hidden`、`MinWidth`…），
   `OnClose` → `PreventDefault` + `Hide`，`OnHide` → 广播 `app:hidden`。
3. **三个弹窗**：沿用刚落地的 `ensure*` / `retire` 结构（Gate 2.5 的成果直接搬），
   `HideOnFocusLost` → `win.OnBlur(win.Hide)`，`OnClose` → 允许销毁 vs 降级隐藏。
4. **托盘**：`mygo.NewTray` + `MenuItem{Type: MenuItemCheckbox}`（暂停收集/开机自启的勾选态）。
5. **热键**：`mygo.GlobalShortcut.Register/Unregister`，撞车预检继续用 `clip.HotkeyTaken`。
6. **服务绑定**：`mygo.Bind(svc)` 取代 `application.NewService`；`clip.Hooks` 不动。
7. **事件**：`mygo.NewEvent[T]("clip:new")` 等，**名字与字段零变更**；`app.Event.Emit` → `.Broadcast(v)`。
8. **静态资源**：`mygo.SetFrontend(assets)`；`/shot/current` 改成 `mygo.Protocol.Handle`
   内部委托 `mygo.FileServer(assets)`（保留 `shotAwareAssets` 的语义）。
9. **原生 HWND 那段**（`PlaceOverlayWindow` / `ActivateWindow` / `SetWindowChrome` /
   `SetWindowBackdrop`）：`win.NativeWindow()` → `win.NativeHandle()`，
   `application.InvokeSync` → `mygo.RunOnMain`。**一个字符的语义都没变，只是换个门牌。**

## Step 2：前端接线（预计 2–3 天）

- `lib/window.ts`：`@wailsio/runtime` 的 `Window.*` → `window.mygo.window`。
- `lib/privacyMode.ts`：`Events.On` → `mygo-runtime` 的事件订阅。
- `bindings/`：`wails3 generate bindings` → `mygo generate` 生成的 TS 客户端；
  `lib/api.ts` 与 `hooks/*` 的调用点按新签名改一遍（方法名不变）。
- CSS：`--wails-draggable: drag` → `--app-region: drag`（`TitleBar.tsx` 一处）。
- **不许**在这一步改组件、改样式、改文案。

## Step 3：验收清单（合并条件，来自 `MYGO-MIGRATION.md` §4）

- [ ] 主窗：`--show` / `--background` / 设置项三种启动显隐、自绘标题栏可拖可双击、关窗不退
- [ ] picker：`Ctrl+Shift+V` 唤起、贴插入符、失焦即隐、键鼠选、回车直粘、拼音搜索
- [ ] capture：`Ctrl+Shift+N`、可拖可拉、尺寸记忆、材质开关、Esc
- [ ] shot：`Ctrl+Shift+D`（本机被占则该用 `Ctrl+Shift+F11`）、框选、Esc、存条目 + 进剪贴板
- [ ] 托盘：左键、6 个菜单项、勾选态随设置、退出干净
- [ ] 三个热键：注册 / 冲突报错 / 设置页保存回滚
- [ ] 单实例二次启动唤回
- [ ] 开机自启 `ReconcileAutoStart`
- [ ] 事件：`clip:new` 实时插入、`ocr:progress`、`ui:navigate`
- [ ] 更新：检查 / 下载 / ed25519 验签 / 静默装（打包链仍走 `docs/RELEASE.md`）
- [ ] WebDAV 同步一轮、草稿 flush 不丢字
- [ ] OCR 识别一轮
- [ ] `go test ./internal/...`、`check-platforms.ps1`、`npx tsc --noEmit`、`npx vite build`、
      `check-css.ps1`、`scratch/e2e-*.ps1`（前端步骤按新 IPC 改）

## 已知会在这一步撞上的坑（来自 Gate 0/1 实测）

| 坑 | 影响 | 对策 |
| --- | --- | --- |
| `DisableMove:true` 让 `SetPosition/SetBounds` 失效 | 弹窗全部跑到 (0,0) | 摆窗的窗口一律不设它 |
| `mygo.Clipboard.*` 会阻塞 UI 线程、被占用时失败 | 直粘/截图入库卡死 | 剪贴板继续用 `internal/clip` 自写实现 |
| 非输入事件改状态不重画 | 事件驱动的界面停在旧帧 | 改完 `win.Invalidate()` |
| 颜色 alpha 是 0..1 float | 半透明画成全黑/全不透明 | `ui.RGBA(r,g,b,0.45)` 或 `color.Alpha()` |
| `Box` 子元素顺序排版 / `DrawOver` 独立图层 | 覆盖层画不出来 | 一个 `Draw` 里画完 |

## 回滚

- 合并到 `main` 前保留 `v2.11-wails-final` tag；`mygo-shell` 分支本身就是完整的第二实现。
- `docs/RELEASE.md` 的打包/签名/发版链**这一步不动**，出问题只换回 exe 即可。

