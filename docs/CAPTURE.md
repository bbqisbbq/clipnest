# 剪贴板收集机制与覆盖面

> 目标：**凡是进入 Windows 剪贴板的东西，都进 ClipNest 的历史**（除非命中你配置的忽略规则）。

## 一、监听是怎么做的

```
WM_CLIPBOARDUPDATE  ──►  消息窗口(专用线程)  ──►  channel  ──►  采集协程
   (系统广播)              AddClipboardFormatListener        45ms 合并 → 读格式 → 归类 → 去重 → 落盘
```

1. **消息窗口**：`watcher_windows.go` 在一条 `runtime.LockOSThread` 的线程上注册窗口类、
   创建 `HWND_MESSAGE` 消息窗口，并调用 `AddClipboardFormatListener`。
   这是微软推荐的方式——只要剪贴板**内容发生任何变化**（谁写的都算），系统就向该窗口广播
   `WM_CLIPBOARDUPDATE`。
2. **不做轮询**：没有定时器、没有比对哈希的死循环；空闲时零 CPU。
3. **45ms 合并**：一次 Ctrl+C 有时会连发 2–3 个 update（先放文本再补 HTML 等）。
   采集协程把 45ms 内的多个通知合并成一次读取，既不会重复记账，也远短于人能连按两次 Ctrl+C。
4. **启动时补一次**：程序启动 400ms 后读一次当前剪贴板，把「启动前刚复制的东西」也收进来。
5. **读即用**：`OpenClipboard` 遇到被别的程序占用时会重试 12 次（10ms 起递增），不会因为
   瞬时抢占而漏掉一条。

## 二、格式归类矩阵

读之前先 `EnumClipboardFormats` 枚举**全部**格式，然后按"用户会期望粘回什么"的优先级归类：

| 优先级 | 来源格式 | 归类 | 存储 | 粘回方式 |
| --- | --- | --- | --- | --- |
| 1 | `CF_HDROP` | 文件 | 路径引用 + 元信息（见 FILE-STRATEGY.md） | 重建 `DROPFILES` |
| 2 | `PNG` / `CF_DIBV5` / `CF_DIB` | 图片 | 解码为 PNG 存 blob | `CF_DIB`(+`PNG`) |
| 3 | `HTML Format` | 富文本 | HTML 片段 + 纯文本 | `HTML Format` + 纯文本 |
| 4 | `Rich Text Format` | 富文本 | RTF + 纯文本（内置轻量 RTF→文本） | RTF + 纯文本 |
| 5 | `text/uri-list` | 链接 | 第一行非注释 URI | 文本 |
| 6 | `CF_UNICODETEXT` / `CF_TEXT` | 文本（若是单个 URL 则归为链接） | 原样文本 | `CF_UNICODETEXT` |
| 7 | 只有 OLE `DataObject`（延迟渲染） | 按 1/6 回退 | `OleGetClipboard` + `IDataObject::GetData` | 同上 |
| 8 | **其它任何私有格式** | 「其它格式」 | 原始字节（≤1 MiB）+ 格式名与格式 ID | 按原名重新注册后原样写回 |

第 8 条就是"滴水不漏"的兜底：Excel 单元格区间、颜色选择器、音频片段、各种私有结构，
都会以「其它格式」入账，点「复制」能**原样**还回剪贴板，目标程序仍认得。

枚举里属于剪贴板管线本身的格式会被跳过（`DataObject`、`Ole Private Data`、`Shell IDList Array`、
`Preferred DropEffect`、`FileName(W)`、`ZoneIdentifier` 等），以及不是 HGLOBAL 的句柄
（`CF_BITMAP` / `CF_METAFILEPICT` / `CF_PALETTE` / `CF_ENHMETAFILE`——对它们调 `GlobalSize`
会破坏堆，代码里显式排除）。

## 三、之后发生什么

- **内容哈希去重**：同一份内容再复制一次，只把时间戳提前、`seenCount++`，不新增条目。
- **回写抑制**：ClipNest 自己往剪贴板写东西（点「复制」/快速粘贴）时记下内容哈希，
  4 秒内的同内容 update 直接忽略，不会自己给自己记账。
- **忽略规则**：来源程序名（如 `keepass.exe`）或正则匹配（文本、预览、文件名）直接丢弃。
- **限额**：单条文本长度上限、最大条数、最长保留天数（收藏条目豁免）。
- **持久化**：`items.jsonl` 防抖 400ms 原子重写；图片/自定义字节存 `blobs/`。

## 四、已知抓不到/会打折的情况（诚实清单）

| 情况 | 说明 |
| --- | --- |
| 程序启动**之前**的历史 | 只补得到"当前剪贴板里最后一条"，更早的没有（那属于 Windows 剪贴板历史，系统接口不对外开放） |
| 剪贴板被写入后**立即清空** | 若 owner 在 45ms 内退出且未渲染数据，读到的就是空 |
| 超过 1 MiB 的私有格式 | 跳过（避免把历史撑爆），可在设置里关掉「其它格式」采集 |
| `CF_BITMAP` 等 GDI 句柄格式 | 只认 `CF_DIB`/`CF_DIBV5`/`PNG`；用 GDI 句柄传图的程序需要另做 `GetDIBits`（未实现） |
| 密码管理器 | 通常用私有加密格式或设置了"禁止剪贴板监听"，属于系统/应用层保护，不强行绕过；建议用忽略规则 |
| 虚拟文件（Outlook 附件拖拽） | `FileGroupDescriptorW` 是虚拟流，当前不解析，会落成「其它格式」或跳过 |

## 五、怎么自己验证

```powershell
# 1) 看剪贴板里到底有哪些格式（含名字、大小）
go run ./scratch/clipdump

# 2) 造一条 RTF / 自定义格式，观察是否被收录
go run ./scratch/setclip -rtf "{\rtf1\ansi Hello \b ClipNest\b0}"
go run ./scratch/setclip -custom ExcelClipRange -payload 01000000deadbeef

# 3) 自动化回归：快捷键弹窗 → 回车粘贴到目标窗口
pwsh -File scratch\e2e-picker.ps1     # Enter 直接粘贴
pwsh -File scratch\e2e-picker2.ps1    # Esc 关闭 / Ctrl+Enter 只复制

# 4) 单元测试（真实读写剪贴板 + DIB 互转 + 存储）
go test ./internal/clip/ -v
```

诊断日志在 `%APPDATA%\ClipNest\clipnest.log`（启动、弹窗定位、粘贴结果都会记一行）。
