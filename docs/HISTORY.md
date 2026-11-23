# 历史、取舍与踩坑

> 这个文件回答三个问题：**当初为什么这么设计**、**哪一轮修过什么**、**哪些坑踩过一次别再踩**。
> 具体怎么分层、文件在哪看 [ARCHITECTURE.md](ARCHITECTURE.md)，逐版变更看 [CHANGELOG.md](CHANGELOG.md)。
> 原则：一轮审查/一次事故写一段就够，不要另开新文件堆着。

## 一、几个影响很大的决定

| 决定 | 理由 | 代价 / 边界 |
| --- | --- | --- |
| **统一条目模型**：条目只有一种，片段 = 带 `snippet` 能力的条目 | `items`/`memos`/`snippets` 三张表说的是同一件事，写三套 CRUD、三套搜索、三套同步配对 | 2.0 做了破坏性迁移（老表启动即删）；`kind` 回答"正文是什么"，执行语义另开 `snippet_kind` 列 |
| **不自动清理**（没有最大条数、没有保留天数） | 剪贴板历史的价值在"找得回"，静默删数据是这类工具最招骂的行为 | 库只长不消，得靠用户手动删；`ExpiresAt` 只为兼容旧数据保留 |
| **文件只记引用 + 失效检测**（不做实体归档） | 不复制用户文件、不碰隐私半径、删条目不用处理副本一致性 | 原文件被移动/删除后取不回内容。方案见 [FILE-STRATEGY.md](FILE-STRATEGY.md)，"一键归档"仍是 backlog |
| **WebDAV 打包同步：一台机器一个 zip** | 只写自己的文件 ⇒ 写冲突不可能发生；请求数与条目数无关；包内是**全量条目**（2.9.4 起，早先只带最新 500 条） | 每轮整包 PUT/GET（无增量），包大小靠 deflate、附件 96 MiB 预算、指纹跳过兜着；字段级冲突合并没做（整条按 `updated_at` 谁新谁赢） |
| **纯本地 + 可选同步**，没有账号与服务端 | 隐私边界清楚：不配 WebDAV 就一个网络请求都不发（除了更新检查） | 跨设备要靠用户自备 WebDAV；密码明文存 `settings.json`（DPAPI 在 backlog） |
| **界面自己做**（无边框、自绘标题栏、自带字体） | 和内容同一套 token，125%/150% 缩放不发虚 | Win32 边框/圆角要自己补，主题切换要重画 `ApplyChrome` |
| **一个 Wails 服务 + 40 个文件的单包** | 绑定路径稳定（挪包就要重生成绑定并装机验证），文件边界替代目录边界 | `internal/clip` 已经很大，拆 `internal/store` / `internal/model` 在 backlog |

## 二、审查与审计记录

| 时间 | 做了什么 | 主要结论 |
| --- | --- | --- |
| 2026-11-19 | 第一轮代码审查 | 清掉 8 处死代码与 4 类文档漂移；确认搜索已是 FTS5 trigram；记下 3 个"该拆没拆"的地方 |
| 2026-11-19 | 界面舒适化（comfort pass） | 暖白纸感主题（`default-light` 换米白面 + 赭石强调，原纯白版另存 `pure-light`）、正文 13→14px 且全局小字各上移一档、圆角 7/10/14/16、`--elev-card` 抬升、`--text-3` 提到 5.3:1、`--on-accent` 按预设区分实心按钮字色、主按钮与 logo 去渐变去光晕、写死的靛蓝 tint 全改 token；前后对照图与重跑脚本在 `scratch/ui-review/` |
| 2026-11-20 | 第二轮审查 + 主题体检 | 修"复制完不刷新"（主窗口没监听 `clip:new`）、草稿防抖搬进 Go、同步可见性、对比度 3.03→4.55 |
| 2026-11-20 | **功能审计**（本次） | 5 个"说了但不工作"的功能 + 存储/同步生命周期欠账 + 事件桥缺口；全部修复于 2.8.3，详见下节 |
| 2026-11-20 | 快速输入窗透明 | 窗口改真透明（圆角透桌面）+ 可选的卡片半透明；发现 WebView2 半透明必须配 DWM backdrop（见"踩过的坑"） |
| 2026-11-23 | 同步专项审查 | 真机取证（坚果云 PROPFIND + 本机库）查出：老库墓碑表主键不匹配导致**删除从来不产生墓碑**、PROPFIND 拿不到包大小/时间、坏对端包毒死整轮、失败不退避；修于 2.9.3 |
| 2026-11-23 | 同步包改全量 | 用户判断"≥500 条只同步最新那批"不可接受。去掉 `syncMaxEntries`、加 `Store.AllEntries()`；附件预算改成可轮转（`sync.blobDeferred`）；间隔默认 120 分钟、上限 1440。同时把"人类可读的文件存储"方案否掉了 —— 属性进 YAML 头这件事可以做，但"文件当真源"会牵动身份（现在是内容哈希）、墓碑键、字段分层与同步增量，见 CHANGELOG 讨论 |

