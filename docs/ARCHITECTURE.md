# 架构与分层

> 目标：一处逻辑只存在一份，改一个功能不需要翻五个文件。
> **每次重构后更新此文档**（文件表就是"去哪找"的唯一答案）。

## 一、后端（Go）

```
main.go                 启动顺序：路径与日志、设置预读、--show/--background、服务与 App、Run
wire.go                 窗口与热键开关（wiring 结构体：三个窗口、托盘项状态、当前热键）
tray.go                 托盘图标与菜单（只放"在托盘上干、在主窗口里干不方便"的动作）
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
| `store.go` | SQLite 打开、`coreSchema`、设置读写、meta、墓碑、统计（349 行） |
| `store_migrate.go` | **老库迁移与一次性修复**：`ensureColumn`、墓碑表换主键、`backfillClipboardCapability`、删旧表。任何"老库也要能跑"的结构变更都写在这里，并配一个老 schema 的测试 |
| `util.go` | 跨文件小工具：`encodeTime`/`decodeTime`（定宽时间戳）、`newID`、`escapeLike`、`encodeFiles`、`hostName`、`placeholderForKind` |
| `log.go` | 诊断日志 `Logf` / `TrimLog`（写失败一律吞掉，绝不连累调用方） |
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
| `draft.go` | 未保存草稿，**两个槽各一把键**：`composer.draft`（主窗口「记一条」）与 `capture.draft`（「记一笔」浮层），都跟着 clipnest.db 走。**写库防抖在 Go 侧**（前端每键送一次，内存立即更新 + 400ms 合并写），`Flush*Draft`（前端在收起窗口/退出前调）与 `Shutdown`（两个槽都写）兜底 |
| `entry_vector.go` | 语义向量的**旁表** `entry_vectors`（id / hash / state / 512 字节 int8），以及字面那一路的 `FTSIDs`、语义那一路的 `NearestIDs`。不碰 `entries`，不进同步包 |
| `search_hybrid.go` | 混合检索：两路各取 top 100 → RRF(k=60) 融合 → 上限 200。语义没准备好时返回 ok=false，查询原样走字面（降级=不做事） |
| `embed_worker.go` | 语义编码队列 + 引擎生命周期（懒加载、闲置 5 分钟释放会话）+ 对账 `reconcileEmbed`。失效靠对账不靠钩子，见该文件顶部说明 |
| `embed_assets.go` | 语义模型资源的一键下载（23.9MB zip，SHA256 校验，白名单解压到数据目录的 `embed/`） |
| `entry_api.go` | 绑定给前端的条目 API（读写、置顶/归档、片段升降级、待办勾选） |
| `filter_api.go` | 筛选器 API：保存/删除/恢复、内置筛选器的覆盖、`filters.hidden` 隐藏位、`GetFilterFields` |
| `service_shot.go` | 截图的业务面：拍帧 → 交给遮罩窗 → 收回裁好的 PNG → 落库 →（可选）进剪贴板。裁切在前端做，这里不做几何计算 |
| `shot_windows.go` | 截图的平台面：GDI `BitBlt` 拍光标所在显示器、`SetWindowPos` 按物理像素摆遮罩窗、把焦点交给 WebView 子窗口 |
| `hotkey.go` / `hotkey_windows.go` | 加速键的"撞车预检"：注册前用 Win32 `RegisterHotKey` 试一次再注销。宿主的 Register 在 `Run()` 之前是排队的、失败拿不到，见 HISTORY「热键注册成功可能是假的」 |
| `capture_imgfetch.go` | 网页「复制图片」的补丁：认出"整段只有一个 `<img>`"的 HTML 片段，按地址把图下载回来（后台跑，失败降级成富文本） |
| `entry_title.go` | 标题：从正文第一个 `# 一级标题` 派生 + `normalizeTitle`（手写锁定 / 自动跟随 / 清空退回）。规则都在这里，别在 UI 里重算 |
| `ocr.go` | 图片 OCR 引擎：Windows 自带 `Windows.Media.Ocr`，经 PowerShell `-EncodedCommand` 调用（脚本纯 ASCII）；外加识别结果的后处理（CJK 空格合并、协议头纠错） |
| `ocr_worker.go` | OCR 队列与 worker（单个串行）+ 启动补扫 + 手动触发 `OcrEntry`。慢（约 0.5~1.2 秒/张），所以绝不挡采集 |
| `entry_ocr.go` | OCR 的存储边：`SetEntryOCR`（写结果 + 重建搜索索引，**不动 updated_at**）、`EntryIDsNeedingOCR`、`OCRStats` |
| `ocr_engine.go` | 引擎选择：`system`（Windows 自带）↔ `ppocr`（PP-OCR），资源在 `<数据目录>/ocr/`；ppocr 不可用时**退回系统引擎**而不是罢工；`OcrEngineStatus` / `OpenOcrAssetsDir` 给设置页 |
| `ocr_assets.go` | PP-OCR 资源的一键下载：拉 `ocr-assets-v1`（pre-release，不影响自动更新）→ 校验大小+SHA256 → 白名单解压 4 个文件（`.tmp` + rename）→ 进度走 `ocr:progress` 事件。**换资源包必须同步改 `ocrAssetsSize` / `ocrAssetsSHA`** |
| `platform.go` | 中立平台契约：`ErrNotSupported`（"这项能力本平台还没实现"，不是 bug） |
| `capture_text.go` | 中立纯函数：采集侧四个哈希口径（`textHash`/`htmlHash`/`rtfHash`/`filesHash`）、`makePreview`、`humanBytes`。**它们在 Windows 侧也要用，所以不许放回 `*_windows.go`** |
| `capture_imagefile.go` | 复制**图片文件**时把图本体存进条目（`imageFileCapture`）：只认单个文件 + png/jpeg/gif + ≤24 MB，存原始字节不重编码；其余情况仍按文件引用记。边界规则与单元测试都在这里 |
| `shot.go` | 中立数据类型 `ShotFrame`（冻结的那一帧：PNG + 物理像素矩形），Windows 抓屏与业务层共用 |
| `internal/ppocr/` | PP-OCRv5 + onnxruntime（**独立包，cgo 只出现在 `engine_cgo.go`**）。`engine_stub.go`（`!windows \|\| !cgo`）给同签名空实现 —— 没 cgo 也能编译。纯逻辑（CTC 解码 / 连通域成框 / 缩放裁剪）在 `ppocr.go`，能不加载模型直接测 |
| `syncpanel.go` | 设置页「设备包」面板的数据形状 |

