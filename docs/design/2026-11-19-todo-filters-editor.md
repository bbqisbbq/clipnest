# 设计：Todo 收集 / 筛选器重构 / 编辑框重构

> 状态：待评审（2026-11-19）。三块可独立验收，批 1 → 批 2 → 批 3。
> 已拍板：**todo 按清单项（解析正文 `- [ ]`）**、**编辑器只保留一个：Vditor IR（全量拷贝 dist），
> 按场景精简界面，CodeMirror 移除**、**「全部内容」保留为不可删的根、「置顶」改为可删**、
> **完成状态以字段属性为主、tag 作为默认关闭的设置项**、**先出文档再写代码**。

---

## 零、共同前提（读这三块之前先认这几条）

1. **查询只有一套**：`EntryQuery.where()`（`entry_query.go`）是唯一 SQL 生成处，
   `ListEntries` 与 `CountByDay`（日历圆点）都用它。任何新筛选维度只能加在 `where()` 里，
   不许在别处另写 SQL —— 这是日历曾经和列表对不上的根因。
2. **`entries.hash` 不是内容校验**，只是同步配对键：`SaveEntry` 只在 `Hash == ""` 时重算，
   所以**改正文不会换 hash**，改 `- [ ]` 为 `- [x]` 不会让对端多出一条。
   （这一条决定了下面 todo 可以直接拿正文当真源。）
3. **tag 是纯用户语义**（`entry.go` 顶部注释：「系统不往里塞东西」，`auto_tags` 因此被删）。
   任何「系统想标记的状态」都走**字段**，不走 tag。
4. 派生规则要和 `ExtractTags` 同构：**从正文解析 → 物化到列 → SQL 精确过滤**，
   而不是每次查询都 `content LIKE`。

---

## 一、Todo（清单项级）

### 1.1 目标 / 非目标

**目标**
- 任何条目正文里写的 GFM 任务列表（`- [ ] 跑一遍发版检查`）都被动态收集，
  不需要给条目打任何标记、不需要走「创建待办」流程。
- 收集页可一键勾选完成 / 取消完成，**改的就是那条条目正文里的 `[ ]`**。
- 收集页每一行能跳到它所在的条目（右栏详情视图，见第三部分）。
- 完成状态沉淀到条目上的**属性**，让筛选器/统计能直接用。

**非目标**
- 不做提醒、到期时间、重复任务、子任务树。
- 不做「独立于正文的待办库」：正文就是真源，删掉那行任务它就消失。

### 1.2 数据模型

| 位置 | 内容 |
| --- | --- |
| `entries.content` | **真源**。GFM 任务列表行，勾选即把 `[ ]` 改写成 `[x]` |
| `entries.todo_state`（新列） | 派生：`''` 无任务 / `open` 有未完成 / `done` 全完成 |
| `entries.todo_done_at`（新列） | 派生：`todo_state` 变成 `done` 的时刻（`open`/`''` 时清空） |
| `Entry.TodoOpen` / `Entry.TodoTotal` | 派生计数（只用于展示「2/5」），不落库 |

为什么派生列而不是新表或每次现算：

- 每次现算要扫全库正文并解析，日历 `CountByDay` 会被拖慢，且 SQL 里没法精确过滤；
- 新表要自己处理同步、墓碑、并发，而正文已经是真源，表只是缓存 —— 不值。
- 与 `tags` 完全同构（`ExtractTags` 派生 → `TagSpec` 落列 → `tags LIKE` 过滤），团队不用学新东西。

### 1.3 解析器（新文件 `internal/clip/todo.go`）

```go
// TodoItem 是正文里的一行任务。Key 在文本不变时稳定，用于勾选时定位。
type TodoItem struct {
    Key    string `json:"key"`    // sha1(entryID + "\x00" + 归一化文本 + "\x00" + 重复序号)[:12]
    Text   string `json:"text"`   // 去掉 "- [ ] " 前缀与首尾空白的正文
    Done   bool   `json:"done"`
    Line   int    `json:"line"`   // 0-based 行号，勾选时按行改写（比按 key 搜更快也更准）
    Indent int    `json:"indent"` // 缩进层级，收集页按此缩进显示
}

func ParseTodos(content string) []TodoItem
func (s *Store) SetTodoDone(entryID, key string, done bool) (Entry, error)
func (s *Store) ListTodos(q EntryQuery) []TodoRef
```

