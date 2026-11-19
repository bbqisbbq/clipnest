# 架构与分层

> 目标：一处逻辑只存在一份，改一个功能不需要翻五个文件。
> **每次重构后更新此文档**（文件表就是"去哪找"的唯一答案）。

## 一、后端（Go）

```
main.go                 启动顺序：路径与日志、设置预读、--show/--background、服务与 App、Run
wire.go                 窗口与热键开关（wiring 结构体：三个窗口、托盘项状态、当前热键）
tray.go                 托盘图标与菜单
internal/winplat/       Win32 输入注入（SendInput/INPUT 结构体、Unicode 注入、Chord/Paste、前台窗口）
internal/expand/         缩写展开引擎（零业务知识，只认 Host 接口；注入走 winplat）
internal/clip/           业务层：条目、捕获、片段、同步、绑定 API
```

`internal/clip` 内部按职责分文件（同一 package，靠文件边界保持清晰）。
**单个文件超过 350 行就该考虑再切一刀**：

### 数据与存储

| 文件 | 职责 |
| --- | --- |
| `model.go` | `Kind` / `FileRef` / `Captured` —— 纯数据，无依赖 |
| `settings.go` | `Settings` + 默认值 + 单调时钟 |
| `store.go` | SQLite 打开/迁移、meta、墓碑、统计、诊断日志 |
| `entry.go` | `Entry` 类型与派生方法、采集入库、生命周期（`SaveCapture` 等） |
| `entry_store.go` | entries 表 schema、行映射、`SaveEntry`（含 FTS）、`mergeEntry` |
| `entry_query.go` | `EntryQuery`、`ListEntries`、日历边界、`EntryStats`、标签/缩写检索 |
| `entry_filter_rule.go` | 筛选器规则树（and/or/not）→ SQL 编译、字段白名单、`FilterFields()` |
| `todo.go` | 待办：从正文解析 `- [ ]`、派生 `todo_state`、勾选写回、收集页数据 |
| `entry_blob.go` | 附件字节：`SaveEntryBlob` / `LoadEntryBlob`（≤512KB 进库，更大的落盘） |
| `entry_image.go` | 纯函数：解码、缩略、扩展名/MIME 推断（不含存储） |

### 服务与 API

| 文件 | 职责 |
| --- | --- |
| `service.go` | `ClipService` 装配、`Start`/`Shutdown`、窗口/版本/主题、`GetStats` |
| `service_capture.go` | 采集管线（合并 45ms、去重、回写抑制）+ 快速输入窗（含尺寸记忆：`capture.size` → meta） |
| `service_paste.go` | 条目 → 系统：复制回剪贴板、打开、picker 弹窗三件套 |
| `service_settings.go` | 设置保存与副作用（自启、展开器、两个后台循环） |
| `draft.go` | 快捷输入框的未保存草稿（meta 键 `composer.draft`，跟着 clipnest.db 走） |
| `entry_api.go` | 绑定给前端的条目 API + 筛选器 CRUD |
| `syncpanel.go` | 设置页「设备包」面板的数据形状 |

### 同步（WebDAV 打包）

| 文件 | 职责 |
| --- | --- |
| `sync.go` | 门面：远端布局说明、常量、`SyncStatus`、`SyncNow`/`TestSync`、定时循环 |
| `sync_dav.go` | 传输层：URL/鉴权/`GET`/`PUT`/`MKCOL`/列包、设备 id 与包名 |
| `sync_package.go` | 包格式：索引/记录结构、`buildPackage`/`parsePackage`、指纹 |
| `sync_run.go` | 一次同步的编排：列包 → 并行拉取 → 合并 → 采纳设置 → 上传 → 状态 |
| `sync_merge.go` | 纯规则：可同步字段裁剪（`machineOnlyFields`）、墓碑合并 |

### 片段与平台层

