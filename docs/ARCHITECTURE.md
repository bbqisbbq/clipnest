# 架构与分层

> 目标：一处逻辑只存在一份，改一个功能不需要翻五个文件。
> 每次重构后更新此文档。

## 一、后端（Go）

```
main.go                  Wails 装配：窗口、托盘、快捷键、钩子注入
internal/expand/         缩写展开引擎（零业务知识，只认 Host 接口）
internal/clip/           业务层：条目、捕获、同步、绑定 API
```

`internal/clip` 内部按职责分文件（同一 package，靠文件边界保持清晰）：

| 文件 | 职责 | 依赖 |
| --- | --- | --- |
| `model.go` | `Kind` / `FileRef` / `Captured` —— 纯数据 | 无 |
| `settings.go` | `Settings` + 默认值 + 单调时钟 | 无 |
| `entry.go` | **统一条目模型**：`Entry`、CRUD、`EntryQuery`、附件 | model / settings |
| `entry_api.go` | 绑定给前端的 API + 筛选器 CRUD + 日历聚合 | entry |
| `entry_image.go` | 图片解码、缩放、扩展名推断 | 无（纯函数） |
| `store.go` | SQLite 打开/迁移、meta、墓碑、统计、日志 | entry |
| `service.go` | 捕获管线、设置副作用、开机自启、窗口/热键钩子 | store / entry |
| `sync.go` | WebDAV 打包同步（entries 一段） | store / entry |
| `snippet.go` | 片段 ↔ 条目的适配层（`Snippet` 只是 `expand.Trigger` 的形状） | entry |
| `snippet_host.go` | 实现 `expand.Host`：缩写索引、匹配、渲染、执行 | expand / entry |
| `snippet_actions.go` | 片段执行（粘贴/命令/启动程序/打开网址） | snippet |
| `*_windows.go` | 平台层：剪贴板读写、DIB、OLE、窗口焦点、DWM 边框 | 无业务知识 |

**铁律**：`*_windows.go` 不许 import 业务概念；业务层不许直接调 Win32。

## 二、前端（React + TS）

```
src/main.tsx             入口：模式分流（主窗 / picker / capture）+ 主题预应用
src/App.tsx              薄壳：装 hooks、切视图、转发通知（不写业务）
src/hooks/
  useToast.ts            全局通知
  useTheme.ts            主题落地（预设 + 自定义 CSS + 原生边框）
  useAppData.ts          设置 / 应用信息 / 同步状态 / 筛选器 / 计数
src/lib/
  api.ts                 绑定封装 + 类型别名 + 默认值
  images.ts              正文图片引用（memoimg:）的解析与收集
  themes.ts              配色预设、对比度自检、自定义 CSS 净化
  format.ts              时间/字节格式化
  liveMarkdown.ts        （已废弃，改用 CodeMirror）
src/components/
  TitleBar / Sidebar / SettingsView     布局与设置
  EntriesView                           统一内容视图（列表 + 卡片 + 编排）
  Composer                              底部输入卡片（编辑器 + 预览 + 拖高）
  MarkdownEditor                        CodeMirror 6 封装
  SnippetFields                         片段的额外编辑项
  HotkeyInput                           按键录制
  Calendar                              月视图导航
  Picker / Capture                      独立窗口
```

**铁律**：`components/` 里的组件不直接算业务；需要数据就通过 props 或 hook。
`lib/` 里不许有 React。

## 三、数据模型（唯一真相）

```
entries        条目：内容 + tag + actions（clipboard / snippet）
entry_blobs    附件：内容寻址，≤512KB 进库、更大落盘
entries_fts    全文索引（trigram，中文子串可搜）
entry_filters  用户保存的筛选器
tombstones     删除墓碑（同步用）
meta           键值对（设备 id、一次性标记）
```

`items` / `memos` / `snippets` 三张表已在 2.0.0 删除：它们表达的是同一种东西的
三种形态，现在统一为 `entries` + `actions`。

## 四、同步

- 一台机器一个包：`clipnest_data/<deviceId>-<host>.zip`
- 包内：`package.json`（索引 + 墓碑 + 设置）+ `entries/<hash>.json` + `blobs/entry-<hash><ext>`
- 合并：按内容 hash 配对；`updated_at` 新的赢；`pinned` 取或；删除走墓碑
- 合并必须用 `Store.mergeEntry`（保留远端时间戳）。用 `SaveEntry` 会把
  `updated_at` 盖成"现在"，让本地副本比墓碑还新，删除会被静默忽略。

## 五、后续可以继续拆的地方

1. 后端把 `store.go` + `entry.go` 抽成 `internal/store` 包，`Entry` 抽成 `internal/model`，
   `clip` 只留 service / sync / snippet
2. 后端把 `*_windows.go` 抽成 `internal/winplat`
3. 前端把 `SettingsView`（目前 600+ 行）按「采集 / 交互 / 同步 / 外观 / 关于」拆成子组件
4. 前端 `Picker` 与 `Capture` 共用 `MarkdownEditor` 后可以进一步共用 Composer

拆包会改变 Wails 绑定的 import 路径（`frontend/bindings/...`），需要重新生成绑定，
所以建议一次只动一层，动完立刻装机验证。
