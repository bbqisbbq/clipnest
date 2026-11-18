# 架构：三层与扩展点

1.0.0 起把「触发器引擎」从业务里拆了出来。改动只为一件事：**以后加功能不用再碰旧代码**。

```
internal/wininput*    ← 没有独立包：注入/钩子/IME 封装在 expand 的 wininput_windows.go
internal/expand/      ← 纯引擎：缓冲区、匹配、分派（不依赖 SQLite / 剪贴板 / 同步）
internal/clip/        ← 业务：store(SQLite)、service(绑定方法)、sync、update、snippet 域
frontend/src/         ← UI：App(壳) + components/ + lib/(api, theme, format)
```

## 三层各管什么

| 层 | 文件 | 职责 | 不认识的东西 |
| --- | --- | --- | --- |
| 引擎 | `internal/expand/expand.go` | 按键缓冲、终止符判断、最长触发器选择、按类型分派、注入失败回退 | SQLite、剪贴板历史、WebDAV、UI |
| 平台 | `internal/expand/wininput_windows.go` | `SendInput` 注入、退格批量、键盘钩子、`ImmGetCompositionString` 组词检测 | 片段是什么、谁在调用 |
| 业务 | `internal/clip/*` | 片段模型与存储、动作执行、弹窗、同步、设置 | 键盘怎么抓、怎么注入 |

引擎对业务的唯一依赖是一个接口：

```go
type Host interface {
    Match(buf string) (Trigger, int, bool) // 这一串字符的末尾是不是某个触发器
    Render(t Trigger) (string, int)        // 文本 + {{cursor}} 位置
    CommandOutput(t Trigger) (string, error)
    PasteImage(t Trigger) error
    LaunchApp(t Trigger) error
    FallbackPaste(text string) error       // 注入被拒时兜底
    ForegroundProcess() string             // 用于「仅在此/排除」过滤
    IsOwnWindow() bool                     // 别在自己窗口里触发
}
```

`ClipService` 实现它（`internal/clip/snippet_host.go`）；引擎不 import 业务包，业务包 import 引擎。

## 想加东西时的落点

| 想加的功能 | 改哪里 | 不用碰 |
| --- | --- | --- |
| 新的片段类型（音频、颜色、表格…） | `SnippetKind` 常量 + `snippet.go` 存储 + `snippet_actions.go` 动作 + `SnippetsView` 的类型表 | 引擎、注入、同步 |
| 新的触发规则（正则、行首、双触发） | 只改 `expand.go` 的匹配段 | 存储、UI、同步 |
| 新的注入通道（WM_CHAR、SendPlay） | 只改 `wininput_windows.go` + `deliver()` 的回退链 | 存储、UI |
| 新的同步载体（S3、网盘） | `sync.go` 里换 transport，包格式不动 | 引擎、UI |
| 新的界面区域 | `frontend/src/components/` 加组件，侧栏一级列表加一项 | Go 侧一行不用改 |

## 为什么把 globals 换成接口

0.9.x 之前是包级全局变量互相 hook（`SetSnippetActionHooks`、`SetExpanderForegroundGuard`、`triggerIndex` 全局 map）。
问题是：任何新功能都可能悄悄改到同一份全局状态，测试只能整体跑。
现在：

- 引擎持有 `*Host`，构造函数注入 → 测试里塞一个 `fakeHost` 就能跑完 9 个引擎用例（`internal/expand/expand_test.go`），不碰真实键盘
- 注入函数 `typeTextFn`、按键翻译 `runeFor` 都可替换 → 单测不需要真桌面
- 存储按域拆文件（store / snippet / snippet_host / snippet_actions / snippet_exec / template / sync / update），每个文件一个关注点

## 存储约定

- 片段的新增行为字段（大小写、保留缩写、程序名单）走 `setSnippetOptions()` 单独一条 UPDATE，
  **不再往那条大 INSERT 里加参数**——加字段时不用改 SQL 的所有占位符
- 老库通过 `ALTER TABLE ... ADD COLUMN`（错误忽略）自动补齐，不需要版本号迁移框架