> 两个没单独发版的轮次留了 commit 指纹，逐条变更去 `git show`：`b243017`（侧栏「待办」与筛选器不同时高亮、
> 托盘菜单精简到 6 项、`ui:navigate` 让「打开设置」真跳转）、`11428d3`（复制即见、草稿防抖搬进 Go、
> 同步可见性、主题对比度 3.03→4.55 —— 内容已并入 CHANGELOG 2.8.2）。

### 2026-11-20 审计修了什么（2.8.3）

审计方法是"文档声明 ↔ 代码实现"逐条对照。**教训：写进 README 的功能，必须在代码里能找到那条数据流**——
最容易漏的不是"没写"，而是"写了一半"（采集了字节但没落库、注册了事件但没人监听、算了状态但没人渲染）。

发现并修掉的问题分四类：

1. **数据断了半截**：私有格式的 `Captured.Binary` 采集出来没人写库 ⇒ 回填永远"原始数据已不可用"；
   文件失效状态后端每 3 分钟算一次、前端从没读过；`RestoreFilters` 只剩一行注释。
2. **方向反了**：同步包取"最新 500 条"写成取最旧（`EntryQuery` 少 `Tail: true`）；
   `peerHasBlob` 的键少了 `entry-` 前缀 ⇒ 对方的附件每次同步都重传。
3. **生命周期没人收尾**：删条目不清 `entry_blobs` / `entry_files/`；写入无事务（FTS 失败留下搜不到的条目）；
   删条目先写墓碑后删行（失败就分叉）；`Shutdown` 不等后台循环。
4. **前后端没接上**：7 个后端事件零监听（设置变更、同步状态、更新可用、删除、粘贴失败…）；
   两处设置只有后端字段没有界面（`captureHotkey`、`expandSuffix`）；两处文案还写着"改完点保存"。

## 三、踩过的坑（踩过一次就别再踩）

**编辑器 / 前端**

- **Vditor 一次建两套正文**：IR 与 wysiwyg 各有一个 `<pre class="vditor-reset">`，只有一套显示，
  而排在 DOM 前面那套是**空的** —— `querySelector(".vditor-reset")` 拿到空气，任何"按段落上色"
  的逻辑都会静默变成"什么也没做"（实测日志：`resets=[#0 空 kids=0 | #2 有字 kids=1]`）。
  取正文一律走 `isContentEditable` + `offsetParent` + 文字最多的那个（见 `lib/privacyMask.ts`
  的 `editorBody`）。
- **正文里的段落可能是"一个字面换行的 `<p>`"**：Vditor 的 IR 本体是 `<pre>`（`white-space:
  pre-wrap`），所以程序化灌进去的文本（比如"恢复上次未保存的草稿"）会是一个 `<p>` 里带着字面
  `\n`，而不是多个 `<p>`。只按块级元素分段的话整篇算一段、遮罩恒为空（实测 `total=1`）。
  分段要同时认"块级边界"和"字面换行"两种。
- **"光标所在的位置"有两种，别只按文字找**：只在**有文字**的片段里匹配光标，会让"刚按了回车、
  光标停在一个空段落上"这一刻找不到归属 —— 于是沿用上一段，界面要等到敲下一个字才更新，
  用户看到的是"回车没反应"。现在"光标在编辑器里但所在段落没有文字"是一个独立状态
  （`NO_READABLE`）：当前段落没字 ⇒ 其余有字的段落全遮。锚点是元素（点在行尾空白）走另一条分支：
  那段里有字就归到它的最后一段。
