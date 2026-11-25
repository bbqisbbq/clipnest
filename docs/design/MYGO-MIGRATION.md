# 向 MyGo 切换：推荐路线（决策版）

> 2026-11-25 · 前置阅读：`MYGO-EVAL.md`（可行性/收益）、`MYGO-EXAMPLES.md`（能抄什么）
> 本文只回答一件事：**怎么切，按什么顺序，什么时候该停。**

## 0. 一句话推荐

**四道闸门：先量对照（Gate 0）→ 啃最硬的窗口（Gate 1）→ 一次原子换壳、UI 一行不改（Gate 2）→ 再一窗一窗原生化（Gate 3）。每一道闸门都有明确的 No-Go 判据，任何一道不过就停在 Wails 侧继续减重，等 MyGo 1.0 再看。**

核心取舍：

- **换壳是一次性的**（一个进程只能有一个框架持有消息泵/托盘/热键，Wails 与 MyGo 不可能各管一半窗口）；
- **换 UI 是渐进的**（换壳后每个窗口可以独立决定 web 还是原生）；
- **换壳那一步不产生任何用户可见收益**，它是投资；收益从 Gate 3 才开始。所以换壳必须做到"行为完全等价、可二分定位"，绝不能和 UI 改写混在一个提交里。

## 1. 为什么不是"一把梭"，也不是"逐窗渐进"

| 方案 | 问题 |
| --- | --- |
| 一把梭（换壳 + 全部原生重写） | 11.5k 行 UI + 5k 行 CSS + 编辑器语义一起动，回归无法二分定位；两周内不可能绿 |
| 逐窗渐进（先换 picker） | **做不到**：picker 换成 MyGo 就意味着整个 exe 是 MyGo 的；Wails 版 picker 无法与新壳并存 |
| 先换壳、再换窗（推荐） | 换壳阶段只需证明"和以前一模一样"（可验证、可回滚）；之后每窗独立收益、独立回滚 |

**我们有一个别人没有的结构优势**：`internal/clip`（13k 行）**0 处 import 框架**，宿主能力全靠 `clip.Hooks`（Emit/ShowWindow/ShowPickerAt/…）注入。所以换壳只碰 `main.go` + `wire.go` + `tray.go` ≈ 730 行——这不是运气，是当初拆 `wiring` 时就留好的口子。**切换的难度不在业务，在宿主层与前端接线。**

## 2. 四道闸门

### Gate 0 — 对照测量（0.5–1 天，不动主干）

两件事并行做：

1. **Wails 侧减重上限**：在 `scratch/` 复制一份构建，把 `picker/capture/shot` 从"常驻 Hide"改成"空闲 Destroy、唤起重建"，量内存与唤起延迟。目的：知道**不换框架最多能省多少**（当前快照 730 MB WS / 705 MB private，11 进程）。
2. **MyGo picker 原型**：照抄 `examples/launcher`，数据用 mock 的 200 条条目 + 拼音 rank，贴光标定位、回车直粘留空。量"热键 → 可见 → 可输入"耗时与常驻内存。

> **Gate 0 已执行完毕：结论见 `MYGO-GATE0.md` —— 两条判据通过（内存 1/15.7、可输入 2.1×），
> 但"延迟更快"这个理由被实测推翻，主因是内存与依赖面。IME 手感待人工 30 秒验证。**

**Go 判据**（同时满足才进 Gate 1）：
- MyGo 原型常驻内存 ≤ 现在的 1/4；
- "热键 → 可输入" ≤ 现在的 1/2，或 ≤ 120ms（人感阈值）；
- 中文输入法在原生 `TextInput` 上连续输入 20 个词不出怪（组合串、候选、退格、粘贴）。
- Wails 减重后若能压到 ≤ 300 MB **且** picker 延迟无劣化 → **No-Go，暂缓切换**，把这笔钱省下来。

### Gate 1 — 啃最硬的窗口（3–5 天，仍只在 `scratch/`）

**不是 picker，是 shot（框选截图遮罩）。** 理由：它是唯一一个把"全屏物理像素铺开 + 抢前台 + 抓屏 + Esc 收尾 + 焦点交给遮罩 + 存条目并顺手进剪贴板"全踩一遍的窗口，也是我们现在 Wails 里 hack 最多的地方（`wire.go` 里 100ms 轮询等 HWND、`PlaceOverlayWindow`、`FocusOverlayWindow`）。它通了，等于 `NativeHandle` / `RunOnMain` / 多屏 DPI / 面板语义 / 焦点时序全部验过；picker 有 launcher 兜底，风险低得多。

