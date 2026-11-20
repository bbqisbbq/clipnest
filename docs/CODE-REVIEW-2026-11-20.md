# 代码审查 + 主题优化（2026-11-20）

> 范围：应用户要求做的一轮"代码审查 / 主题优化"，外加同一批里的三个功能问题
> （复制后列表不刷新、同步没过来、草稿恢复）。结论先行，证据与命令附在后面。
> 版本定位：2.8.1 → 本次改动准备发 2.8.2。

## 一、结论

| # | 问题 | 根因 | 处理 |
| --- | --- | --- | --- |
| 1 | 复制了东西，要切一下页面才出现在列表里 | 主窗口**从来没有监听** `clip:new` / `clip:updated`（只有 picker 弹窗听了）；列表只在挂载/换筛选器时加载一次 | App 订阅 4 个采集事件 → 列表与待办重载、侧栏计数刷新；后台刷新**不把用户拽回底部** |
| 2 | 家里那台机器同步不过来的 | 远端 `clipnest_data/` 里**只有本机一个包**，另一台从没上传过；本机日志也从未出现「2 台设备」 | 产品侧：同步结果与「测试连接」现在会明确说"远端只有本机一个包"，设置页给出下一步操作；家里那台需要按下方清单自查 |
| 3 | 草稿能不能恢复 | 旧实现的防抖在**浏览器里**：400ms 静默期内强杀进程就丢字；原生收起窗口不一定触发 `visibilitychange` | 防抖挪到 Go（内存立即更新 + 400ms 合并写库），并在 `WindowHide` 钩子补发 `app:hidden`、退出时 `Shutdown` 兜底 flush；新增 4 条单测 |
| 4 | 主题/配色 | 见第三节：弱化文字在 `--surface-3` 上不足 4.5:1、类型色底写死深色值、`data-theme` 与预设深浅不一致时浅色覆盖失效、logo 方块白字在亮强调色上只有 2.7:1 | 全部按 token 修掉，并加对比度体检脚本 `scratch/theme-audit.mjs` |

## 二、逐条证据

### 1. 列表不刷新

`frontend/src/components/EntriesView.tsx` 只有一个 `reloadSignal`（输入框存下一条时 +1），
`hooks/useEntries.ts` 的 `loadLatest` 只在 `baseQuery` 变化时跑。后端其实一直在发事件
（`internal/clip/service_capture.go`：`clip:new` / `clip:bumped` / `clip:updated` / `clip:stats`），
但主窗口没人接——**唯一的消费者是 `Picker.tsx`**。

改法（`App.tsx`）：订阅 `clip:new|bumped|updated|cleared` → `liveTick+1` + `refreshMeta()`；
`EntriesView` / `TodoView` 收到 `liveSignal` 重载数据。`loadLatest({ stick: false })` 是新增参数：
后台来的条目不该把正在翻旧记录的人拽回底部（原来 `loadLatest` 无条件 `stickToBottom = true`）。

验证：`scratch/e2e-live-and-draft.ps1` 阶段 4——`Set-Clipboard 'live-refresh-e2e-123'` 后
**不点任何东西**，2.5s 后截图里已出现该条目，侧栏计数 1 → 2
（`scratch/e2e-shots/live-4-after-copy.png`）。

### 2. 同步

硬证据（只读，PROPFIND 远端目录）：

```
GET/PROPFIND https://dav.jianguoyun.com/dav/clipnest_data/
→ 只有 6d77f2e95764d8f2-desktop_eqptgvl.zip（11,978 B）
```

这个包解出来是 `device: DESKTOP-EQPTGVL`、3 条条目、37 条墓碑——就是**本机自己**的包。
`%APPDATA%\ClipNest\clipnest.log` 里 11-17 至今所有 `sync ok: ...` 行都**没有**
`（N 台设备）` 后缀（该后缀只在 `len(peers) > 0` 时拼），也就是说**本机从没见过对端的包**。
远端 `/dav/` 根下也没有别的 clipnest 目录（`我的坚果云/`、`BBQ/`、`legado/` 都翻过）。