- **回填"图文混排"必须同时给三份格式**：条目正文里的图片只是 `memoimg:<hash>` 令牌，
  只写 CF_UNICODETEXT 等于把 `![](memoimg:…)` 直接丢给别人。CF_HTML（data URI）、RTF（`\pict`）、
  纯文本三份一起放，目标程序各取所需；少一份就有一类程序拿不到图。
- **RTF 的 `\pict` 要按"令牌版 HTML"取字节**：`embedMemoImages` 之后的 src 已经是 data URI，
  拿它去 `pngs[hash]` 查必然空 —— 图片段落会变成一个空行（实测踩过）。两份 HTML 各用各的。
- **写 HTML/RTF 会破坏回声抑制**：采集侧读回来算的是 `htmlHash`/`rtfHash`，而条目自己的
  `entryEchoHash` 是 `textHash` 口径 —— 哈希永远对不上，于是"复制一次多一条采集记录"。
  程序化写富文本之后要开一个很短的"一律丢弃"窗口（1.2 秒，`WM_CLIPBOARDUPDATE` 是毫秒级到达的）。
- **RTF 的 `\uN` 是十进制**（不是十六进制）：`\u30002?` 才是"甲"。写成 `\u7532?` 在 Word 里是乱码。
- **别拿 Unicode 字形当图标**：`⌄` `⌃` `‹` `›` 这类字符来自回退字体（Inter / Noto Sans SC 里
  没有），12px 下又小又偏下、粗细也和界面那套 1.8 描边图标对不上；`⌃`/`⌄` 的度量还不一样，
  按钮在两个状态之间高度会跳。要箭头就用 `Icons.tsx` 的 `IconChevron` + CSS `rotate()`
  （折叠控件 180°、左右导航 ∓90°）。**正文里的键位说明（`↑↓ 选择`）不算图标，别动**。
- **折叠控件没有箭头等于没有入口**：待办页「展开已完成（N）」以前纯文字，用户根本不知道能点。
  凡是"点一下改变可见性"的按钮，视觉上必须有个可翻转的指示物。
- **`#标签` 与隐私遮罩抢同一段文字**：两种上色都用 `CSS.highlights`，重叠时后注册的赢。
  现在的顺序是先遮罩再标签，并且标签高亮**主动跳过**被遮住的文字 —— 否则灰雾里会透出一块
  强调色，等于把"这里有标签"漏出去。
- **GUI 自动化的鼠标点击不可信**：`SetForegroundWindow` 之后点进 contenteditable，脚本里看起来
  点了，但没有产生选区（日志里 `anchor=-`），于是"光标所在段落"永远不更新。**而人真敲键盘时
  选区一定在**。结论：验证"跟着光标走"这类行为要用打字（ASCII + 回车）而不是点鼠标；另外
  人在这台机器上同时打字会直接串进自动化窗口 —— 跑 GUI e2e 前先跟用户确认别碰键盘。

- **常驻的弹窗窗口不会自己"再读一次"设置**。快速粘贴窗与「记一笔」都是随应用创建、一直活着的
  窗口，各自只有 `main.tsx` 里那一发 `applyTheme(storedTheme())`；于是设置页换主题时主窗口变了、
  弹窗还是老配色（2.9.0 发布后被指出来）。现在主题落地只有一个入口
  `hooks/useTheme.ts`：主窗口用 `useTheme(settings)`，弹窗用 `useSettingsTheme()`（自己
  `GetSettings` + 订阅 `settings:changed`）。新增常驻窗口时照着办，别再各自拼一份。
- **浅色主题下"半透明"可能看不出来**：卡片是 `--surface`（米白）的 alpha，浅色模式下 DWM 的
  Acrylic 层本身也接近白，背后再是一份白文档就是白叠白 —— 实测只差 7 级。这不是开关没生效，
  换深色/彩色背景立刻能看出（蓝色背景下差 36 级）。要验证得用**有颜色的**背景
  （`scratch/e2e-capture-transparency.ps1 -Backdrop '#1e6fd9'`），拿白窗量会得出"没生效"的错误结论。
- **窗口类型必须是 `Translucent`，不是 `Transparent`**（两者 exStyle 完全相同，都是
  `WS_EX_NOREDIRECTIONBITMAP`，差别在 Wails 建窗时要不要动 DWM 组合属性）。实测换成
  `Transparent` 后，页面里 alpha=0 的地方会露出 **WebView2 的白底**（卡片区域量到 `255,255,255`），
  而 `Translucent` 能让同一处透出桌面 —— 圆角切边靠的就是这条。