规则：
- 匹配 `^\s*([-*+]|\d+[.)])\s+\[([ xX])\]\s+(.*)$`；`[X]` 与 `[x]` 都算完成。
- **跳过围栏代码块**（``` / ~~~）内的行 —— 代码示例里的 `- [ ]` 不是任务。
- 缩进用空格数 / 2 取整，最多 3 层。
- `Key` 由「文本」决定而不是行号：在任务上方加一行文字不会让勾选错位；
  同一条目里文本完全相同的第 2、3 个任务靠 `重复序号` 区分。
- 遍历条目时 `text` 归一化（折叠空白）后再参与 hash，纯空白改动不算改文本。

`SetTodoDone` 的流程（**这是唯一写正文的入口**）：
1. `GetEntry(id)` 取整条（绝不接受前端传回的 Entry —— 那会静默重置 tags/trigger）；
2. 按 `Line` 定位，只把这一个 `[ ]`/`[x]` 字符改掉，其余字节原样保留；
3. `SaveEntry(e)`（保持 `hash` 不变，`updated_at` 刷新）；
4. 派生列自动重算（见 1.4），返回新 Entry。

### 1.4 派生列在哪里算

在 `SaveEntry` 与 `mergeEntry` 里，紧跟 `ExtractTags` 之后：

```go
e.TodoItems = ParseTodos(e.Content)          // 只在内存里，给返回值用
e.TodoState = todoStateOf(e.TodoItems)       // "" | open | done
if e.TodoState == "done" && e.TodoDoneAt == nil { e.TodoDoneAt = &now }
if e.TodoState != "done" { e.TodoDoneAt = nil }
```

**`mergeEntry` 必须重算，不能信远端传来的派生列**：版本混跑时，旧版客户端上传的条目
没有 `todo_state`，直接覆盖会让本端状态凭空消失。派生列一律本端重算 —— 派生数据不跨端传。

→ 同步包格式不用改（`Entry` 的 JSON 多两个字段，老端忽略未知字段即安全）。

### 1.5 完成之后「加属性 / tag」

- **属性（默认，已实现）**：`todo_state = done` + `todo_done_at`。筛选器可直接筛
  「所有任务都做完的条目」，统计里出现「未完成待办 N 条」。
- **tag（可选，默认关）**：设置页加一项 `todoDoneTag`（默认空）。填了就表示：
  条目变成 `done` 时自动补一个 `#<该 tag>` 到正文末尾，变回 `open` 时自动移除。
  这是**用户显式选择的**行为，不算系统偷偷塞 tag；不填就完全不碰正文。
- 收集页展示用「已完成」分区 + 删除线，不依赖 tag。

### 1.6 接口与统计

| 接口 | 用途 |
| --- | --- |
| `GetTodos(q EntryQuery) []TodoRef` | 收集页数据：`{entryId, entryTitle, entryPreview, entrySource, entryCreatedAt, item TodoItem}` |
| `SetTodoDone(entryID, key, done)` | 勾选 |
| `GetEntryStats()` 增 `todoOpen` / `todoDone` | 侧栏「待办」入口的角标 |
| `EntryQuery` 增 `Todo string`（`open`/`done`） | 让自定义筛选器也能筛 todo |

`GetTodos` 实现：先用 `where()` 粗筛（`todo_state <> ''`），再在 Go 里 `ParseTodos` 展开；
按 `todo_state` 排序（未完成在前），再按条目 `created_at` 倒序。量级是几千条 × 正则一遍，
几十毫秒，必要时加 3 秒内存缓存（key: 查询 + `updated_at` 最大值）。

### 1.7 收集页（`components/TodoView.tsx`）

- 侧栏「内容」区**最上方**加一个独立入口「待办」（角标 = 未完成数），与筛选器列表用分隔线隔开。
  它不是筛选器（不能改名/删除），因为它的数据形状是清单项而不是条目。
- 页面结构：
  - `未完成 (N)` 分区 → 每行：`[ ] 文本` + 右侧灰字「来自 *条目预览*（时间）」
  - `已完成 (M)` 分区默认折叠，行首勾选框已打勾、文本删除线
  - 顶部一句提示：怎么写出待办 ——「在任意条目里写 `- [ ] 一件事`，就会出现在这里」
  - 空态：直接给一个「记一条带待办的备忘」按钮，跳到内容页并预填 `- [ ] `
