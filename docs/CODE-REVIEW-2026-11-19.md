# ClipNest 代码审查 · 2026-11-19

审查口径：死代码 / 过时代码 / 自造轮子 / 结构合理性。
范围：`main.go`、`internal/clip`（46 个 .go，含测试）、`internal/expand`、`frontend/src`（65 个 TS/TSX + 15 个 CSS）。

## 0. 方法与验证边界

| 手段 | 覆盖 | 局限 |
| --- | --- | --- |
| 全文阅读 | `main.go`、`internal/clip` 24 个实现文件、`internal/expand` 主要文件、前端全部入口组件与 CSS 相关段 | 未逐行读 `clipboard_*.go`、`dib/ole/watcher`、全部 `*_test.go` |
| `go vet ./internal/...` | 全通过，exit 0 | 不查包级未使用符号 |
| grep 级符号计数（非测试 Go / 测试 Go / `frontend/src` / `frontend/bindings` 四集合） | 死函数、未调用绑定方法 | 反射 / `linkname` / 动态调用可能漏判 |
| CSS 选择器 ↔ TSX className 逐类比 | 843 个「规则-类名」对 / 378 唯一类 | 模板拼接按「待核」处理（`kind-${…}`、`md-surface--${preset}`） |
| 文档逐条对照代码 | README / ARCHITECTURE / SYNC / SNIPPETS / FILE-STRATEGY / design/* | 只到常量与代码路径层，未跑行为 |

行号按 `read` 全文计（非空行数会小 10%；例如 `store.go` 空行 443、实际 493）。

---

## 1. 🔴 先修的真 bug（不是风格问题）

### 1.1 文件巡检每 3 分钟把 `UpdatedAt` 推到"现在"，同步指纹因此永不稳定

证据链：

1. `internal/clip/service_capture.go:34-49` — `refreshFilesLoop` 每 3 分钟跑一次，对最多 500 条 `KindFiles` 条目调 `RefreshEntryFiles`。
2. `internal/clip/entry.go:322-334` — `RefreshEntryFiles` 里面是 `s.SaveEntry(e)`，**复用了整条保存路径**。
3. `internal/clip/entry_store.go:183` — `SaveEntry` 无条件 `in.UpdatedAt = now`。
4. `internal/clip/sync_package.go:275` — `packageFingerprint` 把 `it.UpdatedAt.UnixNano()` 算进指纹。
5. `internal/clip/sync_run.go:252-257` — 指纹等于上次就跳过 PUT。

结果：只要库里有 `KindFiles` 条目，指纹每 3 分钟必变 → **每次同步都整包 PUT**，"内容未变化，已跳过上传"这条路径永远走不到；而且 `UpdatedAt` 变新会把"文件还在"当成内容修改推给对端。

顺带：`sync_package.go:270-272` 的注释自称 "not the updatedAt stamp"，与 :275 的实现自相矛盾。

**修法**：`RefreshEntryFiles` 改成只更新 `files` 列的一条专用 UPDATE（不碰 `updated_at`、不重算 hash、不重建 FTS）。1 处改动，收益是同步流量从"每轮整包"回到"空闲只 PROPFIND"。

### 1.2 单连接 SQLite 让同步合并阻塞界面

`internal/clip/store.go:92` `db.SetMaxOpenConns(1)`（注释只考虑了采集与 UI，没算同步）。
首次同步逐条 `mergeEntry`（`sync_run.go:156-193`），每条 = 1 次 upsert（`entry_store.go:295`）+ FTS 删插（`:233-245`），且每条独立隐式事务 → 这期间所有列表/统计查询排在同一条连接后面。

**修法**：整轮 merge 包一个显式事务批量提交（不用换连接池）；或只读连接用 `?mode=ro` 分离。

### 1.3 `syncOnce` 里 `defer` 写在 `for` 循环内，每台设备的计数虚高

`internal/clip/sync_run.go:113-124`：`recordPeer` 闭包在 `for _, res := range peers` 里 `defer` → 全部推迟到函数返回才执行；闭包读的是函数级累加器 `pulledNew/pulledUpd/deletedLocal`（最终值），而 `newBefore` 是**每轮迭代自己的**快照 → `PulledNew` 被算成「最终总数 − 本轮之前的值」。
`ARCHITECTURE.md:205` 也承认 `syncOnce` 该拆（300+ 行单函数，15–323 行）。

**修法**：`recordPeer()` 直接调用；顺便把 pull / merge / push 拆成三段。

### 1.4 每次保存设置都新起一个"更新检查"，撞自己的限流

`internal/clip/service_settings.go:44-45` 每次 `SaveSettings` 都 `go s.syncLoop()` + `go s.updateCheckLoop()`。
`syncLoop` 有 `syncCancel` 收敛，但 `update.go:320-330` 的 `updateCheckLoop` **没有取消机制**：睡 8 秒后必打一次 GitHub API，`select` 里还只有一个 case（等于 `time.Sleep`）。
设置页连点保存 5 次 = 8 秒后 5 个请求，正好撞 `update.go:127` 自己写的「匿名 60 次/小时」。

**修法**：单一 ticker + 变更时重置；更新检查也塞进可取消的 loop，`select` 补 ctx。

### 1.5 同步包只有最新 500 条，但文档与设计预期是"整份历史"

`internal/clip/sync_package.go:196`：`s.store.ListEntries(EntryQuery{Limit: 500})` — 打包上限硬编码 500 条。
`docs/SYNC.md:20-21,40` 写的是「整份历史 / 把完整历史打包写回去」。
超过 500 条的库，老条目**永远进不了包**，且没有任何提示（既不是设置项也没进日志）。

**修法**：先说清预期（改成「最近 500 条」并写进 SYNC.md），或把它做成设置项并给出"未入包 N 条"的提示。当前状态是"悄悄截断"，最糟的一种。

---

## 2. 死代码

### 2.1 Go 侧（删除代价均低，除标注外）

| 位置 | 是什么 | 证据 |
| --- | --- | --- |
| `picker_windows.go:185,212,316,337,370` | `WorkAreaBottomRight` / `MakeNonActivating` / `sendOneInput` / `InjectUnicodeText` / `InjectBackspaces`（隐写模式遗留注入） | 只有定义 + `platform_other.go:45-49` 桩；`SendPaste` 才是活的（`snippet_host.go:140`） |
| `picker_windows.go:83-96,40-46` | 随之可删的 `inputT` / `keyboardInput` / `vkBack` 等常量 | 同上 |
| `snippet.go:264` | `snippetBlobExt` | 0 引用 |
| `snippet.go:226` | `UpsertRemoteSnippet` | 0 调用；且它把 `UpdatedAt` 传给 `SaveEntry`，那条路已被 `entry_store.go:183` 覆盖 → 即使被调也是坏的 |
| `snippet.go:316` | `SnippetAction` | 0 调用（`Picker.tsx:7` 注释还引用它） |
| `snippet.go:44` | `Snippet.Size` 字段 | 从不赋值/读取 |
| `snippet.go:283-286` | `SaveSnippet` → `SaveSnippetToEntries` 纯转发 | 名字里的 `ToEntries` 是片段表废弃的化石 |
| `sync_package.go:60-64` | `snippetRecord` 类型 | 0 引用 |
| `sync.go:41-42` | `legacySyncFile` / `legacyManifest` | 0 引用，且没有对应迁移代码 |
| `update.go:333` | `updatePlatform()` | 0 引用；`runtime` 导入只为它 |
| `snippet_host.go:182` | `errEngineMissing` | 仅自身（注释："while the engine is being wired"） |
| `platform_other.go:51` | `errNoOp` | 0 引用 |
| `shell_windows.go:62` + 桩 | `clipboardOwnerApp()` | 0 调用；`source_app` 现由 `foregroundAppName()` 提供 |
| `entry_store.go:329,348` | `FindEntryByTitle` / `AppendTextToEntry` | 0 调用（隐写模式已删） |
| `entry_store.go:25` | `entries.auto_tags` 列 | 不在 `entryColumns`，全仓无读写（代价中等：删列要迁移，留着无害） |
| `settings.go:75` / `sync.go:38` | `Settings.SyncFile` / `DefaultSyncFile` | URL 只由 `SyncURL + packageName()` 拼；前端 `syncFile` = 0 |
| `expand/expand.go:79,95` | `Engine.stop chan struct{}` | 只创建不读取，`Engine` 也没有 `Close()` |
| `template.go:24` | `RenderTemplate` 导出壳 | 不是 service 方法 → bindings 里没有；注释却写 "Exported so the UI can preview it" |

### 2.2 前端 CSS：93 个类名 0 命中，约 658 行整块规则已死（占规则行数 20%）

| 文件 | 死块/规则行 | 代表 |
| --- | --- | --- |
| `entries.css` | 307/439（70%） | 旧双栏列表残留：`.list-head` `.search*` `.list` `.row` `.row-icon` `.kind-*` `.row-body/title/meta/time/actions` `.chip` `.icon-btn` `.empty-state` `.detail-placeholder` `.detail-title` `.code-block` `.image-card` `.file-list/.file-item/.file-text/.file-name/.file-path/.file-size` `.custom-card` `.detail-actions` |
| `snippets.css` | 216/366（59%） | **整个「片段视图」页已不存在**：`.app.is-snippets` `.snippets` `.snip-list` `.snip-rows` `.snip-row*` `.snip-editor*` `.snip-fields` `.var-chips` `.snip-preview` `.side-primary` `.usage-stats` |
| `timeline.css` | 87/553 | `.app.is-memos` `.memos` `.memo-main` 旧三栏编辑器 `.memo-editor/.memo-input/.memo-preview` `.memo-splitter` `.composer-preview` `.nav-edit*/.nav-icon-*` `.capture-tools/.capture-tool` |
| `picker.css` | 26/321 | `.update-banner*`（更新提示已移到设置页） |
| `editor.css` | 14/113 | `.capture-tools` `.capture-tool` |
| `base.css` | 3/32 | `.snip-preview,.composer-preview` `.is-warn` `.cm-editor`（CodeMirror 时代遗留） |
| `layout.css` | 5/195 | `.side-group` |
| `theme-light.css` | 4/51 | `.snip-preview*` `.snip-trigger` |

⚠️ **不能整文件删**：`.main`、`.kv`、`.settings-bar` 的补丁散在与死块同一文件里（`.main` 只服务 SettingsView、`.kv` 只服务 AboutSection、`is-hidden/is-saved/is-dirty` 在 `timeline.css:205-214` 而其主体在 `settings.css:252`）。按行区间挑删。

### 2.3 「算了但没样式」的逻辑死代码

`Calendar.tsx:108-114` 算 `level` 并输出 `has-${level}`，但 15 个 CSS 文件里**没有 `.has-1..4`**（只有 picker 的 `.has-thumb`）→ 热度档位白算，实际只靠内联 `style={{opacity}}`。

### 2.4 前端 TS 侧

- 孤儿导出：`lib/api.ts:5` `EventNames`、`lib/images.ts:9` `BLANK_PIXEL`、`lib/themes.ts` 的 `contrastRatio`/`resolveScheme`/`sanitizeCustomCSS`（仅模块内用，export 多余）、`lib/editorToolbar.ts` 的 `ToolbarItem`/`SurfacePresetName`、`MarkdownSurface.tsx:22` `SurfacePreset`、`EntryCardProps`（未跨文件用）。
- 依赖：7 个 dependencies 全部在用，**没有多余依赖**。`vditor` 是唯一"双份"（bundle 内动态 import + `public/vditor` 由 `sync-vditor.ps1` 拷 22MB），属离线换 lute 预加载的取舍，非死代码。
- 未定义类名：`SettingsView.tsx:121` 的 `is-shown`（CSS 只有 `.settings-bar.is-hidden`）。

### 2.5 后端已生成但前端从不调用的绑定方法

`GetVersion`、`CaptureHotkey`、`PickerHotkey`、`GetEntryTags`、`GetSnippetImage`（已被 `GetImageData` 取代）、`RestoreFilters`、`PromoteToManual`、`TriggerPreview`、`ExpandActive`、`TestSnippet`、`GrabClipboardImage`、`SnippetDraft`（`frontend/src` 计数均为 0）。
代价中等：删要重新生成 bindings + `tsc --noEmit`（包路径不变，风险低）。

---

## 3. 过时代码

### 3.1 注释与实现不符

| 位置 | 注释说什么 | 实际 |
| --- | --- | --- |
| `sync_package.go:270-272` | 指纹 "not the updatedAt stamp" | :275 就算 `UpdatedAt.UnixNano()` |
| `sync.go:16-33` | 远端 `devices/<id>.zip`、包内 `items/<hash>.json`、`blobs/<hash>.png` | `clipnest_data/<deviceId>-<host>.zip`、`entries/<hash>.json`、`blobs/entry-<hash>.png` |
| `entry_blob.go:99-100` | 「≤512KB 进 `memo_blobs`」「落盘 `<dataDir>/memo_files/`」 | 表是 `entry_blobs`（`entry_store.go:63`）、目录是 `entry_files/`（`entry_blob.go:36`） |
| `expand/expand.go:5-8` | 「`internal/wininput` 处理 Win32 注入 / 钩子 / IME」 | 该包不存在，实现在 `expand/wininput_windows.go` |
| `entry.go:47`、`entry_store.go:328`、`picker_windows.go:184,211` | 「隐写模式在写 title」 | 隐写模式已删，无人写 `title` |
| `MarkdownSurface.tsx:16` | capture 预设"隐藏工具栏（浮窗里那排按钮由 Capture.tsx 自己画）" | `Capture.tsx` 早已不画（`capture-tools` 全死） |
| `App.tsx:94` | 「筛选器编辑：主题化模态（原来是 `window.prompt`）」 | `EntriesView.tsx:122` 仍在用 `window.prompt` 设片段缩写 |
| `snippet.go:85-86`、`:180-181`、`sync_package.go:290` | 重复/错位的 doc 注释（`blobExt` 注释挂在 `truncateRunes` 上） | — |
| `snippets.css:211-214` | 选择器里混入 `.sip-fields`（少个 n 的笔误） | 随文件一起被拆出来 |

### 3.2 重复定义、被 import 顺序覆盖

- `.detail` / `.detail-head` / `.detail-body` 各定义两份：`entries.css:267/301/327` 与 `detail.css:24/37/93`，靠 `detail.css` 在后取胜。
- `.capture*`：`timeline.css:149-187` 与 `editor.css:36-94`，同上。
- `color-scheme: dark`：`theme-light.css:22` 与 `snippets.css:342`；`select, select option` 两份且值不同（`--surface` vs `--surface-3` + 写死 `#e6e9ef`）。
- `.btn-sm` 两份（`controls.css:68` / `picker.css:254`，高度内距不同）。
- 已废弃 token 仍在活规则里：`picker.css` 的 `var(--accent-soft, …)`、`var(--text-muted, …)` 都没定义，全靠兜底值。

