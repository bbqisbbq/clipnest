# 集成 Memos 的设计方案（本地原生，不起 Web 服务）

> 参照：`usememos/memos`（MIT，Go，64k★）。它的核心是「碎片化 Markdown 备忘 + `#标签` + 时间线 + 置顶 + 附件」。
> 本文只设计，不含实现。

## 一、先切清楚：要它的什么，不要它的什么

| Memos 有 | 我们要吗 | 理由 |
| --- | --- | --- |
| 快速记一条 Markdown | **要** | 这是它的灵魂，和剪贴板管理器是同一类"随手攒东西"的场景 |
| `#tag` 标签、按标签筛 | **要** | 零成本，正文里就能写 |
| 置顶、归档、时间线 | **要** | 简单，存储加两个字段 |
| 全文搜索 | **要**（中文也要能搜） | 已在本地验证 `FTS5 + trigram` 可用 |
| 附件（图片/文件） | **要** | 复用 ClipNest 已有的 blob 表与图片管道 |
| 多用户 / 登录 / 权限 | **不要** | 单机单人，没有任何账号概念 |
| 公开分享链接 / RSS / 评论 / 表情反应 / 关系图 | **不要** | 都需要服务端 + 别人访问 |
| Web UI、gRPC、REST 网关 | **不要** | 就是你说的"不想跑服务端" |
| **数据可互通**（万一以后想上 Memos 服务端） | **可留口** | 存储与导出按 Memos 的字段形状设计，将来一键推拉 |

## 二、四条路线，先排除三条

| 路线 | 做法 | 结论 |
| --- | --- | --- |
| **A. 本地原生模块**（推荐） | 在 ClipNest 内新增「备忘」域：SQLite 表 + 原生 UI + 复用现有同步 | ✅ 无服务、无端口、单进程、和剪贴板/片段天然联动 |
| B. 内嵌 memos 的 server 包 | `import "github.com/usememos/memos/server"` 在 Wails 里起它的路由 | ❌ 它自带 `ListenAndServe`、gRPC + grpc-gateway、多用户 auth、内嵌前端 dist；我们的进程模型和它的生命周期冲突，升级要跟着它走 |
| C. 跑 memos 二进制 + WebView 打开 127.0.0.1 | 子进程 + 端口 | ❌ 正是你不要的形态；还多一份数据、多一个端口、多一个要升级的东西 |
| D. 只做远端客户端 | 设置里填 `https://memos.example.com` + token，走 `/api/v1/memos` | ⚠️ 单独用不满足"本地"，但**可以作为 A 的可选后端**（见第五节） |

**推荐 A 为主体，D 作为可选同步后端**：本地永远可用；如果哪天你已经在服务器上跑了 Memos，再把 ClipNest 指过去就行——仍然是 ClipNest 主动推拉，不在本机起服务。

## 三、数据模型（贴着 Memos 的字段名，方便将来互通）

```sql
CREATE TABLE memos (
  id          TEXT PRIMARY KEY,          -- 本地 id（32 hex）
  content     TEXT NOT NULL,             -- Markdown 原文，标签写在正文里：今天 #工作 #会议
  tags        TEXT NOT NULL DEFAULT '',  -- 冗余：",工作,会议," 便于按标签筛（与正文同步派生）
  pinned      INTEGER NOT NULL DEFAULT 0,
  archived    INTEGER NOT NULL DEFAULT 0,
  visibility  TEXT NOT NULL DEFAULT 'PRIVATE', -- 预留：PRIVATE/PUBLIC，本地不用但同步时不丢
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  -- 以下是 ClipNest 特色，Memos 没有
  source      TEXT NOT NULL DEFAULT 'manual', -- manual|clipboard|snippet|import
  source_app  TEXT NOT NULL DEFAULT '',       -- 从哪个程序复制来的
  hash        TEXT NOT NULL DEFAULT ''        -- 内容哈希：同步去重 / 冲突判定
);

-- 中文可搜：trigram 分词，子串命中（已实测可用，SQLite 3.51.2）
CREATE VIRTUAL TABLE memos_fts USING fts5(content, tags, tokenize='trigram');

-- 附件复用既有 blobs 通路的变体
CREATE TABLE memo_blobs (
  memo_id TEXT NOT NULL,
  name    TEXT NOT NULL,   -- 原始文件名/顺序
  ext     TEXT NOT NULL,
  bytes   BLOB NOT NULL,
  PRIMARY KEY (memo_id, name)
);
```

要点：

- **标签不入单独表**：Memos 就是从正文 `#xxx` 派生 `tags`；我们同样在保存时正则提取，写回 `tags` 列，筛选就是 `tags LIKE '%,工作,%'`
- **FTS5 是外部内容表**（`content=memos` + 触发器同步），不重复存正文；`trigram` 让「会议纪要」能被子串「纪要」命中，不需要额外中文分词器
- **hash 用于同步**：内容相同即同一条，避免两端手工建了同样内容却各存一份

## 四、同步：完全复用现有打包同步（不新增机制）

现有 `clipnest_data/<deviceId>-<host>.zip` 里已经有 `package.json` + `items/` + `snippets/` + `blobs/`，备忘照抄同一形状：

```
package.json          ← 追加 memos[] 与 deletedMemos[]
memos/<hash>.json     ← 一条备忘（正文 + 标签 + 置顶 + 时间戳）
memos-blobs/…         ← 附件（同样受 96 MB 预算约束）
```