同一轮顺手把三个"待验证"变成结论：
- **OCR**：cgo 路线（自己 `go build`）与 `ffi` + Rust/Zig cdylib 路线各试一个最小样例，选一条；
- **字体**：Inter / Noto Sans SC / Maple Mono NF CN 转 TTF/OTF 后 `ui.RegisterFont` 的排版与体积；
- **托盘与热键**：Windows 上托盘左键、三个热键共存、`RegisterHotKey` 撞车检测（我们已有 `HotkeyTaken`）。

**Go 判据**：shot 遮罩能在 4 个显示器/150% 缩放下精确铺满、Esc 可靠、抓到的帧一致；OCR 至少一条路线能打包成功。

> **Gate 1 已执行完毕：结论见 `MYGO-GATE1.md` —— 整条链路跑通（真鼠标拖框 → 裁图 600×360 → 落盘），
> 热键 → 遮罩 118–138ms（现版 184ms），稳态内存 114MB。同时挖出 6 个框架级坑
> （`DisableMove` 让摆窗失效、`Box` 子元素顺序排版、`DrawOver` 独立图层、颜色 alpha 是 0..1、
> 改状态要显式 `Invalidate`、`mygo.Clipboard` 会阻塞 UI 线程 → 剪贴板继续自写）。
> 多屏混合 DPI 仍待真机验证。**

### Gate 2 — 一次原子换壳，UI 一行不改（8–10 天）

分支 `mygo-shell`。四个窗口**全部先保持 web**（React 原样），只换宿主与接线。判定标准是"用户看不出任何区别"。

必须做的事（映射表见 §3）：
- `main.go` / `wire.go` / `tray.go` 重写为 MyGo（~730 行，语义一一对应）；
- 事件契约**逐字保留**（`clip:new`、`clip:bumped`、`settings:changed`、`picker:opened`、`shot:opened`、`ocr:progress`…），只换 emit 实现；
- 前端 `@wailsio/runtime` 的两处接线（`lib/window.ts` 窗口控制、`lib/privacyMode.ts` 事件）换 `mygo-runtime`，`--wails-draggable: drag` 换 `--app-region: drag`；
- bindings 从 `wails3 generate bindings` 换 `mygo generate` 生成的 TS 客户端（`lib/api.ts`、`hooks/*` 的调用点按新签名调一遍）；
- `/shot/current` 从 AssetHandler 包装换成 `mygo.Protocol.Handle`（内部委托 `mygo.FileServer(assets)`）。

**闸门要求**：§4 验收清单全绿。**不许**在这一步顺便改设计、换组件、调 CSS。

### Gate 3 — 逐窗原生化（按收益排序，一窗一 release）

| 顺序 | 窗口 | 收益 | 风险 | 照抄对象 |
| --- | --- | --- | --- | --- |
| 1 | picker | 热键即时（最大体感收益）、砍 1 个 renderer | 输入法手感 | `examples/launcher` |
| 2 | capture | 材质/拖拽更干净、砍 1 个 renderer | Win10 无 Acrylic 回退 | `examples/vibrancy` |
| 3 | shot | Gate 1 已做完，直接收编 | 低 | Gate 1 原型 |
| 4 | main（左栏 + 时间线列表先行） | 砍最后 3 个 renderer、去掉 WebView2 依赖 | 高（UI 体量） | `examples/gallery` |

**主窗是最后一块，而且只在编辑器外面搬**：编辑区（Vditor）用 `Window.NewWebView` 内嵌，永久保留。这样即使主窗原生化，仍有一个 webview 进程——**接受这一点**，别为"纯原生"去改 Markdown 存储模型。

## 3. Gate 2 的逐条映射