### 同步（WebDAV 打包）

| 文件 | 职责 |
| --- | --- |
| `sync.go` | 门面：远端布局说明、常量、`SyncStatus`、`SyncNow`/`TestSync`、定时循环 |
| `sync_dav.go` | 传输层：URL/鉴权/`GET`/`PUT`/`MKCOL`、设备 id 与包名 |
| `sync_dav_list.go` | 目录发现：`PROPFIND` 解析、列包、兄弟目录探测（WebDAV 方言与服务器怪癖都在这） |
| `sync_package.go` | 包格式：索引/记录结构、`buildPackage`/`parsePackage`、指纹、`buildOwnPackage`（全量条目 + 附件挑选入口） |
| `sync_blobplan.go` | 纯函数：附件按预算挑选与挂账轮转（`planBlobUpload`）；96 MiB 预算在单测里造不出来，用几十字节测 |
| `sync_run.go` | 一次同步的编排：列包 → 并行拉取 → 合并 → 采纳设置 → 上传 → 状态 |
| `sync_merge.go` | 纯规则：可同步字段裁剪（`machineOnlyFields`）、墓碑合并 |

### 片段与平台层

| 文件 | 职责 |
| --- | --- |
| `clipboard_rich.go` | 图文混排回填的纯变换：`memoimg:` 令牌 → data URI、HTML → RTF（内嵌 `\pict`）、令牌剥离。不碰剪贴板 API，任何平台都能单测 |
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
| `backdrop_windows.go` | 快速输入窗背后的 DWM Acrylic 层（`SetWindowBackdrop`，半透明卡片必要的前置） |
| `shell_windows.go` | `ShellExecute` / 资源管理器定位 |
| `platform_other.go` | 非 Windows 桩实现（保持可编译） |
| `snippet_exec_windows.go` | 片段动作的 Windows 实现（粘贴 / 启动程序 / 打开网址） |
| `update.go` | 自动更新：GitHub Release 检查、下载、静默安装 |

