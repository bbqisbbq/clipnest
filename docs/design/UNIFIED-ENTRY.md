# 统一条目模型（Entry）— 做减法

> 起因：剪贴板、片段、备忘三个入口本质是同一个东西的三种形态。
> 目标：**一个类型、一份存储、一处入口**，用属性与标签区分来源与用途；
> 导航靠**日历**与**自定义筛选器**，而不是三个并列页面。

## 一、为什么可以合

| | 剪贴板 | 片段 | 备忘 |
| --- | --- | --- | --- |
| 本质 | 一段内容 | 一段内容 | 一段内容 |
| 差异 | 自动采集、高频、会过期 | 有缩写、靠 trigger 被查出 | 手写、永久 |
| 三者差异 | ← 全部可以用「属性」表达，不需要「不同的表」 | | |

## 二、统一类型

```go
type Entry struct {
    ID       string
    Content  string   // Markdown（手写）或纯文本（采集）
    Kind     Kind     // text|link|image|files|rich|custom|html|app|command|url|password
    Source   Source   // manual | clipboard | import —— 怎么进来的
    Tags     []string // 从正文 #tag 派生
    AutoTags []string // 系统标签：剪贴板 / 片段（用户改正文不会丢）
    Pinned, Archived bool
    // 采集相关
    Hash string; SeenCount, CopyCount int; SourceApp string
    Width, Height int; HTML, RTF string; FormatName string; FormatID uint32; Files []FileRef
    // 片段相关（非空 = 这条参与缩写展开）
    TriggerKey string; CaseSensitive, KeepTrigger bool; Apps, ExcludeApps []string; GroupName string; UseCount int
    // 生命周期
    ExpiresAt *time.Time // nil = 永久；采集条目默认 +30 天
    CreatedAt, UpdatedAt time.Time
}
```

### 关键决定

**1. 过期是"每条自己的属性"，不是"每张表的规则"**

这是合并能否成立的关键。剪贴板条目写入时 `expires_at = now + 30d`；
手写备忘 `expires_at = NULL`。清理就是一句 `DELETE WHERE expires_at IS NOT NULL AND expires_at < now`。
两个生命周期在同一张表里**互不干扰**。

**2. 系统标签单独存一列**

用户说"剪贴板就在采集时写 tag"。若把 `#剪贴板` 写进正文，会污染被复制的内容本身。
所以：`tags`（从正文派生）+ `auto_tags`（系统给的）分开存，界面合并显示，筛选两者都匹配。

**3. 片段的 trigger 用可空列 + 部分索引**

```sql
CREATE INDEX idx_entries_trigger ON entries(trigger_key) WHERE trigger_key <> '';
```
expander 每次按键查的就是这个索引，命中的永远是极少数条目——和原来片段表一样快。

**4. 附件统一**

`entry_blobs(hash, ext, mime, data, storage, size, path)`，内容寻址：
≤512KB 进库、更大落盘。剪贴板图片与备忘图片走同一条路，去重天然成立。

## 三、导航（学 Memos）

三个并列入口 → 一个入口 + 两种导航方式：

```
内容                      ← 唯一一级入口
  日历        月视图，点某天 = 只看那天
  筛选器      保存下来的查询（自定义入口）
    · 我写的         source = manual
    · 剪贴板         auto_tag = 剪贴板
    · 片段           trigger_key <> ''
    · 置顶           pinned
    · 已归档         archived
    · 图片           kind = image
  标签        #工作 #读书 …
```

**日历解决"淹没"问题**：不再需要滚一条无限时间线，点某天就只看那天；
**筛选器解决"特定类型入口"问题**：片段、图片这些不再需要独立页面，存成一个筛选器即可。

筛选器定义：

```go
type Filter struct {
    ID    string
    Name  string
    Icon  string
    Query FilterQuery   // source / autoTag / kind / pinned / archived / tag / from / to / text
}
```

内置几个，用户可以自己加、改名、换图标——和侧栏菜单自定义复用同一套设置。

## 四、迁移

启动时一次性把三张表搬进 `entries`（幂等，用 meta 标记）：

| 来源 | 映射 |
| --- | --- |
| `items` | source=clipboard，auto_tags=["剪贴板"]，expires_at=created+30d，kind/blobs 原样 |
| `memos` | source=manual，tags 重新从正文派生，expires_at=NULL |
| `snippets` | source=manual，auto_tags=["片段"]，trigger_key 等字段带过来 |

老表**保留不删**（改名加 `_legacy_` 前缀或直接留着），万一要回滚还能用；
确认无问题后再在后续版本删掉。

## 五、分阶段

| 阶段 | 内容 | 估时 |
| --- | --- | --- |
| **P1** | `entries` 表 + 统一 `Entry` 类型 + 迁移 + 测试（数据层） | ~3h |
| **P2** | 日历组件 + 筛选器（含保存/改名/换图标） | ~3h |
| **P3** | 侧栏收成一个入口；采集/片段/备忘全部改走 `entries` | ~2h |
| **P4** | expander 与 picker 改查 `entries` 的 trigger 索引 | ~1h |

P1 完成前不碰界面，保证随时可回退。
