# 全局输入框 · 导航与设置自动保存（2.8.0）

> 这一轮改的都是"用着别扭"的地方，不是新功能：输入框会消失、草稿会丢、设置要手点保存、
> 日历横向溢出。结论先写在这里，实现细节看 `docs/ARCHITECTURE.md` 的文件表。

## 1. 输入框从"视图的一部分"变成"窗口的一部分"

**问题**：底部「记一条」挂在 `EntriesView` 内部，于是

- 切到待办 / 设置，整块输入框消失（那两屏根本没有它的位置）；
- 草稿是 `EntriesView` 的本地 state，而 `EntriesView` 在设置/待办时**会被卸载**，
  切回来就是空白——用户以为"我写的东西没了"。

**改法**：主区从"一格"改成"两格竖排"。

```
.main-pane  (flex column)
├── .pane-body        当前视图（entries / todo / settings / detail 覆盖层）
└── .memo-composer    全局输入框（ComposerDock）—— 每一屏都在同一个位置
```

- `Composer.tsx` 只管长相（高度、拖拽手柄、按钮）；
- `ComposerDock.tsx` 管行为（存哪儿、存完通知谁）；
- 草稿状态在 `hooks/useComposerDraft.ts`，挂在 `App` 上，**不随视图卸载**。

详情页是唯一不显示输入框的地方：那是在改一条已有的条目，再摆一个"保存"在下面只会让人点错。

### 1.1 顺带挖出来的真 bug：Vditor 的 `options.input` 在手打时不回调

做完第一版、跑真机 e2e 时发现草稿永远是空的：文字在编辑器里，`meta.composer.draft` 却没写进去。
排查结论——Vditor 的 `options.input` 只在它自己的渲染流程里（`processAfterRender` / `setValue`）
被调用，**IR 模式下手打文字不触发**；只有"清空""换文档"这类走 `setValue` 的路径才回调。
（旧版没暴露是因为「保存」按钮读的是 `editorRef.getValue()`，而不是上层 state；但详情页的
「未保存」标记其实一直有这个隐患。）

修法：`MarkdownSurface` 在自己的宿主元素上听冒泡的 DOM `input`（外加 `blur` 补一次），
取 `getValue()` 后回调给调用方。这样"内容变了"对上层是可靠的，与 Vditor 的内部流程解耦。

## 2. 草稿落在库里，而不是前端的 localStorage

`meta.composer.draft`（外加 `composer.draftAt` 记时间）。理由：

| | localStorage | 库里 meta |
| --- | --- | --- |
| 跟着 `clipnest.db` 备份/换机 | ✗ | ✓ |
| 清 WebView 缓存 / 换安装位置 | 丢 | 不受影响 |
| 用户能自己找到 | ✗（藏在 EBWebView 底下） | ✓（就是那个数据库） |

写盘策略：敲字只更新内存，停手 400ms 写一次；窗口失焦 / 隐藏 / 卸载前补一次（`flush`）。
**存成条目后 `SetComposerDraft("")`**，重启不会把已经保存过的内容又塞回输入框。

草稿不进 `entries`、不进同步包：半成品不该被当成历史，也不该同步到别的机器。

## 3. 导航：默认筛选器 + 关设置回原处

- `Settings.DefaultFilterID`（机器本地，`localSettingsFields` 里清）。筛选器本身不跨端同步，
  一个指向别的机器上才有的 id 的"默认筛选器"只会让那台机器启动到空页。
- 启动时只认一次默认值（`defaultApplied` ref）：之后用户点来点去不该被拽回去。
- `⋯` 菜单里「设为默认打开 / 取消默认打开」，行尾显示「默认」小标（描边小字，不抢计数的位置）。
- 进设置页前把"现在停在哪一屏"记进 `settingsReturn`；顶部 `← 返回`、再点一次侧栏「设置」
  都回到那里。筛选器与选中日期本来就没被清掉，所以回去就是原样。

## 4. 设置自动保存

没有「保存/撤销」了。`SettingsView` 里的做法：

- `patch()` 记两件事：本地显示值、以及**这次动过哪些字段**（`touched`）；
- 停手 400ms 把 `{...最新服务端设置, ...touched}` 交给后端（**合并**，不是整对象覆盖）；
- 合并这一步是必须的：侧栏刚设的默认筛选器、或同步线程刚拉回来的对端设置，
  不能被一份"打开设置页那一刻的快照"盖掉；
- 顶部一行状态：`改动会自动保存` / `保存中…` / `已自动保存 14:32` / `保存失败，稍后会自动重试`。
- 失败时改动放回 `touched`，下次编辑或离开设置页再试。

## 5. 日历横向溢出（真 bug，不是审美）

现象：侧栏日历右侧被切掉（15 / 22 / 29 看不见），整条侧栏出现横向滚动条。

根因是两处叠加，测量数据见下表（`scratch/layout-preview/measure-cal.mjs`）：

| 量 | 修前 | 修后 |
| --- | --- | --- |
| `.cal-grid` 宽度 | 193px | 193px |
| `.cal-grid` scrollWidth | **217px** | 193px |
| 单个格子宽度 | 27.56px | 24.14px |
| `.side-scroll` scrollWidth / clientWidth | 238 / 235 | 235 / 235 |

1. `.cal-cell` 是 `<button>`，我们的 reset 只清了边框与底色，没清 `padding` —— 浏览器留的
   `1px 6px` 让每个格子的 min-content 变成 ~27.6px；
2. `grid-template-columns: repeat(7, 1fr)` 里的 `1fr` **最小值是 min-content**，七列因此
   卡死在 217px，撑破了 193px 的容器。

修法：`repeat(7, minmax(0, 1fr))`（允许列压到 0）+ `.cal-cell` 显式 `padding: 0 0 4px`。
两个都改才有意义——只改一个还能勉强不溢出，但格子宽度会跟着内容跳。

## 6. 顺手做的模块化

| 抽出来 | 原来在哪 | 现在 |
| --- | --- | --- |
| 筛选器增删改 | `App.tsx`（4 个回调 + 两种确认） | `hooks/useFilters.ts` |
| 输入框草稿 | `EntriesView.tsx` 的 state | `hooks/useComposerDraft.ts` + `components/ComposerDock.tsx` |
| 日期键 | `App.tsx` 与 `Calendar.tsx` 各写一遍 | `lib/dates.ts` |
| 设置保存条 | `SettingsView` 的底部 footer + `timeline.css` 的 3 条补丁 | 顶部 `.settings-head`（旧样式已删） |

`App.tsx` 仍是"装 hooks、切视图、转发通知"，但少了两台小状态机。