### 3.3 兼容分支缺出口

- `expand.go:60,207`（`Suffix` = legacy 单终止符）、`store.go:243-247`（兼容变宽 RFC3339）、`service_capture.go:166-178`（`capture.hotkeyInit` 一次性修默认值）——判断合理，但都没有"什么时候能删"的记录。
- `sync_merge.go:17-30`（`machineOnlyFields`）与 `sync_run.go:210-219`（采纳远端设置时逐字段拷回）**是同一份"机器本地字段"清单写了两遍**：加一个机器本地设置要改两处，漏一处就把对端的热键/自启写进本机。

---

## 4. 文档与代码漂移

| 文档 | 怎么说 | 代码实际 |
| --- | --- | --- |
| `README.md:33` | 「4 套配色预设（默认深色 / 亮白 / 纸感 / 墨黑）」 | 5 套：`default-dark` / `default-light`=**暖白** / `pure-light`=亮白 / `paper` / `ink`（本轮舒适化新增） |
| `README.md:290-291`、`ARCHITECTURE.md:97` | 设置页「七个分区」；组件清单含 `EntryEditor`/`MarkdownEditor` | 8 个分区（多 `EditorSection`）；`EntryEditor`/`MarkdownEditor` 已删，现为 `EntryDetailView`/`MarkdownSurface` |
| `README.md:255-281` | internal/clip 目录树 | 漏 `entry_filter_rule.go`、`todo.go`、`update.go`、`syncpanel.go` |
| `README.md:248` | 表结构见 `entry_store.go` 的 `schemaSQL` | 无此常量：`entrySchema`（`entry_store.go:16`）+ `coreSchema`（`store.go:50`） |
| `README.md:293` | styles 清单 13 个文件 | 14 个（漏 `fonts.css`） |
| `ARCHITECTURE.md:19-70` | 「文件表就是去哪找的唯一答案」 | 缺 `update.go`、`snippet_exec_windows.go` |
| `ARCHITECTURE.md:109` | 自带 Maple Mono「三字重」 | `styles/fonts.css` 只有一个 400 字面（同文件 :128 自己也这么说） |
| `ARCHITECTURE.md:199-201` | `go vet` 对 win32/ole 报 4 条 "possible misuse" | 实测 `go vet ./internal/...` 零输出 exit 0（两个 `ptrFromUintptr`/`hookParamPtr` shim 已绕开） |
| `ARCHITECTURE.md:15` | 「单文件超 350 行就该再切一刀」 | `store.go` 493、`snippet.go` 446、`picker_windows.go` 435、`entry_api.go` 380、`entry_store.go` 363、`entry_query.go` 354、`snippet_actions.go` 346 |
| `docs/SYNC.md:20-21,40` | 包内是「整份历史」 | 上限 500 条（见 1.5） |
| `docs/SYNC.md:119-120` | 「想更小就调低『最多保留条数』」 | 该设置在 2.3 已删，`ARCHITECTURE.md:179` 明写"不自动清理" |
| `docs/SYNC.md:135`、`MEMOS-DESIGN.md:171` | 包内 `items/`、`snippets/` | 只有 `entries/`（2.0.0 统一） |
| `MEMOS-DESIGN.md:236,212` | 快速输入窗 560×190 / 420×200 | `service_capture.go:124-125`：620×220 |
| `MEMOS-DESIGN.md:294-298` | 预设 4 条、`default-light`=亮白、深色 id `default` | 5 套、`default-light`=暖白、`default-dark` |
| `docs/design/2026-11-19-layout-rhythm.md:50,89,93` | `--read-w: 760px`；侧栏 `7px 9px`；卡片 `14px 16px` + 12 间距 | `base.css:57` 700px；`layout.css:172` `6px 9px`；`timeline.css:56-57` `13px 16px` + 10 间距（本轮舒适化改的） |
| `docs/design/2026-11-19-todo-filters-editor.md:386` | capture「工具栏隐藏，按钮由 Capture.tsx 自己画」 | 2.6.1 已删那排按钮 |
| 同上 `:219` | 图标网格「14 个」 | `FilterDialog.tsx` 的 `ICONS` 11 个 |
| 同上 `:209` | 找回内置筛选器「走恢复默认按钮」 | 前端无此入口，只有 `RestoreFilters()` API |
| `docs/SNIPPETS.md:14`、README | 片段「七种类型」 | 后端已有第 8 种 `SnippetHTML`（`snippet.go:29`），前端标签仍只认 7 种（待产品判断） |