- **Acrylic 开着时，alpha=0 看到的是 Acrylic 本身**：所以「弹窗透明」拉到 100% 是"卡片不再画底色，
  露出磨砂玻璃层"，白底上 `146,146,146`、蓝底 `21,102,208`（背后 `27,101,197`），而不是背后窗口的
  原样像素。想做到"文字直接浮在桌面上"在这套栈里没有解，别再试。
- **WebView2 只给"全透明"，不给"半透明"**。「记一笔」浮窗是透明窗口（`WS_EX_NOREDIRECTIONBITMAP`），
  页面里 alpha=0 的像素确实能让桌面透出来（圆角切边就靠它）；但 alpha=0.7 的卡片底色**不会**与桌面
  混合，而是被合到一层黑底上 —— 实测卡片是 `rgb(13,14,19)`，比实心的 `rgb(14,16,21)` 还暗。
  必须先用 `DwmSetWindowAttribute(DWMWA_SYSTEMBACKDROP_TYPE=Acrylic)` 给窗口垫一层 backdrop，
  半透明像素才与它混合（同一张卡片变成 `rgb(54,55,59)`）。所以「弹窗透明 > 0」= 顺手开 backdrop，
  `0` = 关掉（圆角才是干净切边，也不为看不见的效果付合成开销）。顺带记一笔：Wails 的
  `WebView2CompositionHosting: true`（visual hosting）在本机直接**整窗不渲染**，不是这条路的解法。
  验证脚本 `scratch/e2e-capture-transparency.ps1` 是按像素量的，不靠截图印象。
- **Vditor 的 `options.input` 在 IR 模式手打时不回调**（只有 `setValue` 类路径才响）。内容更新要听宿主元素的
  DOM `input` 冒泡事件 —— 草稿、脏标记全依赖这条。2.8.0 就是因为这个发布十分钟被撤下。
- **在异步回调里比"token 对象引用"会永远判过期**：标签联想的候选清单是异步拉的，
  早期写法在 `.then` 里比较 `tokenRef.current !== token`，可每敲一个键都会生成新的 token
  对象，于是结果永远被当成过期丢掉 —— 弹窗一次都不出现。正确做法是**缓存下来后重新算一遍**
  （`refreshSuggestRef.current()`），让"当前光标"重新参与判断。
- **`insertValue` 不派发原生 `input` 事件**：我们的"内容变了"信号来源是宿主元素上的 DOM
  `input` 冒泡（草稿落库、脏标记都挂在它上面）。用 `Vditor.insertValue` 插文本之后必须
  **手工调一次 `onChange`**，否则界面看着对、库里草稿是空的（标签联想实测踩到）。
- **Vditor 的 IR 正文本体自己就是一个 `<pre class="vditor-reset">`**：判断"我在不在代码块里"
  只能看 `<code>`，按 `pre` 排除等于把整个编辑区排除掉（标签高亮第一版 ranges 恒为 0 就是这个）。
- **给纯文本上色别往 DOM 里插节点**：IR 模式里 `#标签` 就是普通文本，包 `<span>` 会打断光标、
  弄乱 Vditor 的结构、撤销也乱。用 CSS Highlight API（`CSS.highlights` + `::highlight()`）
  只注册范围，能上颜色/背景（圆角内距这些不生效），对编辑零干扰。
- **Vditor 会在插入后异步重排，并把原生选区重置回它记录的旧范围**：`insertValue` 之后立刻
  设光标没用（实测 1.5s 后又跑回 `#tag` 前面）。要么在多个时间点补落（并且用户一动就停手），
  要么别指望一次就定住；另外重排后**末尾那个空格会被拆到别的文本节点**，查找串别带空格。
- **点弹层时先把焦点要回编辑器**：`execCommand` 只作用于"当前文档的焦点选区"，点候选
  （哪怕 mousedown 已 preventDefault）焦点也可能飘走 —— 那样 `delete` 会删错、`insertValue` 落空。
- **输入法组词期间不许拦键**：`keydown` 里遇到 `ev.isComposing || ev.keyCode === 229`
  一律放行，否则中文候选按 Enter 确认不了。组词结束（`compositionend`）再重新算联想。
  同理，`selectionchange` 里别先判断"弹层是否已开"就 return —— 那样"用鼠标点回一个写好的
  `#标签`"永远不会触发联想。
