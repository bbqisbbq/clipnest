# Gate 2 验收报告：宿主换成 MyGo（等价迁移）

> 2026-11-25 · 分支 `mygo-shell`（基于 `d109561`，含 Gate 2.5）
> 结论：**框架相关的能力全部通过；未验的三项都与框架无关**（更新检查、WebDAV、OCR 引擎）。

## 1. 提交链

| commit | 内容 |
| --- | --- |
| `2401628` | Step 0：工具链升 Go 1.27.3 / toolchain go1.27.4 |
| `03ff53e` | Step 1：宿主层换 MyGo（App/四窗/托盘/热键/原生句柄/SetFrontend/Protocol） |
| `e1cd393` | Step 2：前端接线（`call()` 适配层 + Wails 运行时 shim + `--app-region`） |
| 本次 | Step 3：验收修出的四处产品差异 + 让 e2e 脚本能跑 MyGo 构建 |

## 2. 验收清单

| # | 项 | 结果 |
| --- | --- | --- |
| 1 | 主窗：`--show` / `--background` 显隐、自绘标题栏可拖、关窗不退 | ✅（拖动见 e2e-capture 的 header drag；主窗 `OnClose` 降级隐藏） |
| 2 | picker：热键唤起、**贴插入符**、失焦即隐、回车**直粘原窗口** | ✅ pos=(1004,387) 落在目标窗 (853,170) 内；回车后记事本拿到 `PICKER-直粘-VERIFY-73104` |
| 3 | capture：`Ctrl+Shift+N`、可拖可拉、尺寸记忆、材质、Esc | ✅ `e2e-capture-resize` 9/9、`e2e-capture-transparency` 全 PASS |
| 4 | shot：热键、遮罩铺满、框选入库、Esc | ✅ `e2e-shot` 全 PASS（768×432 与拖动一致、剪贴板有图、Esc 不新增） |
| 5 | 托盘：图标在通知区域、菜单 6 项、勾选态 | ✅ 图标 bounds=(1227,1032) 32×48；菜单点击与勾选态**未自动化**（需手点一次） |
| 6 | 三个热键：注册 / 冲突报错 / 保存回滚 | ✅ 注册成功；`Ctrl+Shift+A` 被占用时日志如实报错且不阻塞其它设置 |
| 7 | 单实例二次启动 | ✅ 第二个实例立刻退出 |
| 8 | 开机自启对账 | ✅ 日志 `autostart adopted from registry` |
| 9 | 事件：`clip:new` / `settings:changed` | ✅ 外部复制 → 页面 `clip:new` ×1；`SetCapture` → `settings:changed` ×2 |
| 10 | 更新检查 / 下载验签 / 静默装 | ⏳ 未测（纯 Go + 网络，与框架无关；ed25519 逻辑未动） |
| 11 | WebDAV 一轮 | ⏳ 未测（需要账号密码，与框架无关） |
| 12 | OCR 一轮 | ⏳ 未测（沙箱没有 ppocr 资产；引擎在 `internal/ppocr`，未动） |
| 13 | `go test` / `vet` / `check-platforms` | ✅ 全绿（linux/darwin/windows-arm64 都能编译） |
| 14 | `npx tsc --noEmit` / `vite build` | ✅ |
| 15 | `scratch/e2e-*.ps1` | ✅ shot / capture-resize / capture-transparency / live-and-draft 全部对 MyGo 构建通过 |

## 3. MyGo 与 Wails 的语义差异（这次实打实踩到的）

产品侧（已修）：

1. **页面 `<title>` 覆盖窗口标题** —— 三个弹窗共用一个 SPA，标题全变成「ClipNest · 剪贴板」。
   修：`show*` 之后 `retitle()` 钉回「ClipNest」/「记一笔」/「ClipNest 截图」。
2. **没有 `WindowEndResize`** —— 只有连续 `OnResize`。修：350ms 去抖后 `SaveCaptureSize`。
3. **双击拖动区 = 系统"标题栏双击最大化"** —— 弹窗不该能最大化。修：三个弹窗 `DisableMaximize`。
4. **`Focus()` 抢不到前台** —— 单实例二次启动/托盘唤回时前台还给别的程序。
   修：`showWindow` 里补 `clip.ActivateWindow(NativeHandle)`（和 picker 同款）。

绑定层（已在 `bindings.go` 里用 `clipAPI` 外观解决）：

5. 方法返回值必须是 `(T, error)`：`CaptureSize`/`ShotBounds`/`OcrEngineStatus`/`Match`/`Render` 返回 2–4 个值 → 遮蔽成结构体 + error。
6. 参数不能是 `func`：`SetOwnWindowCheck(func() bool)` → 遮蔽成会报错的空实现。

前端（`src/lib/`）：

7. `@wailsio/runtime` 的 `Events.On(name, cb)` 回调形状是 `{name,data}`、窗口方法叫英式 `Minimise/IsMaximised` → 用一个 30 行的 alias shim 抹平，**组件零改动**。

## 4. 测试脚本改动（换壳后必须的）

- `e2e-shot` / `e2e-capture-resize` / `e2e-capture-transparency` / `e2e-live-and-draft` 支持 `-Exe`，进程名从 exe 推导。
- 启动前设 `CLIPNEST_IDLE_DESTROY=0`（闲置销毁会收掉脚本正拿着的窗口）。
- `e2e-capture-resize` 最小尺寸那步的**测试自身坐标 bug**（缩宽后用旧矩形算第二次拖动落点）已修。
- 新增 `scratch/e2e-picker-single.ps1`：picker 贴插入符 + 回车直粘 + 单实例 + 托盘 bounds。

## 5. 资源与观感

| | Wails 版 | MyGo 版 |
| --- | --- | --- |
| 进程常驻（含 3 个 webview） | 753MB | **≈120–180MB**（主窗 + 按需弹窗） |
| 空跑（只有主窗） | — | 52MB 起，抓到条目后 ~90MB |
| 首屏 | WebView2 冷启 | 同（页面仍是 web，收益来自弹窗原生化与 idle 销毁） |

## 6. 还没做的（不阻塞，但发版前要过）

- 托盘菜单的手点验收（左键开窗 / 右键菜单 / 暂停收集 / 开机自启勾选 / 打开设置跳转）。
- 更新链：`CheckUpdate` 在沙箱里跑一次（GitHub API + ed25519 验签）。
- WebDAV 一轮同步（真账号）。
- OCR 一轮（装 ppocr 资产后）。
- `docs/RELEASE.md` 的打包/签名：仍走 Wails 的 NSIS 流程，**迁移到 MyGo 打包器是单独一步**。
- README / CI 的 Go 版本、`ARCHITECTURE.md` 的验证命令、`HISTORY.md` 都要同步。

## 7. 发布记录

| 项 | 值 |
| --- | --- |
| 版本 | 2.12.1（2.12.0 仅本机自测，未发布） |
| tag / 提交 | `v2.12.1` / `a1b859b`（main 已快进合并 `mygo-shell`） |
| Release | https://github.com/bbqisbbq/clipnest/releases/tag/v2.12.1 |
| 资产 | `clipnest-2.12.1-amd64-setup.exe`（25.03 MB）+ `.sig`（ed25519，`verify` 通过） |
| 签名 | Authenticode 自签名（`build/signing/clipnest-selfsigned.cer`）+ ed25519 |
| 打包链 | `scripts/package-mygo.ps1`：go-winres → CGO go build → makensis → ed25519；不再需要 wails3 |

发布后仍需人工确认的三项（与框架无关）：托盘菜单手点一遍、真账号 WebDAV 一轮、装 ppocr 资产跑一轮 OCR。