结论：这台机器同步本身是通的（PUT/PROPFIND 都成功），问题在**家里那台没有写上去**。
排除法清单（在家里那台按顺序看）：

1. 设置 → 云同步里「启用同步」是否打开、地址是否**完全一致**（`https://dav.jianguoyun.com/dav/`，
   注意结尾斜杠不影响，但多写一层目录就写到别处去了）；
2. 点一次「立即同步」，再看「测试连接」那句话：连不上会直接报错（401/404/DNS）；
   如果显示"远端只有本机这一个包"，说明它连上了但写的是**另一个目录**；
3. 看那台的 `%APPDATA%\ClipNest\clipnest.log` 有没有 `sync failed:`；
4. 坚果云要用**应用密码**（第三方应用专用），用登录密码会 401。

> 另外顺手说明一件与同步无关、但你可能关心的事：本机库里现在只有 **3 条**
> （最新 3 条都是今天 09:11~09:19 采集的，最早一条 11-19 15:39），
> 文件里 695 页有 433 页是空闲页（说明这个库里曾经有大量条目、后来被删掉了），
> 而 `tombstones` 里今天的删除记录是 0 条。也就是说这批条目**不是走应用的删除接口**
> 没的（那条路会写墓碑）。如果你记得昨天在哪一步点过"清空历史"，就对得上；
> 如果不是，请告诉我，我再顺着 `entries_fts` / WAL 的历史值挖一层。

产品侧这次补的可见性：

- `sync_run.go`：只有本机一个包时，结果行追加「（远端只有本机一个包：其他设备还没上传过）」；
- `sync.go` 的 `TestSync`：同样情况直接给出下一步操作，而不是"连接成功，远端有 1 个设备"；
- 设置页设备表下方：`peerCount === 0` 时显示一条 `.sync-hint`，说明要去对方机器做什么。

### 3. 草稿

旧行为（实测，`scratch/probe-draft-first-typing.ps1` 的结论）：敲完 1.2s 后强杀 → 草稿在；
**敲完 0.15s 点 ×（收起窗口）再强杀 → 有可能丢**，因为防抖计时器还在浏览器里，
而原生 Hide 不保证页面收到 `visibilitychange`。

现在的契约（`internal/clip/draft.go`）：

1. 前端 `useComposerDraft` **不再防抖**：每次输入 → `SetComposerDraft(text)`（一次 Wails 调用）；
2. 后端内存立刻更新（`GetComposerDraft` 优先返回它），SQLite 写按 400ms 静默期合并；
3. 三个落库入口：静默期到点、`FlushComposerDraft()`（前端在 blur / `beforeunload` /
   `visibilitychange` / **`app:hidden`** 时调用）、`Shutdown()` 兜底；
4. `app:hidden` 由 `wire.go` 在 `WindowHide` 钩子里补发——这是"点 × 收起"那条路径能保住草稿的关键。

单测（`internal/clip/draft_test.go`）：静默期自落库、显式 flush、Shutdown 兜底、
空白折成空串、超长截断，5 条全绿。

实测：`scratch/e2e-draft-robust.ps1`——敲完**立刻**点 × 收起、300ms 后强杀，
重启后草稿完整回到编辑器（`scratch/e2e-shots/draft-robust-1-restored.png`）。

还剩下的极限情形：**强杀/断电发生在最后一次按键后的 400ms 内**，最后几个字会丢。
要继续压缩只有一个办法——每键都落盘（大草稿会很费），暂不做。

## 三、主题 / 配色

体检脚本 `scratch/theme-audit.mjs`：把 5 个预设的 token 解析出来，按"文字 × 实际可见的
四种底色（bg / surface / surface-2 / surface-3）"算 WCAG 对比度。修前最差的几组：

| 组合 | 修前 | 修后 |
| --- | --- | --- |
| paper `--text-3` on `--surface-3` | 3.03 | 4.60 |
| paper `--warn` on `--surface-3` | 3.47 | 4.58 |
| ink `--text-3` on `--surface-3` | 3.63 | 4.58 |
| default-dark `--text-3` on `--surface-3` | 3.70 | 4.55 |
| default-light `--accent` on `--surface-3` | 4.28 | 4.58 |
| 浅色 `--k-image` on `--surface-3` | 4.15 | 4.58 |

