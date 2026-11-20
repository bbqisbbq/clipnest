# 文件复制的处理策略

剪贴板里的「文件」是最难处理的一类内容：它不是一份可以直接塞进数据库的字节流，而是一组
指向磁盘上某个位置的**引用**（Windows 用 `CF_HDROP` 传递，本质是一段 `DROPFILES` 头 + 宽字符
路径列表）。记录得太"轻"会失去价值，记录得太"重"会和用户的磁盘、隐私、备份策略打架。

ClipNest 当前实现的是**方案 A + B**：只记引用、定期校验。下面把可选路线、取舍和我们为此
设计的数据模型写清楚，方便后续切换到方案 E。

---

## 一、四种方案

### 方案 A：只记路径引用

- 存储内容：文件路径、显示名、大小、是否目录。
- 粘贴回剪贴板时重新构造 `CF_HDROP`，效果与用户重新复制一次等价。
- 优点：零额外磁盘占用；不复制用户数据（尤其是大文件）；不留副本，隐私面最小。
- 缺点：原文件被移动、重命名、删除后，条目再也无法取回内容。

### 方案 B：引用 + 失效检测（与 A 组合使用）

- 在 A 的基础上，定期（本实现：每 3 分钟一次后台扫描，单次最多 500 条）
  对每条文件记录的路径做 `Stat`，把结果写回 `FileRef.Exists`。
- UI 上用「已失效」标签和灰化样式表达；列表里显示「N 项已失效」。
- 优点：用户能立刻看出这条记录还有没有用，不会误以为内容还在。
- 缺点：路径复活（文件被放回原处）时状态会重新变好，这是预期行为，但也意味着"失效"不是永久结论。

### 方案 C：把实体归档进库

- 复制发生时把文件**拷贝**到 `%APPDATA%\ClipNest\archive\<id>\`，之后只从副本读取。
- 优点：原文件删了也不影响历史；真正"完整保存"。
- 缺点：
  - 磁盘占用失控（一次复制 2GB 视频怎么办？必须限额、必须处理并发拷贝与失败回滚）。
  - 隐私与备份半径扩大：用户以为只是复制了一下，实际多了一份长期副本。
  - 需要新的回收策略（归档目录得能手动清理、得算进数据占用的配额里）。

### 方案 D：忽略文件类复制

- 直接不记录。最简单，但丢掉了"我刚复制了哪几个文件"这个高频、实用的场景。

### 方案 E：A+B + 手动「归档到库」

- 默认只存引用；需要长期保留的条目，用户手动点「归档到库」才拷贝实体。
- 优点：把磁盘成本交给用户明确决策；日常零成本，特殊场景有兜底。
- 代价：多一套归档目录、配额、失败回滚和「归档/未归档」状态。

---

## 二、为什么选 A+B

1. **剪贴板历史的第一诉求是"找回刚才复制的那点东西"**，而不是"备份文件"。绝大多数情况下
   用户复制文件后几分钟内就会粘贴，此时原文件一定还在。
2. **写操作风险最小**：不复制、不删除用户文件，ClipNest 对用户磁盘只读（除了自己的数据目录）。
3. **和存储模型天然兼容**：删条目 = 删 `entries` 一行，不需要处理"副本还在不在"的一致性问题。
4. **可平滑升级到 E**：附件已经走内容寻址的 `entry_blobs`（≤512KB 进库、更大的落盘 `entry_files/`），
   文件归档可以复用同一套 blob 存取与清理逻辑。

## 三、数据模型（`internal/clip/model.go`）

```go
type FileRef struct {
    Path   string `json:"path"`   // 绝对路径，粘贴回去时按原顺序重建 CF_HDROP
    Name   string `json:"name"`   // 显示名（filepath.Base）
    Size   int64  `json:"size"`   // 复制时的大小；失效后置 0
    IsDir  bool   `json:"isDir"`
    Exists bool   `json:"exists"` // 最近一次校验的结果
}

type Entry struct {
    Kind  Kind      `json:"kind"`  // "files"
    Content string  `json:"content"` // 所有路径按行拼接，供搜索与纯文本回退
    Files []FileRef `json:"files"`
    // … 其余通用字段
}
```

- 去重哈希 = `sha256("files\x00" + 所有路径以 \x00 连接)`，因此"同一次复制的同一批文件"只留一条记录，
  重复复制只更新时间戳。
- 回填：`WriteFilesToClipboard()` 重新拼 `DROPFILES`（`pFiles=20`, `fWide=1`,
  宽字符串以双 `\0` 结束）并写入 `CF_HDROP`。

## 四、实现要点与坑

| 问题 | 处理 |
| --- | --- |
| 部分程序（WinForms、某些压缩/邮件客户端）用 OLE 延迟渲染，`EnumClipboardFormats` 只看到 `DataObject` | `ole_windows.go` 里 `OleGetClipboard` + `IDataObject::GetData(CF_HDROP)` 回退，并按 `DROPFILES` 头自行解析宽/窄路径 |
| 资源管理器复制的格式很多（`Shell IDList Array`、`Preferred DropEffect`…） | 只认 `CF_HDROP`，其余忽略 |
| 校验大量路径会阻塞 | 只在后台 3 分钟节拍里对 `KindFiles` 条目做 `Stat`，且单次最多 500 条 |
| 目录也常被复制 | `IsDir` 标记，UI 显示为目录项，失效判定同样适用 |

## 五、如果要做方案 E

1. `Settings` 增加 `ArchiveEnabled`、`ArchiveQuotaMB`；
2. `Service.ArchiveEntry(id)`：把 `Entry.Files` 逐个 `io.Copy` 到 `archive/<entryID>/<name>`，
   写入 `FileRef.ArchivedPath`，失败要回滚已拷贝的部分；
3. 归档目录纳入 `Stats.StorageBytes`（它已经遍历整个数据目录，无需改动）；
4. UI：条目卡片/编辑器增加「归档到库」按钮 + 归档状态徽标；
5. 回填时优先用 `ArchivedPath`。

改动集中在 `store.go`（归档/配额）、`service.go`（新方法与回填优先级）与 `EntriesView`，不需要动剪贴板底层。
