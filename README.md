# ClipNest

> 常驻托盘的 Windows 剪贴板管理器 · [Wails v3](https://v3.wails.io) + Go + React + TypeScript

ClipNest 在你使用电脑时安静地记录复制过的内容——文本、链接、图片、富文本，以及文件（只记引用），
并提供一个干净的窗口来搜索、收藏、删除和回填这些记录。关闭窗口不会退出程序，它会回到托盘继续工作。

![主界面](docs/images/history.png)

## 特性

- **托盘常驻**：关闭窗口只是隐藏窗口，进程继续监听；只有托盘菜单的「退出 ClipNest」才真正退出。
- **静默收集**：基于 Win32 `AddClipboardFormatListener` + `WM_CLIPBOARDUPDATE`，不做轮询、几乎零开销。
- **多格式**：
  - 纯文本（自动识别单个链接 → 归类为「链接」）
  - 图片（`PNG` / `CF_DIB` / `CF_DIBV5` 三种来源都支持，24/32 bpp 手写 DIB 解码）
  - 富文本（`HTML Format` 片段 + 纯文本回退）
  - 文件（`CF_HDROP`，只记路径引用，见下文）
  - OLE 延迟渲染的剪贴板（部分应用只暴露 `IDataObject`）也有回退读取路径
- **去重**：按内容哈希去重，重复复制只更新时间戳与次数，不产生垃圾条目。
- **收藏 / 搜索 / 分类**：全部、文本、链接、图片、文件、富文本、收藏；搜索匹配正文、文件名与来源程序。
- **一键回填**：选中「复制」或双击条目，把内容重新写回剪贴板，并抑制自己写入产生的回声记录。
- **保留策略**：最多条数、最长天数、单条文本长度上限；收藏条目永不被自动清理。
- **隐私开关**：可按程序名或正则忽略（密码管理器、内部后台等），也可整体暂停收集。
- **其它**：全局快捷键唤出窗口、开机自启（写 HKCU Run）、单实例（第二次启动只是把窗口拉到前台）。
- **纯本地**：不联网、不上传，数据全部存在 `%APPDATA%\ClipNest`。

## 文件复制怎么处理？—— 四种方案与当前选择

文件类复制（`CF_HDROP`）有四种典型做法，ClipNest 采取 **方案 A+B**：

| 方案 | 做法 | 优点 | 代价 |
| --- | --- | --- | --- |
| **A+B（当前实现）** | 只记录文件路径与元信息，周期性/手动检查是否还存在，失效则标记「已失效」 | 零磁盘占用、不碰用户原文件、隐私风险最低 | 原文件被移动/删除后引用失效，无法「恢复」内容 |
| C 归档实体 | 把文件拷进库目录 | 原文件删了也能取回 | 吃磁盘（需配额策略），隐私与备份风险高 |
| D 忽略文件 | 完全不记录 | 最简单 | 少一类能力 |
| E A+B + 一键归档 | 平时存引用，需要长期保留时手动「归档到库」 | 兼顾成本与可靠性 | 需要多一套归档目录与配额逻辑 |

> 详细取舍、数据模型与后续可选实现见 [docs/FILE-STRATEGY.md](docs/FILE-STRATEGY.md)。
> 当前版本实现的是 A+B；E 的「一键归档」已预留在数据模型里（`Item.Files` 与 blob 目录）。

## 快速开始

### 依赖

| 依赖 | 版本 | 说明 |
| --- | --- | --- |
| Go | 1.26+ | `go.mod` 声明 `go 1.26.0`，低版本会自动尝试下载工具链 |
| Node.js | 20+ | 构建前端（Vite 8） |
| Wails v3 CLI | `v3.0.0` | `go install github.com/wailsapp/wails/v3/cmd/wails3@latest` |
| WebView2 Runtime | 任意新版本 | Windows 10/11 一般已内置 |

### 构建与运行

```powershell
# 依赖（首次）
npm install --prefix frontend

# 开发模式（前端热更新 + Go 自动重启）
wails3 dev

# 生产构建，产物在 bin/clipnest.exe
wails3 build

# 运行已构建的程序（--show 强制打开窗口）
.\bin\clipnest.exe --show
```

### 打包安装程序（可选）

```powershell
wails3 package      # 生成 NSIS 安装包（需要 NSIS，模板已内置配置）
```

## 使用

| 操作 | 方式 |
| --- | --- |
| 唤出窗口 | 托盘图标左键 / 全局快捷键（默认 `Ctrl+Shift+V`） |
| 收起到托盘 | 关闭窗口按钮（×）或窗口右上「收起到托盘」 |
| 复制回剪贴板 | 选中条目后按 `Enter` 或双击 / 点「复制」 |
| 上下选择 | `↑` `↓` |
| 删除当前条目 | `Delete` |
| 搜索 | `Ctrl+F` |
| 彻底退出 | 托盘菜单 →「退出 ClipNest」 |

## 数据与存储

```
%APPDATA%\ClipNest\
  items.jsonl     # 一行一条记录（JSON），改动后防抖 400ms 原子重写
  settings.json   # 采集/保留/忽略/快捷键等设置
  blobs\          # 图片条目的 PNG 数据（<id>.png）
```

- 单条记录见 `internal/clip/model.go` 的 `Item`；
- 历史全量常驻内存（万级条目无压力），筛选与搜索在内存完成，写入不阻塞 UI 线程；
- 退出时 `OnShutdown` 会 flush 一次，正常关闭不丢数据；异常退出最多丢失最后一次 400ms 内的改动。

## 目录结构

```
main.go                       应用装配：托盘、窗口、关窗不退出、快捷键、单实例
internal/clip/
  model.go                    Item / Filter / Stats / Captured 等数据结构
  settings.go                 设置结构与默认值
  store.go                    文件存储：JSONL 索引 + blob、去重、保留策略
  service.go                 绑定给前端的 ClipService（21 个方法）
  win32_windows.go            Win32 基元：剪贴板 API、前景程序、开机自启
  clipboard_windows.go        读取/写回各类剪贴板格式
  dib_windows.go              CF_DIB(V5) ↔ image.NRGBA 互转
  ole_windows.go              OLE IDataObject 回退（延迟渲染场景）
  watcher_windows.go          消息窗口 + WM_CLIPBOARDUPDATE 监听
  shell_windows.go            ShellExecute / 资源管理器定位
  platform_other.go           非 Windows 桩实现（保持可编译）
frontend/                     React + TS + Vite 管理界面
  src/App.tsx                 主布局、事件订阅、键盘快捷键
  src/components/             Sidebar / ItemRow / DetailPanel / SettingsView / Icons
  src/lib/                    api（绑定封装）、format（时间与体积）
  bindings/                   wails3 生成的 TS 绑定（由 `wails3 generate bindings` 产出）
scripts/make_icon.py          生成 appicon.png 与 windows/icon.ico
scratch/clipdump/             开发期剪贴板格式转储小工具
docs/FILE-STRATEGY.md         文件复制的方案对比与数据模型
```

## 已知限制

- **只在 Windows 上有意义**：监听、DIB 解码、托盘均为 Win32 实现；其它平台编译出的是空壳。
- **富文本回填**：写回时同时写 `HTML Format` 与纯文本，个别应用（如旧版 Office）可能只取纯文本。
- **OLE 延迟渲染**：已做 `IDataObject` 回退（文件、文本），但图片仍依赖原生 `PNG`/`DIB` 格式。
- **不做跨屏 DPI 特调**：窗口按系统 DPI 缩放，多屏不同缩放下位置由 Wails/WebView2 决定。
- **历史全量在内存**：十万级条目需要改成 SQLite 或分页索引（当前上限默认 1000 条，足够日常）。

## 隐私

- 所有数据只写本机 `%APPDATA%\ClipNest`，程序不发起任何网络请求。
- 建议在「设置 → 忽略规则」里加上密码管理器进程名（`keepass.exe`、`1password.exe`…）与敏感正则。
- 剪贴板本身是全局可读的，ClipNest 只写自己那部分数据，不修改系统设置。

## 路线图

- [ ] 方案 E：条目「归档到库」按钮 + 归档目录配额
- [ ] 全局搜索的模糊匹配（拼音首字母）
- [ ] 分类规则（按来源程序自动打标签）
- [ ] 条目内图片压缩 / 缩略图缓存，降低 blob 体积
- [ ] SQLite 存储后端（历史规模大了再切换）

## 许可

MIT，见 [LICENSE](LICENSE)。
