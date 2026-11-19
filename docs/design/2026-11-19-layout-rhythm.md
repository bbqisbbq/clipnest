# 2026-11-19 布局节奏：一条阅读列 + 主区留白

> 需求原话：「布局优化 / 美化，增加合适的 padding、margin 等等，让各个区块布局优美」。
> 改完的结论：不是"到处加 padding"，而是**先定一条列、再把每个区块对齐到它**。

## 1. 先量，别猜

改了 9 个样式文件，凭什么说"变好看"？先做一样东西：**整窗标本页**。

`scratch/layout-preview/` 里有两个脚本（不参与打包）：

| 文件 | 干什么 |
| --- | --- |
| `mock.mjs` | 按 `App / TitleBar / Sidebar / EntryCard / EntryDetailView / TodoView / SettingsView / Picker / Capture` 的真实 DOM 结构拼静态 HTML，套上 `vite build` 产出的**真 CSS 与真字体** |
| `snap.mjs` | headless Chrome 截图：entries / detail / todo / settings / 两个窄窗 / picker / capture |
| `probe.mjs` | 同一批页面里读 `getBoundingClientRect()` 与 computed padding，输出数字 |

```
cd frontend; npx vite build
node scratch\layout-preview\snap.mjs --tag before    # 改之前
node scratch\layout-preview\probe.mjs entries        # 量尺寸
```

为什么不用 `wails dev` 截真窗口：标本页是确定性的，同一份 DOM + CSS + 字体，
改前改后能直接对比；而且一屏能看全部页面。Vditor 的内部排版它当然量不到（那部分只
受 `.md-surface` 外层 padding 影响），所以验证 CLI 那几条照跑。

## 2. 根因：主区有三套左边缘

量出来的原始数字（1280 宽窗口，面板 1026 宽）：

| 区块 | 卡片列 | 左边缘 |
| --- | --- | --- |
| 搜索框 | 撑满面板（padding 14px） | 251 |
| 时间线卡片 | `max-width: 760` 居中 | 365（还有滚动条 9px 导致的抖动） |
| 输入卡片 | `max-width: 760` 居中 | 370 |
| 待办行 | `max-width: 860` 左对齐 | 249 |
| 设置卡片 | `max-width: 720` 居中 | 258（**而且整个 main 被压成 480 宽**） |
| 详情正文 | 无上限 | 252，一行能到 900px 以上 |

一眼看过去"不整齐"，不是因为留白不够，而是**每条视图的列宽和起点都不一样**：
切换筛选器 / 待办 / 设置 / 详情时，左边缘会横向跳 5～120px。

## 3. 做法：一个 token 管到底

`styles/base.css` 加了三个变量（其余样式文件都只用它们，不再各写各的数字）：

```css
--gutter: 20px;      /* 窄窗口下的最小左右留白 */
--read-w: 760px;     /* 阅读列宽 */
--read-pad: max(var(--gutter), calc((100% - var(--read-w)) / 2));
```

`--read-pad` 是"居中留白"，直接当 `padding-inline` 用：面板够宽时它等于两边的留白，
面板变窄时退化成 `--gutter`。这样**列宽上限和最小留白是同一条公式**，窄窗口不用写断点。

落地到各处：

| 位置 | 改法 |
| --- | --- |
| `.memo-head` / `.todo-head` / `.todo-body` | `padding-inline: var(--read-pad)`，搜索框、待办行的左边缘 = 卡片左边缘 |
| `.memo-timeline` | 同上（**不用 `margin: auto` 居中**：滚动条占掉的 9px 会让列心左移，左边对不上头部的搜索框） |
| `.memo-day` / `.composer-box` | `max-width: var(--read-w)` |
| `.detail-editor` | `padding-inline: max(var(--gutter), (100% - var(--read-w)) / 2)` |
| `.settings-bar` | 居中留白 **+ 24px**（卡片外面还有 24px 内边距），和卡片左边缘对齐 |

## 4. 顺手修掉的真 bug

1. **设置页被压成 480px 宽**（最明显的一处）。`.main-pane` 是 `display: flex`，
   `<main class="main is-settings">` 没有 `flex: 1`，于是按内容宽度收缩：卡片只有
   480 宽、右侧一片空白，底部保存条也跟着缩。加 `.main-pane > .main { flex: 1; min-width: 0 }`。
2. **`--text-dim` 是个不存在的变量**（picker / snippets 共 4 处）。`color: var(--text-dim)`
   整条声明失效 → 那些"次要文字"亮成了正文色。在 `base.css` 里补一个别名
   `--text-dim: var(--text-3)`，不动调用点。
3. **侧栏日历和导航项不在一条竖线上**：`.side-sub` 有 `padding-left: 10px`，日历只有 8px。
   去掉这层缩进，让 `side-scroll` 的 10px 成为唯一竖向参考线（层级靠字号区分，不靠缩进）。
4. **快速输入窗的 × 和提示文字之间空出一大块**：`timeline.css` 里的旧规则给了
   `.capture-close { margin-left: auto }`，`editor.css` 又给了 `.capture-keys { margin-left: auto }`，
   两个 auto 把空白劈成两半。收回到 0。

## 5. 节奏（这次的 padding/margin 数值）

4px 网格，垂直方向按"组"给空间，不是每条都加：

| 区块 | 之前 | 现在 |
| --- | --- | --- |
| 标题栏 | `0 6px 0 12px`，gap 10 | `0 8px 0 14px`，gap 12；窗口按钮 36×28 |
| 侧栏滚动区 | `8px` | `10px 10px 18px`；分组标签 `16px 8px 6px`（首个 `6px`） |
| 侧栏项 | `6px 9px` | `7px 9px`（二级 `6px`）；行间距 1px |
| 日历 | `margin 4px 8px 10px`，格子 24px | `margin 2px 0 14px`，格子 26px、间隙 3px |
| 列表头 | `10px 14px` | `14px var(--read-pad) 12px`，搜索框 `6px 10px` |
| 分组标题 | `6px 0 8px`，组间距 14 | `10px 0`，组间距 18（首组 6） |
| 卡片 | `12px 14px`，间距 10 | `14px 16px`，间距 12；元信息 `margin-top: 10px` |
| 输入区 | `14px 24px 16px` | `16px var(--gutter) 18px`；底部按钮行 `8px 10px 9px 14px` |
| 详情头 | `7px 14px` | `10px 18px`；正文左右走阅读列；右栏 300px（≤1180px 收成 264px） |
| 待办行 | `5px 8px` | `7px 10px`，勾选框 16px |
| 设置卡 | 头 `11px 14px 10px` / 体 `2px 14px 10px` | 头 `13px 16px 12px` / 体 `4px 16px 12px`；字段行 `9px 0` |
| picker 行 | `5px 8px`，图标 23px | `7px 9px`，图标 26px |

## 6. 改哪里

- 想调整体列宽：只改 `base.css` 的 `--read-w`（时间线、输入区、待办、详情正文会一起动）。
- 想调窄窗口的最小留白：只改 `--gutter`。
- 新加一个主区页面：给它的头部/滚动区用 `padding-inline: var(--read-pad)`，
  内容 `max-width: var(--read-w)`，别再写 `margin: 0 auto`（滚动容器里会和头部差 9px）。
- 改完跑一遍 `snap.mjs` 看眼，再跑 `scripts/visual-check/check-css.ps1` 确认折叠高度没变。