> 其中 `README:33`、`layout-rhythm` 的读数、`fonts/css 数量` 这几条是本轮舒适化直接造成的，属于我该一起修的。

---

## 5. 自造轮子：换还是不换

| 自研 | 行数 | 候选库 | 结论 |
| --- | --- | --- | --- |
| `update.go` GitHub Releases + NSIS 静默装 | 303 | `wails/v3/pkg/updater` | **暂不整体换**。两个硬约束：(a) `go.mod` 锁 `v3.0.0`，该版本模块里没有 `pkg/updater`（只在 v3.2.0-nightly 副本里有）→ 换 = 升框架；(b) 框架在 Windows 上是"helper 替换自身 payload"，其 GitHub asset matcher 显式跳过 `installer.exe`，与现在"NSIS 装到 Program Files、靠安装器提 UAC"的分发模型冲突。**但应当补验签**：现在下载完只比对字节数（`update.go:220-223`）。 |
| `sync_dav.go` | 290 | `studio-b12/gowebdav` | **不换**。省 ~150 行，代价是引入低活跃依赖，并重写各家 WebDAV 的 405/404 差异与并发拉取。 |
| `expand/wininput_windows.go` | 332 | `golang.design/x/hotkey` | **不能换（误判）**：全局热键已由框架 `app.GlobalShortcut` 提供；这里要的是"读用户每个按键 + 改写已输入内容"，库只做组合键注册。 |
| `sync_package.go` zip | — | 无 | zip 已是 `archive/zip`；自研的是同步协议（墓碑并集、内容指纹），没有对应库。 |
| 搜索 | — | — | 已是 FTS5 trigram；剩余全表扫描只有 <3 字符 LIKE 兜底、`EntryStats` 数待办、`CountByDay` 整月分组，规模可接受。 |
| `versionLess` / `stripHTML` / `newID` | 40/20/8 | `x/mod/semver` / 已在依赖里的 `x/net/html` / 已间接依赖的 `google/uuid` | 顺手级，净收益 <40 行，**不为此开重构**。`stripHTML` 是唯一值得顺手换的（`x/net/html` tokenizer 已在模块图里）。 |
| 前端 toast / 确认框 / 日历 / 分页 / 对比度 / 格式化 | 13/25/121/110/40/41 | sonner、radix-alert-dialog、react-calendar、tanstack-query、chroma-js、date-fns | **一律不换**：各自需求都带产品语义（热键录制字符串、按筛选器统计热度、`tail:true` + 游标 + 滚动高度补偿、中文"刚刚/昨天"），换库要重写的那部分比省下的多。真问题是"同一件事写了 4 遍"（见 6.2），不是缺库。 |
| 反向问题 | — | — | `golang.org/x/net` 在生产代码里 0 使用，只被 `sync_test.go:17`（WebDAV 测试服务器）用；`go mod tidy` 之后仍是 direct，值得在文档注明"仅测试用"。 |