其它主题问题与处理：

1. **写死的色值**：条目类型的 chip/图标底（12 处）、危险按钮（6 处）、同步状态绿（3 处）
   在原处写死的是**深色主题那套 rgba**，切到暖白/纸感时底色和字色是两个色相
   （而且浅色下那层 12% 蓝紫铺在米白上会发灰）。全部改成
   `color-mix(in srgb, var(--k-*) 12%, transparent)`，跟着当前 token 走。
2. **logo 方块**：`.tb-mark` 写死 `color: #fff`，而深色预设的强调色是亮靛蓝——白字只有 2.7:1。
   改成 `var(--on-accent)`。
3. **`data-theme` vs 预设深浅**：`theme-light.css` 里的浅色覆盖（类型色、遮罩、阴影、
   `color-scheme`）原来按 `[data-theme="light"]` 命中，而 `data-theme` 是**用户/系统选的**
   深浅、不是**预设自己的**深浅。选「纸感」+ 系统深色时这些覆盖全不生效，深色那套类型色
   会压到米白底上。改成 `[data-scheme="light"]`（`applyTheme` 两个属性都写）。
4. **不可见控件**：`.switch` 关态圆钮写死 `#5c6473`，浅色下像脏点；`.win-btn.is-close:hover`
   写死 `#d64545`，和 `--danger` 是两种红。都换成 token。
5. **可访问性**：加全局 `:focus-visible`（以前只有 `snippets.css` 给三个控件写了，
   按钮/侧栏行/菜单项用键盘 Tab 过去完全看不出焦点）；加
   `@media (prefers-reduced-motion: reduce)` 把过渡压平。

跑过的验证：`node scratch/theme-audit.mjs`（数值）、`scratch/e2e-theme-shots.ps1`
（4 个预设各截一张整窗）、`pwsh -File scripts/visual-check/check-css.ps1`（层叠回归样板页）。

## 四、留给下一轮的（没动，但建议做）

1. **`as never` 有 10 处**（`GetEntries({...} as never)` 等）：生成的绑定用了 `interface` 形态，
   `EntryQuery` 的可选字段在 TS 侧过严，于是各处强转。值得单独一轮把 `frontend/src/lib/api.ts`
   的查询类型手写一份，把这 10 处强转去掉——现在它们会吞掉真正的类型错误。
2. **`useEntries` 的分页与"贴底"耦合**：`stickToBottom` 是个 ref + effect 的隐式协议，
   这次加的 `stick: false` 让调用方必须知道这件事。用 `useSyncExternalStore` 或把滚动策略
   收进一个 `useStickToBottom(ref)` 会更直白。
3. **`internal/clip` 还剩几个 350 行以上的文件**（`sync_run.go` 425 行、`entry_query.go`、
   `clipboard_formats_windows.go`）：照 11-19 那次的口径可以再切一刀。
4. **同步没有"冲突可见性"**：现在按 `updated_at` 谁新谁赢，用户看不到"这条被对面覆盖过"。
   如果以后要多端重度使用，值得加一个"最近一次同步改动了哪几条"的列表（`SyncDetail`
   已经有位置放，只是没记明细）。
5. **首次同步的进度**：上千条时会静默几十秒。可以在 `clip:stats` 旁边加一个进度事件，
   让设置页显示"正在拉取 3/8 个包"。

## 五、验证命令（全部通过）

```powershell
cd frontend; npx tsc --noEmit; npx vite build
go build -o bin\clipnest-dev.exe .; go vet ./internal/... .
go test ./internal/...                      # 含新增的 5 条草稿用例
node scratch\theme-audit.mjs                # 主题对比度体检
pwsh -STA -File scratch\e2e-live-and-draft.ps1   # 复制后即时出现 + 草稿落库
pwsh -STA -File scratch\e2e-draft-robust.ps1     # 敲完立刻收起窗口也不丢稿
pwsh -STA -File scratch\e2e-theme-shots.ps1      # 4 个预设整窗截图
pwsh -File scripts\visual-check\check-css.ps1    # 样式层叠回归
```