合并规则与片段一致（已经是验证过的逻辑，不引入新的冲突模型）：

- 配对键：`hash`（内容哈希）
- 正文取 `updated_at` 新的；`pinned` 取或；`archived` 取或
- 删除用墓碑（`deletedMemos`），保留期与现有墓碑一致（180 天 / 2000 条上限）

## 五、可选：把远端 Memos 当同步后端（不做本地服务）

只在你已经有一台 Memos 服务器时启用。客户端侧：

| 本地动作 | 远端 API |
| --- | --- |
| 新建备忘 | `POST /api/v1/memos`（`content` 带 `#tag`） |
| 改正文/置顶 | `PATCH /api/v1/memos/{id}`（`updateMask`） |
| 删除 | `DELETE /api/v1/memos/{id}` |
| 拉取增量 | `GET /api/v1/memos?filter=update_time > ...` 或分页全量比对 |
| 附件 | `POST /api/v1/attachments` 后 `SetMemoAttachments` |

认证：`Authorization: Bearer <access token>`（Memos 设置里可建）。
**不需要**在本机跑任何服务，ClipNest 只是 HTTP 客户端，调度复用现有 `syncLoop`。

## 六、界面与交互

### 侧栏（沿用两级导航）

```
备忘            ← 一级（新增）
  筛选  全部 / 置顶 / 已归档
  标签  #工作 #读书 #灵感 …（从数据里派生）
```

### 三个入口，别让人"打开一个界面才能记事"

1. **快捷键浮窗**：`Ctrl+Shift+N` 在光标附近弹一个 420×200 的输入条（复用 picker 那套无边框窗口 + 失焦隐藏），`Enter` 保存、`Ctrl+Enter` 换行、`Esc` 关闭
2. **托盘菜单「记一笔」**：同上，不抢占当前窗口
3. **从现有的东西一键变备忘**：剪贴板详情面板加「存为备忘」（和已有的「存为片段」并列）；片段页加「追加到备忘」

### 编辑器

- Markdown 输入 + 预览切换（**复用已上线的 `marked` 渲染与清理逻辑**，不再引新依赖）
- 正文里打 `#` 自动出标签候选（从历史标签里补全）
- 粘贴图片直接进附件（复用现有 PNG/DIB 管道）
- 时间线展示：按天分组，置顶的独立置顶区

### 三向联动（这才是比 Memos 多出来的价值）

```
复制的文字 ──存为备忘──▶ 备忘 ──存为片段──▶ 片段（配缩写，任意输入框可展开）
     ▲                    │
     └────复制/粘贴───────┘
```

## 七、里程碑与工作量

| 里程碑 | 内容 | 估时 |
| --- | --- | --- |
| **M1 能记能搜** | `memos` 表 + FTS5 trigram + 基础 CRUD + 侧栏一级项 + 列表/编辑器 + `#tag` 派生与筛选 | ~6 小时 |
| **M2 记起来快** | 快捷键浮窗（复用 picker 窗口技术）+ 托盘「记一笔」+ 剪贴板「存为备忘」+ 片段「追加到备忘」 | ~4 小时 |
| **M3 多端一致** | 打包同步扩展（`memos/` + 墓碑）+ 附件进包 + 设备面板显示备忘条数 | ~3 小时 |
| **M4 可选互通** | Memos 远端客户端（List/Create/Update/Delete）+ 一次性导入器（读 Memos 导出的 JSON 或直连 API） | ~6 小时 |

M1+M2 就是"日常可用的本地备忘"，M3 才需要云端，M4 完全可选。

## 八、验证点与技术前提

- ✅ **FTS5 + trigram 可用**：已于本机 `modernc.org/sqlite` 3.51.2 实测（`CREATE VIRTUAL TABLE … tokenize='trigram'` 成功），中文子串搜索无需分词器
- ✅ **Markdown 渲染**：已有 `marked` + 去脚本/iframe/内联事件的清理，直接复用
- ✅ **附件管道**：图片 blob 读写、剪贴板 PNG 抓取都已存在
- ⚠️ **附件体积**：同步包预算 96 MB 是全局的，备忘附件要参与同一预算，超了就标 `truncated`（现有机制）
- ⚠️ **编辑器体验**：M1 先用 `<textarea>` + 预览切换；如果嫌不够，再考虑 CodeMirror（多 ~300 KB，且要自己写中文输入法兼容测试）
- ⚠️ **冲突**：仍是"同一条两端都改 → 取更新时间新的"，不引入字段级合并（与片段的行为保持一致，避免两套语义）

## 九、明确不做的

多用户与登录、公开分享链接、RSS、评论与表情、关系图、AI 摘要（Memos 新版有，需要模型额度）、Web 端。

## 十、为什么不用它的代码

许可证是 MIT，技术上可以抄，但：

1. 它的 `server` 包假设自己拥有 HTTP 生命周期（`ListenAndServe`、中间件链、静态资源），塞进 Wails 要么改它的代码，要么在进程内多起一个 listener——后者就是你不要的形态
2. 数据层绑在它的 `store` 接口 + 迁移体系上，共用意味着以后每次升级都要跟一次大改
3. 我们真正想要的只有「Markdown 备忘 + 标签 + 搜索 + 置顶」这四件事，用我们自己的 SQLite 表大约 200 行就够，而引入它是几千个文件

结论：**借它的产品形态与 API 语义，不借它的代码。**