**唯一"真该换"的两处不是替换，而是补齐**：① 更新包验签；② 卡片 Markdown 与编辑器同一套渲染链（现在卡片走 `marked`、编辑器走 Vditor，同一内容的表格/任务框/脚注可能不一致）。

---

## 6. 结构

### 6.1 【高】CSS 层叠靠 import 顺序，且已被实例覆盖
`main.tsx:15-33` 的顺序就是语义，`editor.css:2` 注释自认"必须留在样式表末尾"。任何 IDE 的 import 自动排序、或 `eslint-plugin-import` 整理都会静默改外观，且没有测试能抓到。
好消息：真正的覆盖关系只有 3.2 的几处，而它们**全是死块或重复块**——把 2.2 的死块删掉，顺序依赖几乎自然消失，这颗雷可以从根上拆掉。

### 6.2 【高】"确认"有 4 套实现
`App.tsx:328`（confirm 状态 + `modal-backdrop`）、`EntryDetailView.tsx:279`（自己一套 `confirm: null|"leave"|"delete"` + 同款 backdrop）、`FilterDialog.tsx:207`（第三套 `.filter-confirm`）、`EntriesView.tsx:122`（`window.prompt`，与主题完全不搭——而这正是 `App.tsx:94` 注释声称已消灭的东西）。
抽一个 `<ConfirmDialog>` + `useConfirm()` 一次解决。