**三条铁律**：

1. `*_windows.go` 不许 import 业务概念；业务层不许直接调 Win32。
2. `internal/clip` **不许 import 宿主框架**（换壳前是 Wails，现在是 MyGo）。宿主只出现在
   `main.go`/`wire.go`/`tray.go`/`events.go`/`bindings.go`；业务层通过 `clip.Hooks` 反向调用宿主。
3. 前端的方法表是**快照**：`scripts/mygo-bindings/clipservice.signatures.txt` 决定
   `frontend/src/lib/clipservice.gen.ts` 生成什么。新增/删除绑定方法要同时改快照并重跑
   `scripts/gen-mygo-clipservice.ps1`；**多返回值方法**（`CaptureSize`/`ShotBounds`/
   `OcrEngineStatus`/`Match`/`Render`）必须在生成器的 tuple 表里登记，否则前端解构会报
   `TypeError: (intermediate value) is not iterable`（2.12.0 就踩过这个）。

## 二、前端（React + TS）

```
src/App.tsx              薄壳：装 hooks、切视图（含"关设置回原处"）、转发通知（不写业务）
src/hooks/
  useToast.ts            全局通知
  useTheme.ts            主题落地的唯一入口：useTheme(settings) 给主窗口、useSettingsTheme() 给常驻弹窗
  useAppData.ts          设置 / 应用信息 / 同步状态 / 筛选器 / 计数
  useEntries.ts          条目列表数据层：分页、上滑加载更早、按天分组、贴底滚动
  useEntryImages.ts      正文 memoimg: 引用的按需加载缓存
  useComposerDraft.ts    草稿：内存 → 防抖落库（400ms）→ 重开读回。`useDraft(kind)` 一份逻辑，
                         两个使用者（主窗口 composer / 「记一笔」capture），两把 meta 键
  useFilters.ts          筛选器增删改（⋯ 菜单 + 新建/编辑弹窗的状态机）
src/lib/
  api.ts                 绑定封装 + 类型别名
  images.ts              正文图片引用的解析与收集（纯函数）
  themes.ts              配色预设、对比度自检、自定义 CSS 净化
  dates.ts               日期键唯一写法："2026-11-19" / "2026-11"（日历、今天、EntryQuery.day）
  format.ts              时间/体积格式化
  window.ts              无边框窗口控制
  tagSuggest.ts          `#` 标签联想的纯逻辑（token 识别、候选过滤、插入）
  tagHighlight.ts        用 CSS Highlight API 给编辑器里的 `#标签` 上色（不插 DOM）
  privacyMask.ts         隐私模式：只留光标所在段落可读，其余用 Highlight API 遮成极淡
  privacyMode.ts         隐私模式开关状态（存 Settings.PrivacyInput，三处输入框共用）
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
  HotkeyInput / Lightbox       小部件
  Picker / Capture / Shot     ※ 三个**窗口级**视图（由 main.tsx 按 ?mode= 挂载）：
                          Picker 是光标处快速粘贴列表，Capture 是"记一条"浮层，
                          Shot 是框选截图的遮罩层（冻结的那一帧由 asset handler
                          `/shot/current` 直出 PNG，裁切用 canvas 按 naturalWidth/clientWidth 换算）
  PrivacyToggle          隐私模式的小眼睛开关（详情 / 记一条 / 快速输入窗各一个）
  ContextMenu            通用右键菜单（自绘，不用系统菜单）：贴边翻转、Esc/点外面关、↑↓ 选
                        ※ 菜单**条目**不在这里，规则在 lib/entryMenu.ts（纯函数，可独立断言）
src/styles/vditor-theme.css  Vditor 的 token 映射 + 三条硬编码浅色补丁（深色主题必需）
src/styles/
  fonts → base → layout → entries → controls → settings → picker → snippets → timeline → editor → vditor-theme → detail → todo → filter → shot
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
  改完跑 `scratch/layout-preview/snap.mjs` 对比截图（`scratch/layout-preview/measure-cal.mjs` 量日历列宽）。