- **CSS 变量名写错不是"回退成默认色"，是整条声明失效**：标签联想第一版用了不存在的
  `--surface-1` / `--line` / `--text-1`，于是 `background` / `border` 整条被丢弃，弹窗变成透明、
  和背后界面糊在一起。改完样式先确认变量在 `base.css` 里真的有定义（`--surface` /
  `--surface-2` / `--surface-3` / `--border` / `--border-strong` / `--text` / `--text-2` / `--text-3`）。
- **离线回放 UI：注入本身有效，错的是"人同时在动"**。点击进输入框再打字、点候选、截图全都生效；
  几次"输入没进窗口 / 字符少一个"都是因为有人在那个窗口上操作（用户自己删了一个字母，我当成了
  代码 bug，白改了一轮）。两条规矩：① 回放前先说一句"别动那个窗口"；② 更稳的是走**确定性
  路径**——把数据预先写进库（`scratch/setdraft` 写草稿、`seedtags` 造带标签的条目），启动后
  只点两下，不需要打字也不受输入法影响。纯逻辑（识别/过滤/替换）另外用
  `node --experimental-strip-types scratch/tag-suggest-check.mjs` 跑，完全不碰界面。

- **两处都监听 window `keydown` 时 `stopPropagation` 没用**，同一节点上的其他监听照样执行；
  要么 `stopImmediatePropagation`，要么像 `Dialog` 那样导出一个 `isDialogOpen()` 让下层自己让位。
- **`memo()` 比 props 时别把箭头函数算进去**：上层每次渲染都新建闭包，比进去等于白 memo；
  只比"数据引用 + 展开态"这类真正影响渲染结果的东西。
- **列表请求要有序号**：切筛选器/连打搜索时先发的可能后回来，老响应会把新结果顶掉。
- **后台刷新别整页替换**：用户可能正翻着更早几页，只能把新页并进去（`mergePages`）。
- **CSS 的 import 顺序就是层叠顺序**（`main.tsx` 里有注释）。删样式前先 grep 类名 —— 有一批像
  `.app.is-memos` 这样的类**永远不会被渲染**（`App.tsx` 只输出 `is-entries`），改它等于白改。
- **Vditor 自带深色硬编码**，深色主题必须靠 `vditor-theme.css` 的几条 `!important` 压住。

**后端 / 数据**

- **"派生值"该不该落库，取决于它会不会被人改写**：`todo_state` 落了库（筛选器要按它过滤），
  `todoTotal/TodoOpen` 只在内存（纯展示）。标题两者都占：卡片/待办/同步都要用（不该每次读都扫正文），
  又要允许人手写覆盖（得记住"这是手写的"）。所以 `title` 存当前生效值、`title_source` 存来源，
  规则集中在 `normalizeTitle`。**当你想加第二个"可被用户覆盖的派生字段"时，照这个形状做。**
- **同一个标题在两个地方渲染 = 显示两遍**：标题从正文 H1 派生之后，卡片顶部（`memo-card-title`）
  和正文渲染出来的 `<h1>` 是同一句话，第一版截图就是"会议纪要"叠了两行。现在只在
  `title_source === "auto"` 时把正文里第一个 H1 摘掉再渲染；手写标题不动正文（那才是内容）。
- **`#标签` 和 `# 标题` 只差一个空格**：派生标题必须用 CommonMark 的 `^#\s+`，不能用
  `^#`；否则 `#工作 今天做完` 会被当成标题。同理代码块（围栏 + 缩进 4 空格）里的 `# xxx` 是注释不是标题。
- **OCR 的引擎选择与三个坑**：目标引擎是 Windows 自带的 `Windows.Media.Ocr`（离线、零依赖）。
  接入方式比较过三条：Go 直接调 WinRT（`IAsyncOperation` 要手搓 COM vtable，成本远大于收益）、
  C# sidecar（`.NET Framework 4.8 + WinRT` 需要 Windows SDK 的 UnionMetadata 才能编译，本机只有
  .NET Core 版 winmd，csc 直接报 `System.Runtime 缺引用`）、**PowerShell 5.1**
  （Win10/11 自带，最终选的这条）。三个坑：① WinRT 类型投影的**第二个参数必须是契约程序集名**
  （`OcrEngine` 在 `Windows.Media.Ocr`，写 `Windows.Foundation` 有时能过有时 `TypeNotFound`）；
  ② 脚本**必须纯 ASCII**（PowerShell 5.1 按 ANSI 读命令行，混一个多字节字符就可能整段解析失败），
  所以路径走环境变量、脚本用 `-EncodedCommand`；③ 控制台默认代码页会把中文吞成 `?`，
  要在脚本里把 `[Console]::OutputEncoding` 设成 UTF-8。