- 交互：
  - **勾选**：乐观更新 → 调 `SetTodoDone` → 失败回滚 + toast；成功后 toast 带「撤销」。
  - **点行文本**：进右栏详情视图并滚动定位到该行（见第三部分）。
  - 不做拖拽排序（正文顺序就是顺序）。

### 1.8 卡片上的呼应

`EntryCard` 在 `todo_state = open` 时，meta 行显示 `2/5 待办`；点它 → 打开详情视图。
不在卡片上做勾选（卡片是折叠的，勾选要看到上下文）。

---

## 二、筛选器重构

### 2.1 目标

1. 双击筛选器 → **主题化模态弹窗**（不再是 `window.prompt`），能改名、换图标、**编辑规则**。
2. 支持自定义筛选器：基于 tag / 属性做 `and` / `or` / `not` 组合。
3. 删掉能自定义重现的内置筛选器：`置顶`（`全部内容` 保留为不可删的根）。

### 2.2 规则模型（后端）

```go
// FilterGroup 是一棵规则树：Op 决定 children 怎么合。
// 只做一层嵌套就够用（组里放组），不做无限嵌套的 UI。
type FilterGroup struct {
    Op         string            `json:"op"`                   // and | or | not
    Conditions []FilterCondition `json:"conditions,omitempty"` // 叶子
    Groups     []FilterGroup     `json:"groups,omitempty"`      // 子组
}

type FilterCondition struct {
    Field string `json:"field"` // 白名单，见下表
    Op    string `json:"op"`    // 见下表
    Value string `json:"value"` // 多值用 \n 分隔
}

// EntryQuery 新增一个字段；空 = 保持原有扁平行为
Rule *FilterGroup `json:"rule,omitempty"`
```

**字段白名单与算子**（`where()` 里 `switch`，不拼用户输入的列名，天然防注入）：

| Field | 含义 | Op | 编译成 |
| --- | --- | --- | --- |
| `tag` | 标签 | `is` / `isNot` / `anyOf` | `tags LIKE '%,x,%'`（多值 OR） |
| `source` | 来源 | `is` / `isNot` | `source = ?` |
| `kind` | 内容类型 | `is` / `isNot` | `kind = ?` |
| `action` | 能力 | `has` / `hasNot` | `actions LIKE '%,x,%'` |
| `todo` | 待办状态 | `is`（`open`/`done`/`none`） | `todo_state = ?` |
| `pinned` / `archived` | 布尔 | `is` | `pinned = 1` 等 |
| `text` | 关键词 | `contains` / `notContains` | FTS（≥3 字）或 LIKE |
| `ageDays` | 多久以前 | `newerThan` / `olderThan` | `created_at >= / < 今天-N 天` |
| `day` | 某一天 | `is` | `created_at ∈ [d, d+1)` |

编译规则：
- 组 → `(` + children 用 `AND`/`OR` 连 + `)`；`not` → `NOT (...)`（组内默认按 `AND` 连）。
- 空组/空条件直接丢弃；**整棵树编译出 0 个谓词时视为无条件**（不会变成 `WHERE`）。
- 与现有扁平字段**可叠加**（扁平 AND rule），老筛选器不受影响。

### 2.3 内置筛选器的删除

```go
type EntryFilter struct {
    ID, Name, Icon string
    Query EntryQuery
    SortOrder int
    Builtin   bool // 代码内置：不可「改成另一套规则」
    Deletable bool // 允许从侧栏移除（置顶 = true，其余 false）
}
```

- `全部内容`（`Query{}`）：**不可删**。它是零条件根，也是自定义筛选器删光后的落地视图。
- `我写的` / `剪贴板` / `片段` / `图片` / `已归档`：不可删（对应的是系统能稳定识别的维度）。
- `置顶`：`Deletable`，用户删掉后写 meta `filters.hidden = ["pinned"]`，
  `GetFilters` 过滤掉；想找回来走「恢复默认筛选器」按钮（设置页或侧栏末尾菜单）。
- 内置筛选器被改名/换图标的覆盖行仍写在 `entry_filters`（现状保留）。

### 2.4 模态弹窗（`components/FilterDialog.tsx`）

复用已有 `.modal-backdrop` / `.modal` 的设计变量（`App.tsx` 的清空确认框在用），
新增 `filter-dialog` 类做宽版（720px）。内容：