| Wails v3 | MyGo | 备注 |
| --- | --- | --- |
| `application.New(Options{Services, Assets, SingleInstance, …})` | `mygo.App` + `SetName` + `SetFrontend` + `RequestSingleInstanceLock` | 服务改 `mygo.Bind` |
| `application.NewService(svc)` | `mygo.Bind(svc)` | `ClipService` 方法签名基本不动 |
| `application.RegisterEvent[T]("clip:new")` + `app.Event.Emit` | `mygo.NewEvent[T]("clip:new")` + `.Broadcast(v)` | 顶层声明一次；**名字不要改** |
| `WebviewWindowOptions{URL, Frameless, AlwaysOnTop, Hidden, BackgroundTypeTransparent, DisableResize, MinWidth…}` | `mygo.WindowOptions{URL, Frameless, AlwaysOnTop, Hidden, Transparent, CornerRadius, DisableResize, MinWidth…}` | picker/capture 的圆角改用 `CornerRadius`，不再靠网页画。**坑：`DisableMove:true` 会让 `SetPosition`/`SetBounds` 完全失效（v0.10.0 实测，见 `MYGO-GATE0.md` §6），摆窗的窗口一律别设它** |
| `HideOnFocusLost: true` | `win.OnBlur(win.Hide)` | |
| `RegisterHook(events.Common.WindowClosing)` + `e.Cancel()` | `win.OnClose(func(e){ e.PreventDefault(); win.Hide() })` | 配合 `OnBeforeQuit{quitting=true}` |
| `RegisterHook(events.Common.WindowHide)` → emit `app:hidden` | `win.OnHide(func(){ … })` | 草稿 flush 依赖它，别丢 |
| `RegisterHook(events.Windows.WindowEndResize)` | `win.OnResize` / 自绘回调 | 记「记一笔」尺寸 |
| `application.InvokeSync(fn)` | `mygo.RunOnMain(fn)` | 语义一致（等返回） |
| `win.NativeWindow()` → `uintptr` | `win.NativeHandle()` → `uintptr`（Windows = HWND） | `PlaceOverlayWindow`/`SetWindowChrome` 原样搬 |
| `app.GlobalShortcut.Register/Unregister/IsRegistered` | `mygo.GlobalShortcut.*` | 撞车检测仍用我们的 `HotkeyTaken` |
| `app.SystemTray.New()` / `MenuItem.SetChecked/SetLabel` | `mygo.NewTray` / 同名方法 | 托盘菜单的勾选状态靠 `MenuItem.Checked` |
| `SingleInstance{UniqueID, OnSecondInstanceLaunch}` | `RequestSingleInstanceLock` + `App.OnSecondInstance` | `--show` / `--background` 参数照旧 |
| `application.AssetFileServerFS(assets)` | `mygo.SetFrontend(assets)` + `Protocol.Handle` 包装 | `/shot/current` 逻辑不变 |
| `wails3 generate bindings` | `mygo generate`（跑 app 生成 TS） | CI 里要跑一次 |
| `@wailsio/runtime` `Events.On` / `Window.*` | `mygo-runtime` 事件 / `window.mygo.window` | 只改两处文件 |
| CSS `--wails-draggable: drag` | `--app-region: drag` | `TitleBar.tsx` 一处 |

**保持不动的**（框架无关，换壳也不碰）：`internal/**`（含 Win32 剪贴板枚举、`shot_windows.go` 抓屏、`wininput_windows.go` 键盘模拟、同步、OCR 调度、更新验签）、`scripts/e2e-*.ps1` 的**后端**部分。

## 4. 验收清单（Gate 2 合并条件）

用户可见行为，逐条过：

- [ ] 主窗：启动显隐（`--show`/`--background`/设置项）、无边框自绘标题栏可拖可双击最大化、关窗不退
- [ ] picker：`Ctrl+Shift+V` 唤起、贴插入符、失焦即隐、键鼠选、回车直粘原窗口、拼音搜索
- [ ] capture：`Ctrl+Shift+N` 唤起、可拖可拉、尺寸记忆、材质开关、Esc 关
- [ ] shot：`Ctrl+Shift+A` 全屏框选、Esc 取消、存条目并进剪贴板
- [ ] 托盘：左键、菜单 6 项、勾选状态随设置变化、退出干净
- [ ] 三个热键：注册/冲突报错/设置页保存回滚
- [ ] 单实例：二次启动唤回主窗
- [ ] 开机自启：`ReconcileAutoStart` 修路径
- [ ] 事件：`clip:new` 实时插入列表、`ocr:progress` 进度、`ui:navigate` 从托盘跳设置
- [ ] 更新：检查/下载/ed25519 验签/静默装（**打包链先沿用现有 `docs/RELEASE.md`，暂不换 `mygo build`**）
- [ ] WebDAV 同步一轮成功、草稿 flush 不丢字
- [ ] OCR 一轮识别成功
- [ ] 验证命令全绿：`go test ./internal/...`、`scripts/check-platforms.ps1`、`npx tsc --noEmit`、`npx vite build`、`scripts/visual-check/check-css.ps1`、`scratch/e2e-*.ps1`（前端步骤按新 IPC 改后）