- **中文空格合并别用 `\s`**：系统 OCR 会给每个汉字之间塞空格（`坚 果 云`），要合并；但
  `\s` 包含换行，会把两行中文粘成一行（测试 `TestNormalizeOCRText` 抓到的）。
- **onnxruntime 的环境是进程级单例**：`InitializeEnvironment()` 只能成功一次，再调就报
  `The onnxruntime has already been initialized`。踩法很阴：设置页做"引擎可用吗"的状态检查时
  先初始化了环境，真正识别时第二次初始化失败 —— 而代码里是"PP-OCR 失败就退回系统引擎"，
  于是**看起来一切正常，实际一直在用低精度引擎**（`scratch/ocrcmp` 里两个引擎输出一模一样才发现）。
  现在用包级 `sync.Once` 兜住。
- **DB 后处理里的 unclip 别按最长边乘比例**：`grow = max(w,h)*(ratio-1)/2` 会把一条 600×20 的文字行
  撑成 200 高，切出来全是背景，识别模型当然什么都读不出（表现为"框巨大、结果空"）。
  PaddleOCR 的算式是按 `面积×ratio/周长` 推一个**统一**外扩距离。
- **可选功能别绑死编译链**：PP-OCR 需要 cgo。把它做成独立包 `internal/ppocr`，真实现放
  `engine_cgo.go`（`windows && cgo`），`engine_stub.go`（`!windows || !cgo`）给同签名空实现 ——
  没有 gcc 的机器仍能编译、能用系统引擎。这比"在 clip 包里加 cgo 依赖"稳得多。
- **大文件下载在这台机器上要靠镜像**：GitHub Release / NuGet CDN 走代理都反复截断
  （`schannel: server closed abruptly`），onnxruntime 的 win-x64 DLL 最后是从
  **清华 PyPI 镜像**下 `onnxruntime-1.32.0-cp311-cp311-win_amd64.whl` 解出来的（`pip download -i https://pypi.tuna.tsinghua.edu.cn/simple`）。
  下次再要这类二进制，先试镜像。
- **能抽成纯函数的规则就别留在组件里**：右键菜单"哪条内容该有哪些操作"（图片才有 OCR、
  识别过才显示复制识别文字、删除永远在最后）放在组件里就只能靠手点验证；抽成
  `lib/entryMenu.ts` 之后 `scratch/entry-menu-check.mjs` 一条条断言，回归时不用动鼠标。
- **写派生数据不能碰 `updated_at`**：OCR 结果属于派生数据，而 `updated_at` 是"内容变过"的等价物
  （同步指纹、时间线排序、冲突仲裁都靠它）。改它会让开机后的批量识别看起来像"所有图片刚被编辑"。
- **两三个字的检索词要照顾到**：`entries_fts` 是 trigram 分词，**少于 3 个字用不了 MATCH**，
  `EntryQuery` 因此对短词退回 `LIKE`。加了 `ocr_text` 列之后，那个 LIKE 分支必须把它一起带上，
  否则"搜图片里的两字词"永远搜不到。

- **时间戳必须定宽**（`2006-01-02T15:04:05.000000000Z`）：所有排序、游标、墓碑比较都基于字符串比较，
  变宽的小数位会让"同一微秒"的行顺序飘。
- **哈希有两个口径**：采集口径（`textHash`/`htmlHash`/`filesHash`，用于去重与回声抑制）与内容口径
  （`contentHash`，用于同步配对）。写回剪贴板要用 `entryEchoHash`，拿 `entry.Hash` 比永远不相等。
- **`ON CONFLICT(id)` 的 upsert 不等于"同内容只有一条"**：两台机器各自采集同一段文字会得到同 hash、
  不同 id 的两行。合并前必须按 hash 找到本地行、**沿用本地 id**。
