# Gate 2.5 实测：Wails 版"弹窗闲置即销毁"能省多少、代价是什么

> 2026-11-25 · 分支 `gate2-5-idle-windows` · 目的：在**不换框架**的前提下摸到 Wails 侧的内存天花板，
> 同时给 Gate 2 换壳前留一条可回退的保险。
> 实测机：Windows 11 / 单屏 1920×1080 / 沙箱 APPDATA / 安装版先停后拉回

## 1. 三种配置的对照

| 配置 | 进程数 | 稳态 WS | picker 热键→可见 | 结论 |
| --- | --- | --- | --- | --- |
| **A. 现状**（三窗常驻隐藏） | 11 | **757MB** | **10–17ms** | 基准 |
| **B. 全销毁**（picker/capture/shot 闲置即销毁） | 8 | **448MB** | **77–138ms**（5–10×） | 省 300MB，热路径明显变慢 |
| **C. 只销毁 capture/shot，picker 常驻** | 9 | **542MB** | **10–14ms**（首次 132ms） | **省 215MB（−28%），热路径无退化** |

要点：

- 一个隐藏窗口 = 一个 WebView2 文档 = 约 90MB（renderer 均值）。三个空闲弹窗 ≈ 280MB。
- **picker 必须留**：它是唯一"用户按了就得立刻出现"的窗口，重建要重跑 React（132ms 首开、77–93ms 之后）。
  capture / shot 是"用户预期要等一下"的操作，重建成本可以接受。
- 启动即省：C 配置下启动时只建主窗（8 进程 / 452MB），picker 第一次唤起才建。

## 2. 实现（都在 `wire.go` + 新增 `idle_windows.go`）

- 三个窗口的创建抽成 `ensurePicker/ensureCapture/ensureShot`，展示路径改为 `ensure*()`。
- 隐藏路径（`hidePicker/hideCapture/hideShotWindow`）→ `retire()`：闲置 `CLIPNEST_IDLE_MS` 后销毁。
  到点时若窗口又可见（连按两次热键）就跳过。
- 销毁走 `Window.Close()` + 一个 `allowClose` 标记：`WindowClosing` 钩子在"我们自己的销毁请求"时放行，
  其余情况照旧 `Cancel()` 降级为隐藏。**顺带修掉一个旧隐患**：picker/capture 之前没有 `WindowClosing`
  钩子，被外部关掉会销毁窗口但 `w.picker` 仍留着指针。
- `HideOnFocusLost` 是 Wails 自己隐藏的，会绕过 `hide*()`：补了 `WindowHide` 钩子，否则最常走的
  "失焦自隐"这条路径永远不会触发销毁。

开关（默认关，不设就是原来的行为）：

```powershell
$env:CLIPNEST_IDLE_DESTROY = "1"      # 打开闲置销毁
$env:CLIPNEST_IDLE_MS = "1500"        # 闲置多久后销毁（默认 1500）
$env:CLIPNEST_IDLE_SKIP = "picker"    # 不参与销毁的窗口（推荐就是它）
```

## 3. 验证（四道门，全部用仓库自带脚本）

| 门 | 结果 |
| --- | --- |
| `go test ./internal/...` | ok（4 包） |
| `go vet ./internal/...` | 零告警 |
| `pwsh scripts/check-platforms.ps1` | linux/darwin/windows-arm64 全过 |
| `scratch/e2e-shot.ps1`（热键→遮罩→拖框→条目 768×432→剪贴板→Esc 不新增） | **PASS** |
| `scratch/e2e-capture-resize.ps1`（尺寸记忆/拖动/拉伸/最小尺寸） | **PASS** |
| `scratch/e2e-capture-transparency.ps1`（圆角真透明 + 卡片半透明） | **PASS** |

后两个脚本跑的就是"销毁后重建"的窗口——尺寸记忆（822×367）跨重建保留、圆角与半透明都正常，
说明重建这条路径没有把 capture 的外观弄坏。

## 4. 遗留与风险

1. **重建后不会自动补回 DWM 边框色 / Acrylic 材质**（`applyChrome` / `applyCaptureBackdrop`
   目前只在"设置变化"时触发）。实测半透明与圆角正常（那是前端画的 + Translucent 背景类型），
   但边框色会回到系统默认，直到下次保存设置。修法：`ensureCapture` 成功后回调一次
   `w.applyChrome(...)` / `w.applyCaptureBackdrop(...)`，需要把当前主题/透明度缓存到 wiring 上。
2. **首开变慢**：C 配置下 picker 首次唤起 132ms（之前 10–17ms），只在启动后第一次出现。
   若要消掉，可以在启动后**延迟几秒**预建 picker（用 `time.AfterFunc(3s, ensurePicker)`），
   既保住启动速度又不让首次唤起吃亏——还没做，留给 Gate 2 决定。
3. 销毁/重建期间 `w.picker == nil`，`ownWindow()`、`applyChrome` 都是判空的，已确认安全。
4. 这不是免费的收益：**在 Wails 上最多也就到 542MB**，仍然比原生单窗（48MB）差一个数量级。
   它只是"不换框架能拿到的钱"。

## 5. 已落地（2026-11-25，默认开）

按"默认开 + 只留 picker"落地，并补掉了上面 §4 的两条：

- `CLIPNEST_IDLE_DESTROY` 默认 **开**（要退回三窗常驻：设 `=0`）；`CLIPNEST_IDLE_SKIP` 默认 **picker**。
- 重建后**重放了 DWM 边框色与 Acrylic**：`wiring` 缓存 `chromeDark/chromeBorder/backdrop`，
  `ensureCapture()` 建好窗口后立刻 `applyChromeNow()` + `SetWindowBackdrop(...)`。
- **picker 延迟预建**：`time.AfterFunc(3s, ensurePicker)` —— 启动不拖慢，首次热键也不撞冷建。

默认配置实测（无任何环境变量）：

| 时刻 | 进程 | WS | 说明 |
| --- | --- | --- | --- |
| 启动 1.5s | 9 | 454MB | 只有主窗，picker 还没预建 |
| 启动 4.5s | 9 | 533MB | picker 已预建 |
| picker 热键→可见 | — | — | 24.5 / 16.9 / 10.5 ms（第一次是"从未显示过的窗口"首次 Show） |
| 开一次「记一笔」 | 10 | 687MB | 临时 |
| 用完闲置 2.5s | 9 | **575MB** | 日志：`idle: capture window destroyed` |

稳态 533–575MB（基线 757MB，**−180~220MB**），热路径不变。

## 6. 建议

- **代码保留**（env 开关，默认关）。它便宜、可回退，还能当 Gate 2 换壳前后的对照基线。
- 若要默认开，建议就按 C 配置：`CLIPNEST_IDLE_DESTROY=1` + `CLIPNEST_IDLE_SKIP=picker`，
  再补 §4 第 1 条的 chrome 重放与第 2 条的延迟预建。
- 换壳（Gate 2）之后，这套开关自然作废——MyGo 里 picker 原生窗是 48MB 量级，
  没有"常驻 WebView2 文档"这个概念。