- **字体分两套**：`--font-ui` = Inter + Noto Sans SC（比例宽度，长中文段落好读）；
  `--font-mono` = Maple Mono NF CN（等宽，`styles/fonts.css` 里只有一个 400 字面）。
  等宽字体铺满界面会把中文撑成 1.2em 宽、行拉长，所以别再把它塞进 `--font-ui`。
  改字体后跑 `scripts/visual-check/check-css.ps1`（它会把 dist 里的真字体一起加载，
  量出来的折叠高度才和 app 一致）。
- 折叠卡片的"能不能展开"是运行时量出来的（`EntryCard` 里 `scrollHeight - clientHeight > 8`），
  不是按内容类型猜的：短卡片不该出现底部遮罩，也不该冒出「展开全文」。

## 三、数据模型（唯一真相）

```
entries        条目：标题（可选）+ 内容 + 标签 + actions（clipboard / snippet）
               + 待办派生列（todo_state / todo_done_at）
               + 片段的执行类型（snippet_kind：text/password/image/file/url/app/command/html）
entry_blobs    附件：内容寻址，≤512KB 进库、更大落盘 entry_files/（删条目后回收孤儿）
entries_fts    全文索引（trigram，中文子串可搜；标题也进索引）
entry_vectors  语义向量（id / hash / state / 512 字节 int8）——**本机派生物，不进同步包**
entry_filters  用户保存的筛选器（含规则树 JSON）
tombstones     删除墓碑（同步用）
meta           键值对（设备 id、一次性标记、filters.hidden、composer.draft、capture.draft、capture.size）
```

**写入有事务边界**：`SaveEntry` / `mergeEntry` 把「条目行 + FTS 行」放在同一个事务里（索引写不进去就整条回滚，
不会留下"存住了但搜不到"的条目）；`DeleteEntry` 在一个事务里做「删行 → 删索引 → 写墓碑」，顺序反了会留下
"本机还在、对端已删"的分叉；清空历史是单事务 + 一次附件回收。

**草稿不是条目**：输入框里敲了一半的东西存在 `meta.composer.draft`（主窗口「记一条」）与
`meta.capture.draft`（「记一笔」浮层）——都只有一条键值、只在本机，
不进 `entries`、不进同步包、也不算内容哈希的一部分——半成品不该被当成历史，也不该同步到别的机器。
`Set*Draft("")` 即清空，所以"存成条目"之后不会在重启时又被塞回输入框。

**为什么「记一笔」的草稿非落库不可**：那个窗口闲置会被销毁（`idle_windows.go`，默认 1.5 秒），
销毁 == 页面连同里面的 React state 一起消失。状态只要留在前端，就等于"关一次窗丢一次稿"
（2.12.5 及以前就是这样，用户报了上来）。凡是可能被闲置销毁的窗口，凡是用户敲了一半的东西，
都必须活在后端。

**待办是派生的，不是第二张表**：正文里的 `- [ ]` 就是真源，
`todo_state`（`''`/`open`/`done`）与 `todo_done_at` 是 `SaveEntry` / `mergeEntry` 里现算的派生列
（`todo.go` 的 `deriveTodo`）。勾选改的是正文那一个字符，`hash` 不变，所以同步配对不受影响。
派生列**不跨端传**：`mergeEntry` 一律本端重算，否则版本混跑时旧客户端会把状态抹掉。

`entries.title` 现在由片段写入（`SaveSnippet` 存用户起的标题；读的时候没标题才退回正文首行）。
空标题的条目在卡片上不多一行。
表结构是 `CREATE TABLE IF NOT EXISTS`，**新增列必须同时加 `ensureColumn` 迁移**（见 `store.go`），
否则老库不会自动长出新列（`title` / `todo_state` / `todo_done_at` / `snippet_kind` 都是这么加的）。

**片段的执行类型单独一列**：`kind` 回答"正文是什么"（text/link/image/files/rich/custom），
`snippet_kind` 回答"回车时做什么"（粘贴/打开/启动/执行）。早期把 `password`/`app`/`command` 直接写进
`kind`，结果类型筛选里多了几个不存在的正文类型，标题也存不下来。旧数据不用迁：读的时候认 `kind` 里的旧值。

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