- **`ListEntries` 默认是 ASC（最旧优先）**：要"最新 N 条"必须显式 `Tail: true`。
- **"取全量"不能用 `EntryQuery` 的默认行为**：`where()` 永远带 `archived = 0`，所以拿
  `ListEntries` 组包时**归档条目一条都进不了包**（真机 12 条里 3 条归档，从没同步过），
  而另一台机器上它们照样能被搜到、照样出现在快速粘贴窗。全量走 `Store.AllEntries()`。
  同理，`CountEntries` 是"界面上看得到的条数"（不含归档），判断"本地是否为空"要用
  `CountAllEntries` —— 混用会出现"全都归档了 ⇒ 判成空库 ⇒ 拒绝上传"。
- **组包不能用 `ListEntries`**：它有一条 500 的硬上限（`limit <= 0 || limit > 500` 都回落成 100），
  那是给界面分页的保护。拿它组包 = 库被悄悄截断：超过 500 条之后更老的历史**永远上不了云**，
  而界面还写着"同步成功"。要全量就走 `Store.AllEntries()`（不带 LIMIT 的单独查询，只给同步用）。
  2.9.4 之前就是这么错的：从"取最旧 500 条"修成"取最新 500 条"只是换了个错法。
- **预算型的截断必须能轮转**。附件按 96 MiB 预算挑选时，如果每轮都从同一个方向扣（按条目顺序一路
  扣到 0），排后面的附件**每轮都被扣光**，用户看到的是"有些图片就是同步不过来"，而日志里写的是
  "下轮继续"——永远等不到那个下轮。现在没发出去的记进 `meta` 的 `sync.blobDeferred`，下一轮优先发；
  单个附件超总预算的单独计数并明说"永远发不出去"，不许它一直占着挂账名额。
  挑选逻辑抽成纯函数 `planBlobUpload` 才测得了：96 MiB 的预算在单测里造不出来，用几十字节测。
  **已知边界**：这个设计靠"对端索引告诉我们它已经有这些附件"（`peerHasBlob`）来收敛。所以
  "附件总量超预算 + 对端长期不同步"时，本机每轮会重推一个内容略有不同的包（挂账集合在两组之间
  来回）。对端同步过一次就立刻收敛（候选集清零）。真要彻底消掉，得有"已确认上传"的显式确认表 ——
  现在不值得，因为触发条件是"附件 >96 MiB 且对面几小时不开机"，日志里 `sync: pushed` 会如实显示。
- **两个不同的键别合并成一个**：包内名字是 `entry-<条目哈希>.png`，库里取字节的键是
  `memoimg:<附件哈希>`（或私有格式的采集哈希）。第一版把 zip 名字直接喂给 `LoadEntryBlob`，
  于是"附件一个都装不进包"，`TestSyncPackagesEverythingIntoOneFile` 当场红。
- **清空/批量删除要走单事务**：逐条 `DeleteEntry` 是上千次 autocommit，界面像卡死。
- **同步合并必须用 `mergeEntry`**（保留远端时间戳）；用 `SaveEntry` 会把 `updated_at` 盖成"现在"，
  本地副本比墓碑还新，删除会被静默忽略。
- **`ON CONFLICT(...)` 的冲突目标必须真的是主键/唯一约束**，否则 SQLite 会**整条语句报错**。
  2026-11-23 的实锤：老库的 `tombstones` 还是 `PRIMARY KEY (hash)`，而写墓碑用的是
  `ON CONFLICT(hash, scope)` —— 语句永远失败，错误又被 `_, _ =` 丢掉，于是**删除在本机成功、
  对端毫不知情**（界面上删了 17 条，`tombstones` 一条没多，远端包里的 `deleted` 也没变）。
  两条规矩：① `CREATE TABLE IF NOT EXISTS` 不会改已有表，凡是"老库也要能跑"的表结构变更
  都得写显式迁移（`store.go` 的 `migrateTombstoneSchema` 就是样板：RENAME → 建新表 → 搬数据
  并归一化 `scope` → DROP → 补索引）；② 写路径上的错误不许静默丢，墓碑写不进去就让删除回滚。
  **单测全绿也抓不到这一类**：测试库每次都是新建的，schema 永远是最新的 —— 必须专门造一个
  老 schema 的库来跑（`store_migrate_test.go`）。