| 文件 | 职责 |
| --- | --- |
| `snippet.go` | 片段 ↔ 条目的适配层（`Snippet` 只是 `expand.Trigger` 的形状） |
| `snippet_host.go` | 实现 `expand.Host`：缩写索引、匹配、渲染、执行 |
| `snippet_actions.go` | 片段动作（粘贴/命令/启动程序/打开网址） |
| `template.go` | `{{date}}` / `{{clipboard}}` / `{{cursor}}` 占位符 |
| `clipboard_windows.go` | 读剪贴板：格式枚举 → 归类 → 各类型 `capture` |
| `clipboard_formats_windows.go` | 格式知识：跳过清单、URL 判定、RTF/HTML → 文本、预览生成 |
| `clipboard_write_windows.go` | 写回：文本/HTML/RTF/图片/`CF_HDROP` |
| `dib_windows.go` | `CF_DIB(V5)` ↔ `image.NRGBA` |
| `ole_windows.go` | OLE `IDataObject` 回退（延迟渲染场景） |
| `watcher_windows.go` | 消息窗口 + `WM_CLIPBOARDUPDATE` 监听 |
| `picker_windows.go` | 光标定位、前台接管、合成 `Ctrl+V` |
| `win32_windows.go` | Win32 基元：剪贴板 API、前景程序、注册表自启 |
| `shell_windows.go` | `ShellExecute` / 资源管理器定位 |
| `platform_other.go` | 非 Windows 桩实现（保持可编译） |
| `snippet_exec_windows.go` | 片段动作的 Windows 实现（粘贴 / 启动程序 / 打开网址） |
| `update.go` | 自动更新：GitHub Release 检查、下载、静默安装 |

**两条铁律**：

1. `*_windows.go` 不许 import 业务概念；业务层不许直接调 Win32。
2. package 归属会影响 Wails 绑定路径（`frontend/bindings/...`）。**拆文件是安全的，挪包不是**：
   要把 `Entry` 或 `Store` 挪去新包，必须同时重新生成绑定并装机验证。

## 二、前端（React + TS）

```
src/App.tsx              薄壳：装 hooks、切视图（含"关设置回原处"）、转发通知（不写业务）
src/hooks/
  useToast.ts            全局通知
  useTheme.ts            主题落地（预设 + 自定义 CSS + 原生边框）
  useAppData.ts          设置 / 应用信息 / 同步状态 / 筛选器 / 计数
  useEntries.ts          条目列表数据层：分页、上滑加载更早、按天分组、贴底滚动
  useEntryImages.ts      正文 memoimg: 引用的按需加载缓存
  useComposerDraft.ts    快捷输入框草稿：内存 → 防抖落库（400ms）→ 重启读回
  useFilters.ts          筛选器增删改（⋯ 菜单 + 新建/编辑弹窗的状态机）
src/lib/
  api.ts                 绑定封装 + 类型别名
  images.ts              正文图片引用的解析与收集（纯函数）
  themes.ts              配色预设、对比度自检、自定义 CSS 净化
  dates.ts               日期键唯一写法："2026-11-19" / "2026-11"（日历、今天、EntryQuery.day）
  format.ts              时间/体积格式化
  window.ts              无边框窗口控制
src/components/
  TitleBar / Sidebar / Calendar / Tip / Icons      布局与小组件
  SettingsView          设置页编排者（草稿 + 自动保存 + 顶部返回/状态条）
  settings/             八个分区（含 Editor）：Capture / Interaction / Sync / Snippets / Appearance / Update / About + ui.tsx
  Composer + ComposerDock  Composer 只管长相（高度、拖拽、按钮），ComposerDock 管"存哪儿、存完通知谁"
  EntriesView           统一内容视图（搜索、卡片操作编排；输入框已不在它里面）
  entries/EntryCard     单条卡片：折叠、图片点击、操作按钮
  EntryDetailView       条目详情：占右侧整区（不是浮层），顶部面包屑返回 + 属性面板
  TodoView              待办收集页：摊平所有条目里的 - [ ] 任务，勾选写回正文
  FilterDialog          筛选器编辑弹窗（主题化模态）+ FilterRuleEditor 规则编辑器
  MarkdownSurface       全应用唯一编辑器（Vditor IR）的适配层；preset = detail/composer/capture
                        ※ 内容的 onChange 靠自己在宿主上听的 DOM `input` 事件（见下方经验），
                          不要指望 Vditor 的 `options.input`——IR 模式手打时它不触发
  SnippetFields         片段的额外编辑项（嵌在详情的属性面板里）
  HotkeyInput / Lightbox / Picker / Capture
src/styles/vditor-theme.css  Vditor 的 token 映射 + 三条硬编码浅色补丁（深色主题必需）
src/styles/
  fonts → base → layout → entries → controls → settings → picker → snippets → timeline → editor → vditor-theme → detail → todo → filter
  （fonts.css 里是 @font-face：自带 Maple Mono NF CN 单 400 字重，字体文件在 src/assets/fonts/）
src/theme-light.css      主题引擎管不到的浅色补丁（--k-* / --shadow-pop / 原生控件）
```

**三条铁律**：

1. `components/` 只管渲染与交互编排，数据靠 props / hook；不直接算业务。
2. `lib/` 里不许有 React（`hooks/` 才可以）。
3. **CSS 的 import 顺序就是层叠顺序**：同优先级的选择器靠后覆盖，新增样式文件要插在
   正确的段位里，不能随手追加到末尾（`main.tsx` 里有注释说明）。