### 6.3 【高】`clip` 包里躺着 100+ 行 Win32 注入，违反自己的铁律
`picker_windows.go:275-385` 与 `expand/wininput_windows.go:96-178` 的 `inputT`/`keyboardInput`/`sendUnicodeText`/`sendBackspaces`/`sendLeftArrows` **几乎逐行重复**；`template.go:54-63` 的 `leftArrows` 又与 `expand.go:371-380` 的 `CursorOffset` 逻辑同源。
`ARCHITECTURE.md:74` 说"业务层不许直接调 Win32"。删掉 2.1 的死注入后，`clip` 侧只剩 `PasteInto`/`SendPaste`，重复自然收敛。

### 6.4 【中】`main.go` 438 行、`main()` 一个函数装完所有装配
12 个函数级变量 + 3 个窗口 + 托盘 + 热键 + 钩子回调全是闭包。可测试性为零。建议按"窗口组 / 托盘组 / 热键组"拆成 3 个 `wire*()` 返回结构体。

### 6.5 【中】锁粒度与"钩子回调里做重活"
`ClipService.mu`（`service.go:68-75`）一把锁护 5 个不相关关注点；`Engine.mu`（`expand.go:71`）在 `FeedKey` 全流程持有，而 `FeedKey` 跑在**低级键盘钩子回调**里，期间还调 `imeBusy()`（跨进程 `ImmGetContext`）与 `ForegroundProcess()`（`OpenProcess`）。
Windows 会静默摘掉超时的钩子 → 展开器"忽然不工作"。建议回调只入队、判定移出、IME/进程名加短 TTL 缓存。

