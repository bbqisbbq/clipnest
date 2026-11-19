# 架构与分层

> 目标：一处逻辑只存在一份，改一个功能不需要翻五个文件。
> **每次重构后更新此文档**（文件表就是"去哪找"的唯一答案）。

## 一、后端（Go）

```
main.go                  Wails 装配：窗口、托盘、快捷键、钩子注入、单实例
internal/expand/         缩写展开引擎（零业务知识，只认 Host 接口）
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
| `entry_blob.go` | 附件字节：`SaveEntryBlob` / `LoadEntryBlob`（≤512KB 进库，更大的落盘） |
| `entry_image.go` | 纯函数：解码、缩略、扩展名/MIME 推断（不含存储） |

### 服务与 API

| 文件 | 职责 |
| --- | --- |
| `service.go` | `ClipService` 装配、`Start`/`Shutdown`、窗口/版本/主题、`GetStats` |
| `service_capture.go` | 采集管线（合并 45ms、去重、回写抑制）+ 快速输入窗 |
| `service_paste.go` | 条目 → 系统：复制回剪贴板、打开、picker 弹窗三件套 |
| `service_settings.go` | 设置保存与副作用（自启、展开器、两个后台循环） |
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

**两条铁律**：

1. `*_windows.go` 不许 import 业务概念；业务层不许直接调 Win32。
2. package 归属会影响 Wails 绑定路径（`frontend/bindings/...`）。**拆文件是安全的，挪包不是**：
   要把 `Entry` 或 `Store` 挪去新包，必须同时重新生成绑定并装机验证。

## 二、前端（React + TS）

```
src/main.tsx             入口：模式分流（主窗 / picker / capture）+ 样式顺序 + 主题预应用
src/App.tsx              薄壳：装 hooks、切视图、转发通知（不写业务）
src/hooks/
  useToast.ts            全局通知
  useTheme.ts            主题落地（预设 + 自定义 CSS + 原生边框）
  useAppData.ts          设置 / 应用信息 / 同步状态 / 筛选器 / 计数
  useEntries.ts          条目列表数据层：分页、上滑加载更早、按天分组、贴底滚动
  useEntryImages.ts      正文 memoimg: 引用的按需加载缓存
src/lib/
  api.ts                 绑定封装 + 类型别名
  images.ts              正文图片引用的解析与收集（纯函数）
  themes.ts              配色预设、对比度自检、自定义 CSS 净化
  format.ts              时间/体积格式化
  window.ts              无边框窗口控制
src/components/
  TitleBar / Sidebar / Calendar / Tip / Icons      布局与小组件
  SettingsView          设置页编排者（草稿、脏标记、保存条）
  settings/             七个分区：Capture / Interaction / Sync / Snippets / Appearance / Update / About + ui.tsx
  EntriesView           统一内容视图（搜索、写新条目、卡片操作编排）
  entries/EntryCard     单条卡片：折叠、图片点击、操作按钮
  EntryEditor           全屏条目编辑器；Composer 是底部"记一条"输入卡
  MarkdownEditor        CodeMirror 6 封装；SnippetFields 是片段的额外编辑项
  HotkeyInput / Lightbox / Picker / Capture
src/styles/
  base → layout → entries → controls → settings → picker → snippets → timeline → editor
src/theme-light.css      主题引擎管不到的浅色补丁（--k-* / --shadow-pop / 原生控件）
```

**三条铁律**：

1. `components/` 只管渲染与交互编排，数据靠 props / hook；不直接算业务。
2. `lib/` 里不许有 React（`hooks/` 才可以）。
3. **CSS 的 import 顺序就是层叠顺序**：同优先级的选择器靠后覆盖，新增样式文件要插在
   正确的段位里，不能随手追加到末尾（`main.tsx` 里有注释说明）。

## 三、数据模型（唯一真相）

```
entries        条目：内容 + 标签 + actions（clipboard / snippet）
entry_blobs    附件：内容寻址，≤512KB 进库、更大落盘 memo_files/
entries_fts    全文索引（trigram，中文子串可搜）
entry_filters  用户保存的筛选器
tombstones     删除墓碑（同步用）
meta           键值对（设备 id、一次性标记）
```

`items` / `memos` / `snippets` 三张表已在 2.0.0 删除：它们表达的是同一种东西的
三种形态，现在统一为 `entries` + `actions`。片段 = 带 `snippet` 能力的条目。

**不自动清理**：没有最大条数、没有保留天数，条目只在用户手动删除/归档时消失
（2.1.0 的产品决定）。`Entry.ExpiresAt` 保留只为兼容旧数据与对端同步包。

## 四、同步

- 一台机器一个包：`clipnest_data/<deviceId>-<host>.zip`
- 包内：`package.json`（索引 + 墓碑 + 设置）+ `entries/<hash>.json` + `blobs/<name>`
- 合并：按内容 hash 配对；`updated_at` 新的赢；`pinned` 取或；删除走墓碑
- 合并必须用 `Store.mergeEntry`（保留远端时间戳）。用 `SaveEntry` 会把
  `updated_at` 盖成"现在"，让本地副本比墓碑还新，删除会被静默忽略。

## 五、怎么验证

```powershell
go build -o bin\clipnest-dev.exe .        # 后端 + 内嵌前端产物
go test ./internal/...                    # 存储/查询/同步/展开引擎
cd frontend; npx tsc --noEmit; npx vite build --mode production
pwsh -File scripts\visual-check\check-css.ps1   # 样式回归截图（改 CSS 后跑）
```

`go vet ./internal/clip/` 目前对 `win32_windows.go` / `ole_windows.go` 的
`unsafe.Pointer` 指针算术报 4 条 "possible misuse"，属已知存量问题（原生内存代码，
修它要单独验证），不是回归信号。

## 六、后续可以继续拆的地方

1. `sync_run.go` 的 `syncOnce`（300+ 行单函数）可以拆成 pull / merge / push 三段，
   但需要引入中间结构，属于重构而非搬移。
2. `store.go` + `entry_store.go` 抽成 `internal/store`，`Entry` 抽成 `internal/model`，
   `clip` 只留 service / sync / snippet（会改绑定路径，一次只动一层并装机验证）。
3. `*_windows.go` 抽成 `internal/winplat`（同上，注意绑定与构建 tag）。
4. `Picker.tsx` 与 `Capture.tsx` 已经共用 `MarkdownEditor`，可以再共用一层 Composer。