```
┌ 编辑筛选器 ─────────────────────────────── ✕ ┐
│ 名称 [__________]   图标 [▦ 网格选择 14 个]   │
│ ─────────────────────────────────────────── │
│ 规则（满足全部/任一）  [全部 ▾]              │
│  ┌ 条件： [标签 ▾] [是 ▾] [________]  ✕      │
│  ┌ 条件： [待办 ▾] [是 ▾] [未完成 ▾]  ✕      │
│  ┌ 组：  [任一 ▾]   ( 条件… )  ✕             │
│  + 添加条件   + 添加组                       │
│ ─────────────────────────────────────────── │
│ 预览：命中 37 条                             │
│                        [取消]  [删除] [保存] │
└─────────────────────────────────────────────┘
```

- 图标选择：现有 11 个 `Icons.tsx` 图标 + 新增 `list`/`clock`/`tag`/`layers` 等几个，做成网格。
- 「预览命中 N 条」：新接口 `CountEntriesWith(q) int`，输入防抖 300ms（`SELECT COUNT(*)` + `where()`）。
- 底部 `[删除]` 只在 `Deletable` 时出现；删除走确认（复用 App 里的确认模态）。
- 规则编辑器**限制嵌套一层**（组内只能放条件，组与组之间不能再嵌），UI 才不会变成 bug 温床。

### 2.5 侧栏交互

- 双击 → 打开 `FilterDialog`（内置的也能打开：可改名/换图标，规则区对内置禁用并说明原因）。
- 悬停行尾出现 `⋯` 菜单：重命名 / 复制为新筛选器 / 删除（可删时）。
- 列表底部 `+ 新建筛选器` → 空白 `FilterDialog`（默认 `and` 组，预填「标签 是 …」）。
- 拖拽排序：**本期不做**（`sort_order` 字段留着，后续再加）。

### 2.6 迁移

- `entry_filters.query` 里现有的是扁平 `EntryQuery` JSON，新增 `rule` 字段缺省为 `nil` → 行为不变。
- 不需要改表结构；`Deletable` 是代码属性，隐藏状态存 meta。

---

## 三、编辑框重构（右栏详情 + Vditor）

### 3.1 视图结构

```
view: "entries" | "detail" | "settings"

<workspace>
  <Sidebar/>                    ← 常驻，详情视图下高亮来源筛选器
  <div class="main">
     entries  → <EntriesView/>
     detail   → <EntryDetailView entryId=… backTo={{filterId, day}}/>
     settings → <SettingsView/>
  </div>
</workspace>
```

- 详情视图占右侧整个主区，**不再是浮层**：没有 `.editor-overlay`、没有 backdrop。
- 顶部一条「详情条」：`← 返回 {筛选器名}` · 条目的时间/来源/能力徽标 · 右侧「保存（⌘S）」「⋯ 更多（复制/归档/删除）」。
- 「更多」菜单里的删除复用确认模态（不再 `window.confirm`）。
- 未保存时：点返回 / Esc / 切筛选器 → 弹「有未保存的修改」三选（保存并返回 / 放弃 / 继续编辑）。
- 从同一条目再次进入复用同一 `entryId`，`draft` 从最新 Entry 初始化。
- 从列表进详情时**不丢列表位置**：`EntriesView` 卸载后重建会重排到最新，
  所以详情视图用「覆盖 + 保留 EntriesView 挂载」的方式实现 ——
  `entries` 与 `detail` 都在 DOM 里，详情用 `position:absolute; inset:0` 盖住主区，
  返回时列表滚动位置原样还在。这是「右栏打开」的关键细节。

### 3.2 Vditor 集成（`components/VditorEditor.tsx`）

已核实（Vditor 4.0.1，本地已解包看过 `dist`）：