**几条经验**：

- **编辑器的 onChange 不走 Vditor 的 `options.input`**：那个回调只在 Vditor 自己的渲染流程里
  （`processAfterRender` / `setValue`）触发，**IR 模式手打文字不触发**（2.8.0 实测：草稿一直是空的，
  只有点「清空」才回调）。`MarkdownSurface` 因此在宿主元素上自己听冒泡的 DOM `input`（+ 失焦）事件，
  取 `getValue()` 后回调给调用方。任何"内容变了要做点什么"的功能（草稿落库、详情的未保存标记）
  都依赖这条，别改成 `options.input`。
- **主区竖向分两格**：`.pane-body`（当前视图）+ `.memo-composer`（全局输入框，`ComposerDock`）。
  输入框**不在任何视图内部**——它在 App 的最外层，所以切筛选器 / 待办 / 设置时位置和内容都不变；
  详情页是唯一的例外（那是在改一条已有的内容，不要两个"保存"抢地方）。草稿存 `meta.composer.draft`，
  换页面、重启都读得回来。
- **设置是自动保存的**：`SettingsView` 只记"这次动过哪些字段"（`touched`），停手 400ms 合并进
  最新服务端设置再落盘——所以同步线程拉回来的对端设置、侧栏刚设的默认筛选器都不会被一份过期快照覆盖。
  没有"保存/撤销"按钮，只有顶部一行状态。
- **主区只有一条阅读列**：`base.css` 的 `--read-w`（列宽）/ `--gutter`（最小留白）/
  `--read-pad`（居中留白）三个 token 统一了时间线、输入区、待办、详情正文。
  新页面用 `padding-inline: var(--read-pad)` + `max-width: var(--read-w)`，
  **不要**在滚动容器里用 `margin: 0 auto` 居中（滚动条会吃掉 9px，列心左移，和头部差半格）。
  改完跑 `scratch/layout-preview/snap.mjs` 对比截图，见 `docs/design/2026-11-19-layout-rhythm.md`。
- **字体分两套**：`--font-ui` = Inter + Noto Sans SC（比例宽度，长中文段落好读）；
  `--font-mono` = Maple Mono NF CN（等宽，`styles/fonts.css` 里只有一个 400 字面）。
  等宽字体铺满界面会把中文撑成 1.2em 宽、行拉长，所以别再把它塞进 `--font-ui`。
  改字体后跑 `scripts/visual-check/check-css.ps1`（它会把 dist 里的真字体一起加载，
  量出来的折叠高度才和 app 一致）。
- 折叠卡片的"能不能展开"是运行时量出来的（`EntryCard` 里 `scrollHeight - clientHeight > 8`），
  不是按内容类型猜的：短卡片不该出现底部遮罩，也不该冒出「展开全文」。

## 三、数据模型（唯一真相）

```
entries        条目：标题（可选）+ 内容 + 标签 + actions（clipboard / snippet）+ 待办派生列
entry_blobs    附件：内容寻址，≤512KB 进库、更大落盘 memo_files/
entries_fts    全文索引（trigram，中文子串可搜；标题也进索引）
entry_filters  用户保存的筛选器（含规则树 JSON）
tombstones     删除墓碑（同步用）
meta           键值对（设备 id、一次性标记、filters.hidden、composer.draft、capture.size）
```

**草稿不是条目**：输入框里敲了一半的东西存在 `meta.composer.draft`（只有一条键值、只在本机），
不进 `entries`、不进同步包、也不算内容哈希的一部分——半成品不该被当成历史，也不该同步到别的机器。
`SetComposerDraft("")` 即清空，所以"存成条目"之后不会在重启时又被塞回输入框。

**待办是派生的，不是第二张表**：正文里的 `- [ ]` 就是真源，
`todo_state`（`''`/`open`/`done`）与 `todo_done_at` 是 `SaveEntry` / `mergeEntry` 里现算的派生列
（`todo.go` 的 `deriveTodo`）。勾选改的是正文那一个字符，`hash` 不变，所以同步配对不受影响。
派生列**不跨端传**：`mergeEntry` 一律本端重算，否则版本混跑时旧客户端会把状态抹掉。

`entries.title` 目前没有界面在写（旧版隐写模式用它做 `@名称@` 的落点）；空标题的条目在卡片上不多一行。
表结构是 `CREATE TABLE IF NOT EXISTS`，**新增列必须同时加 `ensureColumn` 迁移**（见 `store.go`），
否则老库不会自动长出新列。