### 6.6 【中】错误处理三种口径
`bool`（`Store.DeleteEntry`、`ClipService.DeleteFilter`）、`error`（多数 service）、静默吞（`store.go:232,248`、`entry_api.go:267,280`、`service_paste.go:44`）。
前端拿到 `false` 分不清"不存在 / 删不掉 / 内置保护"。另外 `store.go:116` 的 `dropLegacyTables` 跑在 `logDirValue.Store(dir)`（:118）**之前**，它内部用 `Logf` → 迁移失败的日志会丢。

### 6.7 【中】`App.tsx` 352 行的职责边界
注释说"只做三件事"，实际还管筛选器 CRUD、更新检查、同步触发、脏标记拦截；`leaveDetail`（158-173）与 `onOpenSettings`（214-228）**手写了两遍同一个 detailDirty 拦截**。`view/detailEntry/detailFrom/detailDirty` 是个隐式状态机，建议收成单一对象并补 6 行转移表。

### 6.8 【低】
- `EntryCard` 11 个回调 prop 纯转发（`EntriesView.tsx:203-216`）→ 收成 `actions` 对象。
- 主题有两个入口：`main.tsx:44-47` 手写一遍 `applyTheme`（含 `as` 强转）与 `hooks/useTheme.ts:13-30` 各有一个默认值和 localStorage key 必须人工同步。
- `lib/window.ts:26-27` 用了 `React.CSSProperties` 却没 import（靠全局 UMD 命名空间）。
- `SettingsView.tsx:121` 对含 3 个可点按钮的 `<footer>` 加 `aria-hidden`，应改 `inert`。
- `lib/editorToolbar` 手写的 `subscribeToolbarSettings` + `useState` 可用 `useSyncExternalStore` 替掉。