- **侧栏角标和列表不能各写一套 SQL**：`EntryStats` 原来手写 `actions LIKE '%,clipboard,%'`
  当"剪贴板"，而「剪贴板」筛选器走 `where()` 编译出来的 `action`；两边再各自演化一次，就出现
  「我写的 3 + 剪贴板 2 ≠ 全部 6」。现在角标**直接复用 `BuiltinFilters()` 的 Query**，
  数字与"点进去能看到的条数"由同一个编译器产出（`filter_stats_test.go` 钉死）。
  教训：凡是"同一份语义算两遍"的地方，第二遍迟早会漂。
- **界面上能改数据的写路径必须发事件**：侧栏角标只在收到 `clip:new/bumped/updated/deleted/
  cleared/stats` 时才重取（`useAppData.refreshMeta`）。`SaveEntry` / `SetEntryPinned` /
  `SetEntryArchived` / `SetTodoDone` 以前一个都不发，于是"手写几条、置顶、归档"之后数字
  一直停在启动那一刻。新加写路径时顺手想一句"界面靠什么知道它变了"。
- **`actions` 是能力位，不是来源**：快速粘贴窗（`Picker.tsx` 的 `action: "clipboard"`）只看这一列，
  而它有历史欠账（老模型写进来的行是空的）。按"来源"筛内容要用 `source`，按"能不能出现在
  粘贴窗"才用 `actions` —— 混着用就会出现"复制过却在窗口里找不到"。启动时有一次幂等回填。
- **PROPFIND 的 props 要自己列**：只请求 `<resourcetype/>` 时坚果云只回 resourcetype，
  `getcontentlength` / `getlastmodified` 一概没有 —— 设备表于是永远是 `0 B` / `—`，
  而这两列正是排查"对方到底有没有上传"的关键。要 size/mtime/etag 就得写进请求体。
- **一个坏的对端包不能中断整轮同步**：以前 `merge` 直接 return，连带把"上传自己那一包"也拦了，
  于是有一台机器留下半截 zip（或旧布局文件）就谁都同步不了。跳过 + 记账才是对的。
- **失败要有退避**：只在固定间隔跑，等于"开机那一枪失败 = 白等一个间隔"。
- **组包阶段别做有副作用的清理**（如裁剪墓碑）：它改变下一轮指纹，空闲机器会白推整包；
  真要裁剪就放在**组包之前**，让指纹如实反映最终内容。
- **内容寻址的附件要回收**：删条目时扫一遍正文里的 `memoimg:` 令牌（私有格式按 `Entry.Hash` 引用），
  没人引用的行与文件一起删。

**平台 / 构建**

- **`EmptyClipboard` 之后任何失败都会让用户的剪贴板变成空的**：先确保格式已注册（`initFormats()`）
  再动剪贴板；`GlobalAlloc` 的句柄在 `SetClipboardData` 失败时记得 `GlobalFree`。
- **Wails 事件要先 `application.RegisterEvent[T]` 才会被前端收到**，否则只有一条 "event not registered" 警告；
  前端用 `frontend/bindings/events` 里的常量名，别手写字符串。
- **重新生成绑定用 `wails3 generate bindings -ts -i -d frontend/bindings`**（`-ts` 出 `.ts`、`-i` 出 interface
  形态，与仓库里的一致；不加 `-ts` 会生成 `.js` + `.d.ts`）。
- **e2e 脚本要先停掉安装版**：SingleInstance 会让沙箱实例直接退出。剪贴板用例在 app 运行时也会 skip。
- **NSIS 脚本是 UTF-8 带 BOM**，改它别把 BOM 弄丢。

## 四、还没做的事（一处汇总）

产品级 backlog，都不是 bug：

- 文件「归档到库」（FILE-STRATEGY 方案 E：手动把实体拷进 `entry_files/` + 配额）。
- 同步的字段级冲突合并、同步凭据 DPAPI 加密、首次同步的进度事件、"最近一次同步改动了哪几条"。
- 按来源程序自动分类打标签；图片压缩与缩略图缓存的落地（现在只在读取时降采样）。
- `CF_BITMAP` / `CF_METAFILEPICT` 走 `GetDIBits`；Outlook 虚拟附件 `FileGroupDescriptorW`。
- 注入通道回退链（`WM_CHAR` 直投）与"按程序指定通道"；片段填参 `{{ask:标题}}`、撤销上次替换。
- 待办到期/提醒/重复任务/子任务树；筛选器拖拽排序、导出导入。
- 拆包：`internal/store` + `internal/model`，`*_windows.go` 全量进 `internal/winplat`（会动绑定路径，一次一层）。