## 五、宿主契约（MyGo）

窗口/托盘/热键/事件全由 `github.com/egoist/mygo` 提供。它和换壳前的 Wails 有 6 处语义差异，
每一条都是实测踩出来的，改 `wire.go`/`tray.go` 之前先看完：

| 差异 | 表现 | 我们怎么处理 |
| --- | --- | --- |
| **页面 `<title>` 覆盖窗口标题** | 三个弹窗共用一个 SPA，标题全变成「ClipNest · 剪贴板」，任务栏/e2e 分不清 | `retitle()`：建窗后与每次 show 后各设一次（300ms / 1200ms） |
| **没有 `WindowEndResize`** | 只有连续的 `OnResize`，拉伸结束存不了尺寸 | `OnResize` + 350ms 去抖 → `SaveCaptureSize` |
| **双击拖动区 = 系统"标题栏双击"** | 弹窗会被最大化 | 三个弹窗 `DisableMaximize: true` |
| **`Focus()` 只是请求焦点** | 前台是别的程序时请求被丢掉（单实例唤回、托盘唤回都会踩） | `showWindow` 里补 `clip.ActivateWindow(NativeHandle)` |
| **绑定只接受 `(T, error)`** | `(A,B,C)` 多返回值、`func` 参数都会 panic 在绑定阶段 | `bindings.go` 的 `clipAPI` 外观 + 前端 tuple 还原 |
| **`DisableMove: true` 会让 `SetPosition` 静默失效** | 窗口卡在 (0,0) | 永远传 `DisableMove: false`，靠 `--app-region: drag` 拖动 |

另外两条与原生 UI 无关但同源的坑（`ui` 包，Gate 3 若做自绘会遇到）：`Box` 的子元素是**顺序排布**
（第二个 `.Fill()` 拿到 0 高），`DrawOver` 是独立图层（图像和蒙版必须在同一个 `root.Draw` 里画）。

**调试口**（生产默认关闭，靠环境变量开）：

| 变量 | 作用 |
| --- | --- |
| `CLIPNEST_CAPTURE_PAGE=<png>` | 用 `win.CapturePage()` 抓一帧。无边框窗抢不到前台时，外部截屏拍到的是别的程序 |
| `CLIPNEST_EVAL_JS=<js>` | 启动 3s 后在主窗执行 JS，结果写进应用日志（`_JS2` 是 9s 后的第二段，用来验"先订阅、再从外面触发"的时序） |
| `CLIPNEST_LOG_TRAY=1` | 打印托盘图标 Bounds（自动化没法"看见"托盘） |
| `CLIPNEST_IDLE_DESTROY=0` / `CLIPNEST_IDLE_MS` | 关掉/调整闲置销毁（e2e 脚本必须关，否则窗口会被收掉） |
## 六、怎么验证