---

## 7. 建议的行动顺序

**第一批（P0，半天内，都是小改动大收益）**
1. `RefreshEntryFiles` 不再走 `SaveEntry`（1.1）—— 命令：`internal/clip/entry.go:322`。
2. `syncOnce` 里 `defer recordPeer()` 改直接调用（1.3）。
3. `updateCheckLoop` 加 ctx + 单 ticker（1.4）。
4. 同步包 500 条：改文档或改设置，别继续悄悄截断（1.5）。
5. 删 §2.1 的 Go 死代码（约 −200 行）+ 重新生成 bindings。

**第二批（P1，一两天）**
6. 删 §2.2 的死 CSS（约 −650 行），删完重跑 `scripts/visual-check/check-css.ps1` + `scratch/layout-preview/snap.mjs` 对比截图；顺手把 `.main`/`.kv`/`.settings-bar` 补丁归位，让 CSS 不再依赖 import 顺序（6.1）。
7. 抽 `<ConfirmDialog>` + `useConfirm()`，消灭 4 套确认（6.2）与 `window.prompt`（3.1）。
8. 整轮 merge 包事务（1.2）。
9. 更新包补验签（5 表格）。

**第三批（P2，重构级，各自单独立项）**
10. `syncOnce` 拆 pull/merge/push + 错误口径统一（1.3/6.6）。
11. `clip` 抽 `internal/winplat`，消除双份 SendInput（6.3）。
12. `main.go` 拆 wire 函数；`ClipService.mu` 拆锁；钩子回调只入队（6.4/6.5）。
13. 文档批量校正（§4 全表，含我本轮舒适化造成的 3 条）。

---

## 8. 本次没验证到的

- 没跑 `go test`、`go build`（避免覆盖 `bin/` 与占用正在运行的实例），所以"删除后是否仍编译"未验证。
- 死符号判定是 grep 级计数，不是静态分析器；`//go:linkname`、反射、Wails 绑定反射调用等边界可能漏判。
- 未逐行读 `clipboard_*.go` / `dib` / `ole` / `watcher` / `entry_filter_rule.go`，所以"手写 DIB/RTF/HTML 解析该不该换库"只给了方向性判断。
- `frontend/bindings` 里"未调用"的方法，若被动态名或包装名调用，需人工确认 `lib/api.ts`。
- Wails updater 的可行性只读了框架源码，未做升级试验。

---

## 9. 处理记录（2026-11-19「全部修」）