## 5. 护栏

- **锁版本**：`require github.com/egoist/mygo v0.10.x`，禁 `@latest`。框架两个月 84 个 release，随手升级等于随机化产品。
- **探针门禁**：新增 `scripts/check-mygo.ps1`（编译探针 → 起窗 → `CapturePage` 抓一帧 → 打印内存与首帧耗时），与 `check-platforms.ps1` 一起进 CI；升级框架必须两道都绿。
- **保留回退线**：合并前打 `v2.11-wails-final` tag，Wails 版主干保留至少两个版本周期；`v3.0.0-mygo` 先发 pre-release 跑一周再转正。
- **契约冻结**：Gate 2 期间前端只改"接线"，不改组件与 CSS；事件名/字段名零变更（这样前端可以独立于后端验证）。
- **工具链**：Go ≥ 1.27（本机经 goproxy.cn 自动拉取可行）；CI 与 README 的构建前置条件、`ARCHITECTURE.md` 的文件职责表与验证命令、`docs/HISTORY.md` 都要同步更新。
- **诚实记账**：Gate 2 结束后在 `HISTORY.md` 写清"这一步没有用户可见收益，收益在 Gate 3"。

## 6. 时间与人力

| 阶段 | 人日 | 产出 | 可回滚性 |
| --- | --- | --- | --- |
| Gate 0 | 0.5–1 | 两个对照数（Wails 减重上限 / MyGo picker 原型） | 全部在 `scratch/` |
| Gate 1 | 3–5 | shot 原型 + OCR/字体/托盘结论 | 全部在 `scratch/` |
| Gate 2 | 8–10 | 等价迁移、无 Wails 依赖 | 分支级，tag 兜底 |
| Gate 3-1 picker | 2–3 | 首个真实收益 | 单窗口 |
| Gate 3-2 capture | 2–3 | 材质/拖拽 | 单窗口 |
| Gate 3-3 shot | 3–5 | 收编 Gate 1 | 单窗口 |
| Gate 3-4 主窗 | 15–25 | 去 WebView2 依赖（编辑器仍内嵌） | 分块 |

到"三小窗原生、主窗仍 web"约 **3–4 周**（含 e2e 改造）；主窗原生化另算，且可以永远不做。

## 7. 什么情况下不换（Abort 判据）

出现任意一条就停在 Wails、把成果写成 `HISTORY.md` 的结论，等 MyGo 1.0：

- Gate 0/1 里原生输入法的中文手感明显不如 web 输入框（粘贴/组合/候选偶发丢失）；
- 遮罩窗在混合 DPI 下无法稳定铺满，或抢前台不可靠（这是我们产品的命门）；
- 框架在 2 周内出现破坏性 API 变更，或关键 issue 无人响应；
- `mygo build` / 打包链无法保住现有签名 + 更新链，而我们又不接受"长期不用它的打包器"。

## 8. 明确不做（anti-goals）

- ❌ 一个 PR 里同时换框架 + 重写 UI；
- ❌ Gate 2 期间顺手改设计、换组件、优化 CSS；
- ❌ 现在就替掉 `docs/RELEASE.md` 的打包/签名/发版链（先让 `mygo build` 只当开发/构建工具，发布链等 Gate 2 全绿再评估）；
- ❌ 为主窗"纯原生"改造 Markdown 存储模型——Vditor 走内嵌 webview；
- ❌ 把手写 Win32（抓屏/键盘模拟/剪贴板格式）改成框架 API——MyGo 不提供这些，现有代码是资产。

## 9. 终局形态（可以先定下来）

```
MyGo 壳（托盘/三热键/单实例/自启/更新/事件）
├─ main     原生 UI（左栏 + 时间线列表 + 详情）+ 内嵌 WebView（Vditor 编辑区）
├─ picker   原生 UI
├─ capture  原生 UI
└─ shot     原生 UI
```

停在"三小窗原生 + 主窗 web"也是合格终局：内存大头已回收，WebView2 依赖还在但可接受。