```powershell
go build -tags production -ldflags="-w -s -H windowsgui" -o bin\clipnest.exe .   # 生产构建（内嵌前端产物）
pwsh -File scripts\gen-mygo-clipservice.ps1   # 改了绑定方法/返回值：重生成前端 call 适配层
pwsh -File scripts\package-mygo.ps1 -Version <版本>   # 本地打安装包（go-winres 资源 + NSIS + 签名）
pwsh -STA -File scratch\e2e-picker-single.ps1  # 快速粘贴：贴插入符 + 回车直粘 + 单实例 + 托盘 boundsgo build -o bin\clipnest-dev.exe .        # 开发构建（同样的代码，文件名区分开）
go test ./internal/...                    # 存储/查询/待办/筛选规则/同步/展开引擎/附件回收（audit_fix_test.go）
cd frontend; npx tsc --noEmit; npx vite build --mode production
pwsh -File scripts\check-platforms.ps1    # 多平台门禁：linux/darwin/windows-arm64 上 internal/... 能编译
node --experimental-strip-types scratch/tag-suggest-check.mjs   # 标签联想：`#` 识别 / 候选过滤（纯逻辑，不需要浏览器）
node --experimental-strip-types scratch/privacy-mask-check.mjs   # 隐私模式：哪些段落要遮（纯逻辑，不需要浏览器）
$env:CLIPNEST_OCR_LIVE=1; go test ./internal/clip -run TestSystemOCRLive -v   # 真引擎 OCR 体检（要 Windows + 语言包）
go run ./scratch/ocrdemo <数据目录> <png>   # OCR 全链路演示：入库 → 识别 → 搜索命中
node --experimental-strip-types scratch/entry-menu-check.mjs   # 右键菜单条目规则（纯逻辑，9 项断言）
go test ./internal/ppocr                                    # PP-OCR 纯逻辑（CTC/成框/缩放，不需要模型）
$env:CLIPNEST_PPOCR_LIVE=1; $env:CLIPNEST_PPOCR_DIR=scratch\ppocr-assets\engine; go test ./internal/ppocr -run TestLiveRecognize -v   # 真引擎
go run ./scratch/ocrcmp <数据目录> <png>                   # 两个引擎并排比（沙箱数据目录里放 ocr/ 资源即启用 ppocr）
go run ./scratch/ocrdownload <数据目录>                    # 一键下载全链路（下载→校验→解压→引擎能起；要 HTTPS_PROXY）
pwsh -File scripts\visual-check\check-css.ps1   # 样式回归截图（改 CSS 后跑）
pwsh -STA -File scratch\e2e-capture-resize.ps1  # 记一笔浮层：拖动/拉伸/尺寸记忆（真鼠标事件）
pwsh -File scratch\e2e-shot.ps1                  # 截图：热键 → 遮罩铺满显示器 → 拖动 → 条目尺寸与拖动一致 → 剪贴板有图 → Esc 不多出条目
pwsh -File scratch\e2e-webimg.ps1                # 网页「复制图片」：真 Chrome 复制 → 存成图片条目；服务器停掉 → 退回富文本
go run ./scratch/clipfmt                        # 剪贴板取证：格式名 + 文本/HTML/DIB 头（查"某类内容存错了"先跑它）
pwsh -File scratch\shot-settings-check.ps1       # 设置页新增几行的外观：截图快捷键 / 截图后复制到剪贴板 / 网页图片自动下载
pwsh -File scratch\e2e-capture-transparency.ps1 # 记一笔浮层：圆角真透明 + 卡片半透明（按像素量；浅色主题要加 -Preset default-light -Backdrop '#1e6fd9'）
pwsh -STA -File scratch\check-theme-sync.ps1          # 两个弹窗真的收到 settings:changed（托盘「暂停收集」触发一次）
pwsh -STA -File scratch\check-sidebar-active.ps1 # 侧栏高亮：待办 ⇄ 内容不同时点亮两行（真鼠标点击）
pwsh -STA -File scratch\shot-tray-menu.ps1      # 托盘菜单截图 + 点「打开设置」验证跳转
pwsh -STA -File scratch\e2e-live-and-draft.ps1  # 复制后不切页面就能看到新条目 + 草稿跨重启
pwsh -STA -File scratch\e2e-draft-robust.ps1    # 敲完立刻收起窗口（点 ×）再强杀，草稿不丢
pwsh -STA -File scratch\e2e-global-composer.ps1 # 常驻「记一条」：草稿跨视图/跨重启 + 设置自动保存（真鼠标）
pwsh -STA -File scratch\e2e-capture-draft.ps1  # 「记一笔」草稿：打字 → Esc → 闲置销毁 → 重开，内容仍在（真键盘）
pwsh -STA -File scratch\e2e-embed-search.ps1   # 语义搜索：Ctrl+F 搜一句零共同词的话 → 截图；再看设置里的「搜索」分区
pwsh -STA -File scratch\e2e-theme-shots.ps1     # 4 个主题预设各截一张整窗
pwsh -STA -File scratch\smoke-2.8.3.ps1        # 冒烟：沙箱 APPDATA 起一次、截图、扫日志里的 error/warn
node scratch\theme-audit.mjs                    # 主题对比度体检（5 预设 × 4 底色 × 7 文字）
```

上面这些 `-STA` 脚本都会**先停掉正在跑的安装版**（否则 SingleInstance 会让沙箱实例直接
退出），收尾再把它拉起来；数据走沙箱 `%APPDATA%`，不碰真实库。

`go vet ./internal/...` 现在是**零告警**：`win32_windows.go` 的 `ptrFromUintptr` 与
`expand/wininput_windows.go` 的 `hookParamPtr` 两个 shim 已经把 `unsafe.Pointer`
指针算术的误报绕开了（早先那 4 条告警已经不存在，别再照着忽略）。

## 七、后续可以继续拆的地方

1. ~~`sync_run.go` 的 `syncOnce`（300+ 行单函数）拆成 pull / merge / push~~ —— 2026-11-19 已拆：
   `syncRun` + listPeers / pull / merge / adoptSettings / push / report 六段（顺带修掉"每台设备计数虚高"）。
2. `store.go` + `entry_store.go` 抽成 `internal/store`，`Entry` 抽成 `internal/model`，
   `clip` 只留 service / sync / snippet（会改绑定路径，一次只动一层并装机验证）。
3. `*_windows.go` 抽成 `internal/winplat`（同上，注意绑定与构建 tag）。**部分已做**：
   `internal/winplat` 已经在给输入注入用，且现在有非 Windows 桩；剩下的剪贴板/截图/窗口几何
   仍在 `clip` 包内（它们要用 `Captured`/`ShotFrame` 这些业务类型，搬包等于先搬模型）。
4. `Composer.tsx` / `EntryDetailView.tsx` / `Capture.tsx` 已经共用 `MarkdownSurface`（`Picker.tsx` 只是列表，不含编辑器），可以再抽一层 Composer 外壳。
5. ~~`store.go`（607 行）再切一刀~~ —— 2026-11-24 已拆成 `store.go`(349) / `store_migrate.go`(209) /
   `util.go`(97) / `log.go`(58)；同轮 `sync_dav.go`(255)+`sync_dav_list.go`(232)、
   `entry_api.go`(241)+`filter_api.go`(208)。剩下这几个还在 350 行以上，切法按"纯逻辑 vs 编排"：
   `sync_run.go`(489)、`entry.go`(417)、`expand.go`(411)、`snippet.go`(409)、`entry_query.go`(398)、
   `ppocr.go`(398)、`entry_store.go`(383)、`update.go`(376)、`clipboard_rich.go`(372)。

**单文件行数现状**（>350 行的都是待拆候选，别在新代码里继续长）：

```powershell
Get-ChildItem -Recurse -File -Filter *.go |
  Where-Object { $_.FullName -notmatch '\\(bin|scratch)\\' } |
  ForEach-Object { [pscustomobject]@{ L = (Get-Content $_ | Measure-Object -Line).Lines; P = $_.FullName } } |
  Where-Object L -gt 350 | Sort-Object L -Descending