| 项 | 状态 | 落点 |
| --- | --- | --- |
| 1.1 巡检污染 updated_at | ✅ | `entry.go` 的 `RefreshEntryFiles` 改成只写 `files` 列；无变化时返回 false，不再白发 UI 事件 |
| 1.2 单连接堵塞 | ✅ | `store.go`：`MaxOpenConns(4)`（WAL 本就开着）；整轮 merge 包事务留作后续（写者仍互斥，读数已不排队） |
| 1.3 defer-in-loop | ✅ | `sync_run.go` 重写为 `syncRun` + listPeers/pull/merge/adoptSettings/push/report，每台设备计数就地写回 |
| 1.4 重复更新检查 | ✅ | `update.go` 的 `updateCheckLoop(ctx, delay)` + `restartUpdateCheck()`；`service.go` 加 `updateCancel` 并在 Shutdown 取消 |
| 1.5 包上限 500 | ✅ | `syncMaxEntries` 常量 + 满额记日志；`docs/SYNC.md`、`README.md` 改为"最近 500 条" |
| 2.1 Go 死代码 ~200 行 | ✅ | 隐写注入四函数、`UpsertRemoteSnippet`/`SnippetAction`/`snippetBlobExt`/`snippetRecord`/`legacy*`/`updatePlatform`/`FindEntryByTitle`/`AppendTextToEntry`/`errEngineMissing`/`errNoOp`/`clipboardOwnerApp`/`RenderTemplate` 导出壳/`Engine.stop`/`Snippet.Size`/`Settings.SyncFile`/`entries.auto_tags`；另删 12 个前端从不调用的绑定方法，bindings 已重生成（67 methods） |
| 2.2 死 CSS 771 行 | ✅ | `scratch/css-cleanup/dead-css.mjs`（AST 思路的规则级删除，动态类名前缀视为活）；**主窗前后像素差 0**（`scratch/ui-review/after-comfort-baseline.png` vs `after.png`） |
| 2.3 日历 has-N 白算 | ✅ | `Calendar.tsx` 去掉 `level` 计算 |
| 2.4/2.5 孤儿导出与未用依赖 | ✅（依赖本就干净） | `is-shown` 补了 `inert`；孤儿导出保留 `contrastRatio` 等（模块内自用，改成非导出反而多一处 diff，暂不动） |
| 3 过时注释 | ✅ | `packageFingerprint`、`sync.go` 包格式、`entry_blob.go` 表名/目录、`expand.go` 分层注释、`MarkdownSurface` capture 注释、`App.tsx` 的 window.prompt 注释（已真的消灭） |
| 4 文档漂移 | ✅ | README（预设 5 套 / 目录树 4 个文件 / entrySchema / entry_files / 组件清单 / 八个分区 / fonts.css）、ARCHITECTURE（file 表补 2 行、vet 零告警、单字重、MarkdownSurface、syncOnce 已拆）、SYNC（500 条 / 删 retention 建议 / entries/）、MEMOS-DESIGN（entries/、620×220）、layout-rhythm（头注指向 comfort-pass）、todo-filters（capture 无工具栏、11 图标）、SNIPPETS（第 8 种 html）、根 AGENTS.md（37 文件 / 14 个 CSS） |
| 6.1 CSS 依赖 import 顺序 | ✅ 基本消除 | 删死块 + 合并 `.detail`（border-left 归位 detail.css）+ `body.is-picker .btn-sm` + 删 snippets 里重复的 `select`/`color-scheme` |
| 6.2 四套确认框 | ✅ | 新 `components/Dialog.tsx`（`choose` 底座 + `useConfirm`/`usePrompt`/`useChoose`）；App、EntryDetailView（三向选择）、FilterDialog、EntriesView 的 `window.prompt` 全部接入 |
| 6.3 clip 里重复的 Win32 注入 | ⏳ 部分 | 死的那一份已删除（picker 的五个函数）；`sendLeftArrows`/`keybdTap`/`SendPaste` 与 `expand` 侧仍有 2 处重复，抽 `internal/winplat` 留待单独一次改动（原生输入路径只能编译验证、无法功能验证，不放在同一次提交里） |
| 6.4 main.go 438 行单函数 | ⏳ 未做 | 纯装配胶水，拆成 wire 函数没有行为收益；风险是闭包捕获写错后只能靠装机发现。建议单独一次、装一次机验证一次 |
| 6.5 锁粒度 / 钩子回调做重活 | ⏳ 未做 | 需要先给 IME/进程名加 TTL 缓存并测量，属于有性能语义的改动 |
| 6.6 错误口径 / 日志顺序 | ✅ 部分 | `store.go` 的 `logDirValue.Store` 提到 `dropLegacyTables` 之前（迁移失败终于能留日志）；`bool`→`error` 统一未做（会改绑定签名，收益低） |
| 6.7 App.tsx 脏数据拦截两遍 | ✅ | 收成 `guardDirtyDetail`，`leaveDetail`/`onOpenSettings` 共用 |
| 6.8 小项 | ✅ | `window.ts` 显式 `import type CSSProperties`；`SettingsView` 的 `aria-hidden` → `inert`；`editorToolbar` 订阅改 `useSyncExternalStore`；主题恢复收进 `lib/themes.ts` 的 `storedTheme`/`rememberTheme`（main.tsx 与 useTheme 共用 key 与默认值） |
| 5 更新包验签 | ⏳ 未做 | 需要先决定签名方案（ed25519 vs SHA256 校验文件）与发布流程，不是代码单方面能拍的 |

### 验证

`go build ./internal/... .` ✓ · `go vet ./internal/... .` 零告警 ✓ · `go test ./internal/...` ✓ ·
`npx tsc --noEmit` ✓ · `npx vite build` ✓ · `go build -o bin\clipnest-dev.exe .` ✓ ·
`scripts/visual-check/check-css.ps1` ✓ · 主窗标本图与清理前**逐像素相同** ✓ ·
`wails3 generate bindings -clean=true -ts` 重生成 ✓（67 个方法）