| 项 | 结论 |
| --- | --- |
| `options.cdn` | 可指向本地目录；默认 `https://unpkg.com/vditor@x.y.z`。**离线 app 必须换成本地** |
| `options.mode` | `"wysiwyg" \| "ir" \| "sv"` → 用 `"ir"`（即时渲染） |
| 运行时必需资源 | `index.min.js`(280KB) + `js/lute/lute.min.js`(3.6MB) + `js/icons/*`(62KB) + `js/i18n/zh_CN.js` + `css/index.css` + `css/content-theme/{light,dark}.css` |
| 图片粘贴 | `options.upload.handler(files: File[]) => string \| null \| Promise<string>`（返回要插入的 Markdown） |
| 保存快捷键 | `options.ctrlEnter(value)` 回调 |
| 变更回调 | `options.input(value)`；`options.after()` 渲染完成；`options.esc(value)` |
| 主题 | 编辑器壳 `theme: "classic" \| "dark"`（跟随我们的深浅主题）；正文预览 `preview.theme.current: "light" \| "dark"` |
| 图标风格 | `icon: "ant" \| "material"`（用 `ant`，与现有线性图标更接近） |

**离线资源落地（已定：全量拷贝）**：把 `node_modules/vditor/dist` **整个**拷到
`frontend/public/vditor/dist/`（约 22MB），`cdn: import.meta.env.BASE_URL + "vditor"`。
全量拷贝意味着公式（katex/mathjax）、流程图（mermaid/flowchart）、思维导图（markmap）、
图表（echarts）、五线谱、化学式这些渲染器**都能用**，代价只是安装包变大；
它们都是按需加载（渲染到对应语法时才请求），不影响编辑器的启动速度。
构建脚本里加一步校验：缺 `dist/js/lute/lute.min.js` 或 `dist/index.min.js` 就让
`vite build` 失败（不然装出来是"打开编辑器白屏"）。

**必须实测的未知项（写进实施清单）**
1. Wails 内嵌资源用 `wails://` / 自定义 scheme 时，`cdn` 相对路径能否被 lute 的 `<script src>` 加载。
   失败则退路：用 `options._lutePath` 或把 lute 以 base64/内联方式注入（需评估）。
2. IR 模式中文 IME 与 Tauri/Wails WebView2 的组合（Vditor 在 IR 下用 contenteditable，历史上 IME 有过问题）。
3. 我们的 `#tag` 高亮在 Vditor 里没有等价挂点（`customRenders` 只管代码块）→
   **接受编辑器内不高亮 tag**，卡片/收集页照旧显示 tag 徽标；文档里写清这是取舍。

**工具栏清单**（精简，不要 Vditor 默认那排）：

```js
toolbar: ["headings","bold","italic","strike","|","list","ordered-list","check","|",
          "quote","line","code","inline-code","table","link","|","undo","redo","|",
          "edit-mode","both","fullscreen"]
```

去掉：表情、录音、上传（用 `upload.handler` 接管粘贴，不要选文件上传）、插入图表/公式/五线谱。

**主题映射（`styles/vditor-theme.css`）**：把 Vditor 的 CSS 变量接到我们的 token 上，
不放任它自带配色：

```css
.vditor { --panel-background-color: var(--surface); --textarea-background-color: var(--surface);
          --toolbar-background-color: var(--surface-2); --border-color: var(--border-soft);
          --second-background-color: var(--surface-2); }
.vditor-ir .vditor-reset { font-family: var(--font-ui); font-size: 14px; line-height: 1.8; color: var(--text); }
.vditor-toolbar { border-bottom: 1px solid var(--border-soft); padding: 2px 6px; }
/* 边框全部去掉：我们不要"低档边框"，用底色分层 */
```

### 3.3 只有一个编辑器：Vditor，按场景精简界面

**全应用统一用 Vditor**，不再保留 CodeMirror。差别只在「给多少界面」：

| 场景 | 组件 | mode | 工具栏 | 高度/外观 | 键盘 |
| --- | --- | --- | --- | --- | --- |
| 详情视图 `EntryDetailView` | `MarkdownSurface preset="detail"` | `ir` | **完整**（见下） | 撑满右栏 | `⌘S` 保存、`Esc` 返回 |
| 底部「记一条」`Composer` | `preset="composer"` | `ir` | **精简 6 项**：粗体 / 斜体 / 列表 / 任务 / 行内代码 / 链接 | 3 行起，内容多了自动长到 40vh 封顶 | `⌘Enter` 存下、`Esc` 清空焦点 |
| 快速输入窗 `Capture`（热键唤起） | `preset="capture"` | `ir` | **隐藏**（`toolbarConfig.hide`） | 无边框、无内边距，透明底接窗卡 | `⌘Enter` 存下、`Esc` 关窗 |
| 隐写窗 `Stealth` | **保持现在的 `<textarea>`** | — | — | 全透明 | — |