`items` / `memos` / `snippets` 三张表已在 2.0.0 删除：它们表达的是同一种东西的
三种形态，现在统一为 `entries` + `actions`。片段 = 带 `snippet` 能力的条目。

**查询只有一套**：侧栏筛选器、日历、搜索框都编译成 `EntryQuery`，SQL 条件由
`EntryQuery.where()`（`entry_query.go`）一处生成，`ListEntries` 与
`CountByDay`（日历圆点）共用它。加新筛选维度只改 `where()` 一处，两边的口径
就不会分叉——日历曾经自己写 SQL 数"全库"，于是选了「片段/图片」后画点却点不出东西。

**自定义筛选器的规则树也走同一个入口**：`EntryQuery.Rule` 由 `entry_filter_rule.go`
编译成 SQL 片段再拼进 `where()`。字段/算子有白名单（`FilterFields()` 是给 UI 的唯一一份），
不认识的会被丢弃并记一行日志——宁可少一个条件，也不要整页查不出来。
规则里显式提到 `archived` 时，代码里那个"默认只看未归档"会让位（否则归档筛选永远查不到东西）。

**哈希有两个口径，别混用**：

| 口径 | 生成处 | 用途 |
| --- | --- | --- |
| `Captured.Hash` | `textHash` / `htmlHash` / `rtfHash` / `filesHash`（`clipboard_windows.go`） | 采集去重、**回声抑制** |
| `entries.hash` | `contentHash`（`entry.go`） | 同步配对、内容级去重 |

写回剪贴板要丢掉"自己刚写出去的回声"，必须用 `entryEchoHash(entry)`（它按上面
第一套口径重算）；直接拿 `entry.Hash` 去比永远不相等，于是手写备忘复制回剪贴板
会多出一条"来源 clipnest"的采集条目。`SaveCapture` 另有一层内容口径去重兜底。

**不自动清理**：没有最大条数、没有保留天数，条目只在用户手动删除/归档时消失
（2.1.0 的产品决定）。`Entry.ExpiresAt` 保留只为兼容旧数据与对端同步包。

## 四、同步

- 一台机器一个包：`clipnest_data/<deviceId>-<host>.zip`
- 包内：`package.json`（索引 + 墓碑 + 设置）+ `entries/<hash>.json` + `blobs/<name>`
- 合并：按内容 hash 配对；`updated_at` 新的赢；`pinned` 取或；删除走墓碑
- 合并必须用 `Store.mergeEntry`（保留远端时间戳）。用 `SaveEntry` 会把
  `updated_at` 盖成"现在"，让本地副本比墓碑还新，删除会被静默忽略。
- **筛选器不跨端同步**（`entry_filters` 不进包），所以指针类设置也不能跨端：
  `Settings.DefaultFilterID` 在 `localSettingsFields` 里，对端带来的值一律丢掉。

## 五、怎么验证

```powershell
go build -o bin\clipnest-dev.exe .        # 后端 + 内嵌前端产物
go test ./internal/...                    # 存储/查询/待办/筛选规则/同步/展开引擎
cd frontend; npx tsc --noEmit; npx vite build --mode production
pwsh -File scripts\visual-check\check-css.ps1   # 样式回归截图（改 CSS 后跑）
pwsh -STA -File scratch\e2e-capture-resize.ps1  # 记一笔浮层：拖动/拉伸/尺寸记忆（真鼠标事件）
```

`go vet ./internal/...` 现在是**零告警**：`win32_windows.go` 的 `ptrFromUintptr` 与
`expand/wininput_windows.go` 的 `hookParamPtr` 两个 shim 已经把 `unsafe.Pointer`
指针算术的误报绕开了（早先那 4 条告警已经不存在，别再照着忽略）。

## 六、后续可以继续拆的地方

1. ~~`sync_run.go` 的 `syncOnce`（300+ 行单函数）拆成 pull / merge / push~~ —— 2026-11-19 已拆：
   `syncRun` + listPeers / pull / merge / adoptSettings / push / report 六段（顺带修掉"每台设备计数虚高"）。
2. `store.go` + `entry_store.go` 抽成 `internal/store`，`Entry` 抽成 `internal/model`，
   `clip` 只留 service / sync / snippet（会改绑定路径，一次只动一层并装机验证）。
3. `*_windows.go` 抽成 `internal/winplat`（同上，注意绑定与构建 tag）。
4. `Picker.tsx` 与 `Capture.tsx` 已经共用 `MarkdownSurface`，可以再共用一层 Composer。
