# scratch/layout-preview

改视觉（padding / margin / 列宽）时用的一组截图与量尺寸脚本。**不参与打包**，
只依赖 `frontend/dist` 里已经构建好的 CSS 与字体。

```powershell
cd frontend; npx vite build
cd ..
node scratch\layout-preview\snap.mjs --tag before          # 全部页面截图
node scratch\layout-preview\snap.mjs entries picker        # 只拍指定页面
node scratch\layout-preview\probe.mjs entries todo         # 打印关键元素的位置与 padding
```

- `mock.mjs`：按真实组件的 DOM 结构拼静态标本页（标题栏 / 侧栏 / 卡片 / 详情 / 待办 /
  设置 / picker / capture），共用同一份构建产出的 CSS。
- `snap.mjs`：headless Chrome 截图，输出 `*.png`（`--tag` 加后缀做前后对比）。
- `probe.mjs`：同一批页面里读 `getBoundingClientRect()`，用来抓"左右边缘不齐""越界"
  这类肉眼看不清的问题（`--dump-dom` 把结果打回来，不需要 CDP）。

小窗（picker 468×424 / capture 620×220）不能靠 `--window-size`：Windows 的头less 窗口
有约 500px 的最小宽度，量出来的视口比真窗口宽，右边会被裁。脚本里改成固定尺寸的容器。

设计取舍与这一次改动的来龙去脉见 `docs/design/2026-11-19-layout-rhythm.md`。