```

## 八、平台契约（多平台）

现状：**只有 Windows 有完整实现**；macOS / Linux **能编译、能跑单测、平台能力是空实现**。
这一节是加新平台时的唯一入口——好消息是：**改动都落在 `*_other.go` 那一侧，业务层一行不用动**。

### 铁律

1. **纯逻辑不许住在 `*_windows.go` 里**。哈希、预览、格式化、字节解析这些"跟平台无关但
   恰好先给 Windows 用"的函数，必须放中立文件（`capture_text.go` 就是为此存在的）。
   违反的代价很直接：整个 `clip` 包在非 Windows 上编不过。
2. **新增平台文件，不新增平台分支**。别在业务代码里写 `if runtime.GOOS == ...`；
   要加能力就在 `*_other.go` 里补同签名实现，或新开一个 `*_darwin.go`。
3. **加了平台函数就要跑门禁**：`pwsh -File scripts\check-platforms.ps1`（见下）。

### 能力矩阵

`桩` = 只有空实现（返回 `ErrNotSupported` 或零值）；`—` = 还没定义任何契约。

| 能力 | 中立契约（谁声明） | Windows 实现 | mac / linux |
| --- | --- | --- | --- |
| 剪贴板监听 | `StartWatcher` / `Watcher.Changes` / `Stop`（`platform_other.go`） | `watcher_windows.go`：消息窗 + `WM_CLIPBOARDUPDATE` | 桩 |
| 剪贴板读取 | `captureClipboard` → `*Captured` | `clipboard_windows.go` + `clipboard_formats_windows.go`（格式知识）+ `dib_windows.go` + `ole_windows.go` | 桩 |
| 剪贴板写回 | `WriteText/HTML/RTF/Image/Files/Raw/RichToClipboard` | `clipboard_write_windows.go` | 桩 |
| 回声抑制哈希 | `entryEchoHash`（`platform_other.go`） | 同左，复用 `capture_text.go` 的四个 hash | 桩（返回 `""` ⇒ 抑制失效，见 HISTORY） |
| 输入注入 | `winplat.SendUnicode/Backspaces/LeftArrows/TapKey/Chord/Paste/ForegroundWindow` | `winplat/input_windows.go`（`SendInput`） | `winplat/input_other.go` 桩 |
| 缩写展开钩子 | `install/uninstall/placeCursor/vkToRune/imeBusy/foregroundWindow/sendUnicodeText/sendBackspaces/sendLeftArrows`（`expand`） | `expand/wininput_windows.go`（低级键盘钩子 + IME 状态） | `expand/wininput_other.go` 桩 |
| 快速粘贴窗几何 | `ForegroundWindowHandle` / `CaretAnchor` / `ClampToWorkArea` / `ActivateWindow` / `SendPaste` / `PasteInto` | `picker_windows.go` + `clipboard_windows.go` | 桩 |
| 截图 | `CaptureCursorMonitor` → `*ShotFrame` / `PlaceOverlayWindow` / `HideOverlayWindow` / `FocusOverlayWindow` | `shot_windows.go`（GDI `BitBlt`） | 桩 |
| 热键撞车预检 | `HotkeyTaken`（`hotkey.go`，导出给 `wire.go`） | `hotkey_windows.go`（`RegisterHotKey` 试注册） | `hotkey_other.go` 恒 `false` |
| 窗口背景 | `SetWindowBackdrop`（`backdrop_*.go`） | DWM Acrylic | 恒 `false`（调用方退回不透明） |
| 开机自启 | `setAutoStart` / `autoStartEnabled` / `autoStartCommand` / `autoStartCommandValue` | `win32_windows.go`（HKCU Run） | 桩 |
| 外壳打开 | `shellOpen` / `OpenExternal` / `revealInExplorer` | `shell_windows.go`（`ShellExecute`） | 桩 |
| 片段动作 | `launchProgram` / `runCommand` / `ProcessImageName` / `sendLeftArrows` | `snippet_exec_windows.go`、`picker_windows.go` | 桩 |
| 来源程序名 | `foregroundAppName` | `win32_windows.go` | 桩（空串） |
| OCR 引擎 | `ocr.go` 的引擎选择（`system` ↔ `ppocr`） | `ocr.go` 调 `Windows.Media.Ocr`（PowerShell `-EncodedCommand`）；`internal/ppocr` 走 cgo | `ppocr` 编成 stub；`system` 引擎跑不起来 ⇒ 设置页应显示"不可用" |
| 托盘 / 窗口 / 事件 | MyGo（`github.com/egoist/mygo`） | `tray.go` / `wire.go` / `events.go` | MyGo 本身支持，但上面那批能力是桩 |
| 自动更新 | `update.go`（GitHub Releases + ed25519 验签） | 检查 + 下载 + 静默安装（NSIS 包） | 检查逻辑可复用，安装路径要换 |

### 门禁

```powershell
pwsh -File scripts\check-platforms.ps1                  # linux/amd64 + darwin/arm64 + windows/arm64
pwsh -File scripts\check-platforms.ps1 -Targets linux/amd64
```

它只做一件事：对这几个 GOOS 跑 `go vet ./internal/clip ./internal/expand ./internal/winplat ./internal/ppocr`
（`CGO_ENABLED=0`）。**过了不等于新平台能用**，但过了意味着"没有代码是 Windows 专属"——
这是加平台前唯一必须满足的硬条件。

> 整包 `go build` 交叉编译不在此列：宿主的 linux/darwin 后端要 cgo 与各平台工具链，
> 那属于真机 CI。/cmd 层（`main.go`/`wire.go`/`tray.go`）目前也仍按 Windows 假设写。