为什么隐写窗不换：它故意不用富文本 —— 那一窗的要求是「看不见、但 IME 必须正常」，
textarea 是刻意的正确选择（`Stealth.tsx` 顶部注释就是写这件事）。它不是「编辑器」，是盲输入。

实现：一个薄包装 `components/MarkdownSurface.tsx`，

```ts
type SurfacePreset = "detail" | "composer" | "capture";
// preset 只决定：toolbar 数组 / toolbarConfig.hide / height / classes / cache.enable / 占位文案
```

对外暴露 `MarkdownSurfaceHandle`：`getValue / setValue / focus / insertImageToken`，
替换掉现在 `MarkdownEditorHandle` 的 `insert / wrap / focus`。
`MarkdownEditor.tsx`（CodeMirror 封装）**删除**，`package.json` 里 6 个
`@codemirror/*` + `@lezer/highlight` 一起移除（`styles/timeline.css` 里 `.md-editor .cm-*`
的规则改成 Vditor 的选择器，深色/浅色都走我们的 token）。

**统一带来的取舍，明确记下来**：

- `#tag` 在编辑器内不再高亮（CodeMirror 里那个 `cm-md-tag` 装饰器没有 Vditor 等价挂点）。
  tag 在卡片、详情属性面板、收集页照常显示。想找回只能走 Lute 自定义 renderer，属于后续探索。
- 浮窗的启动成本变高：热键唤起 `Capture` 时也要初始化 Vditor（lute 3.6MB + IR 初始化）。
  对策见 3.4 的预热方案；**这是批 3 必须先量的一件事**（热键按下到能打字，目标 < 150ms）。

### 3.4 体积与加载

- `lute.min.js` 3.6MB + `index.min.js` 280KB 是**按需**加载：任一 surface 首次挂载时
  `const { default: Vditor } = await import("vditor")`，先渲染一个等高的骨架，不闪白。
- **预热**：主窗 `App` 挂载后 idle 阶段（`requestIdleCallback`）就执行一次 `import("vditor")`，
  让模块与 lute 提前进内存；`Capture` / `Stealth` 这类独立窗口在 `main.tsx` 分流时就预热
  （它们的窗口在热键前已经存在，预热不占用户可感知的时间）。
- 安装包增大约 22MB（public 里全量 `vditor/dist`）。这是明确选择：**买下「公式/流程图/思维导图/图表都能渲染」**，
  而不是把 15MB 花在"反正用不上"的猜测上。若日后体积成为问题，再按需裁剪并加构建白名单。

### 3.5 详情视图的其余改造

- 能力开关（剪贴板/片段/缩写）从小字勾选改成右侧一块「属性面板」：`能力` / `片段设置` /
  `元信息（时间/来源/复制次数/哈希）` 三段，可折叠。
- 片段额外字段（`SnippetFields`）原样嵌进属性面板，不再挂在编辑器底部。
- 图片点击放大仍走 `Lightbox`；正文图片走 `memoimg:` → `useEntryImages` 不变。
- 保存：`⌘/Ctrl+S` 与按钮同一条路径；保存后不关视图，只把「未保存」标记清掉并 toast。

---

## 四、实施顺序与验收

### 批 1 · Todo（后端可先跑通）

1. `internal/clip/todo.go`：解析器 + `todoStateOf` + `SetTodoDone` + `ListTodos`
2. `entry_store.go`：schema 加 `todo_state` / `todo_done_at`；`SaveEntry`/`mergeEntry` 里重算派生列；`entryColumns` 加两列
3. `entry_query.go`：`EntryQuery.Todo` + `where()` 分支；`EntryStats` 加 `todoOpen`/`todoDone`
4. `entry_api.go`：`GetTodos` / `SetTodoDone` / 统计透出
5. 前端：`TodoView.tsx`、侧栏入口、`EntryCard` 待办徽标、`api.ts` 类型
6. 测试：`todo_test.go`（解析边界：代码块、`[X]`、嵌套、重复文本、增删行后 key 稳定；`SetTodoDone` 不动其他行/不重置 tags）

**验收**：写一条含 3 个 `- [ ]` 的备忘 → 侧栏「待办 3」→ 收集页勾掉 1 个 → 打开条目看正文是 `- [x]`，
另两行**逐字节未变**；全勾完 → 条目 `todo_state=done`、收集页移入「已完成」。

### 批 2 · 筛选器

1. `entry_api.go` + 新 `entry_filter_rule.go`：`FilterGroup` 类型与 SQL 编译（纯函数，单测友好）
2. `DeleteFilter` 支持内置隐藏（meta）、`CountEntriesWith`
3. 前端：`FilterDialog.tsx`、`FilterRuleEditor.tsx`、图标网格、侧栏 `⋯` 菜单与「+ 新建筛选器」
4. 测试：规则编译单测（and/or/not 嵌套、空组、多值 tag、ageDays 边界）+ 与扁平条件叠加

**验收**：新建「有未完成待办 且 带 #工作 标签」的筛选器 → 命中数非零且与页面条数一致；
日历圆点点进去能看到同样的条目；删掉「置顶」重启后仍在隐藏态；点「恢复默认」后回来。

### 批 3 · 编辑框（一个编辑器）

1. Vditor 资源落地（全量 dist）+ 构建校验 + `MarkdownSurface.tsx`（懒加载、预热、主题、上传 handler、ctrlEnter/esc）
2. 三个 surface 逐个换装：`EntryDetailView`（新建，完整工具栏）→ `Composer`（精简 6 项）→ `Capture`（隐藏工具栏）
3. 删除 `MarkdownEditor.tsx`（CodeMirror）与 `@codemirror/*` 依赖、`EntryEditor.tsx` 浮层、`styles/editor.css` 的 `.editor-*` 规则；把 `.md-editor .cm-*` 样式改成 Vditor 选择器
4. 量热键唤起延迟（`Capture` 从按键到可打字），按 3.4 调预热；超 150ms 就查 lute 初始化而不是退回去加第二个编辑器
5. 验证：`pwsh scripts/visual-check/check-css.ps1` 出图对比深浅两套主题

**验收**：双击卡片 → 右侧整区打开编辑（无浮层、有工具栏），列表滚动位置在返回后不变；
底部「记一条」只有精简工具栏、高度自适应；热键唤起的输入窗没有工具栏但能正常打字；
三处粘贴截图都出现 `![](memoimg:…)` 且卡片渲染出图；断开网络启动 → 三个入口都能编辑（零 CDN 依赖）；
`package.json` 里不再有 `@codemirror/*`。

### 验证命令（每批都跑）

```powershell
go build -o bin\clipnest-dev.exe .
go test ./internal/...
cd frontend; npx tsc --noEmit; npx vite build --mode production
pwsh -File scripts\visual-check\check-css.ps1     # 批 3 必跑
```

---

## 五、风险与取舍（已想清楚，不是待办）

| 风险 | 处置 |
| --- | --- |
| Vditor 的 lute 从 `cdn` 加载，Wails 自定义 scheme 可能拦 | 批 3 第一件事就是最小验证；失败退路写在 3.2 |
| lute 3.6MB + 全量 dist 22MB 拉大安装包 | 已明确接受：换「公式/流程图/思维导图/图表可渲染」；资源按需加载，编辑器仍是懒加载 |
| IR 模式 IME | 批 3 装机实测中文输入（详情、Composer、Capture 三处都要试）；不行就退 `mode:"sv"`；不再退回 CodeMirror（已定单编辑器） |
| 热键唤起的输入窗要秒开，但 Vditor 初始化有成本 | 模块 + lute 预热（3.4）+ 等高骨架；把「按键 → 可打字」当硬指标量出来，不靠感觉 |
| 编辑器内 `#tag` 不高亮 | 明确取舍，tag 在卡片与详情属性面板里照常可见 |
| 同步版本混跑把派生列抹掉 | `mergeEntry` 一律本端重算派生列（1.4） |
| 老旧筛选器规则非法（id 不存在等） | `DeleteFilter` 幂等；`where()` 遇到未知 field/op 直接丢弃该条件并 `Logf` 一行 |
| 收集页全库扫描变慢 | 先粗筛 `todo_state <> ''`，必要时 3 秒缓存；不做预计算表 |

## 六、明确不做的

- 待办到期提醒 / 日历到期待办 / 重复任务 / 子任务树
- 筛选器拖拽排序、筛选器导出导入
- 规则表达式字符串（类 TiddlyWiki 语法）：结构化规则树已经够用，且不用写解析器
- 编辑器内的 tag 自动补全、链接预览、协同
