// 整窗标本页的 DOM 构造：把 App / Sidebar / EntryCard / Detail / Todo / Settings
// 的结构按源码抄成静态 HTML，喂给同一份构建产出的 CSS。
// 只给 snap.mjs（截图）和 probe.mjs（量尺寸）用，不参与打包。
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..", "..");
const distAssets = join(root, "frontend", "dist", "assets");

function cssFile() {
  const files = readdirSync(distAssets)
    .filter((f) => /^index-.*\.css$/.test(f))
    .map((f) => ({ f, m: statSync(join(distAssets, f)).mtimeMs }))
    .sort((a, b) => b.m - a.m);
  if (!files.length) throw new Error("找不到 frontend\\dist\\assets\\index-*.css，先跑 npx vite build");
  return files[0].f;
}

// Vite 产出的是 url(/assets/xxx.woff2)；标本页放在 dist/assets 里，改成相对路径。
const css = readFileSync(join(distAssets, cssFile()), "utf8").replace(/url\(\/assets\//g, "url(./");

// ---------------------------------------------------------------- markup bits

const icon = (paths, size = 14, extra = "") =>
  `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" ${extra}>${paths}</svg>`;

const ICONS = {
  all: `<rect x="3.5" y="3.5" width="7" height="7" rx="1.6"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.6"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.6"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.6"/>`,
  memo: `<path d="M4 6.5h10.5L17.5 9v9.5H4z"/><path d="M14.5 2.5V6H18"/><path d="M7 11h7M7 14h5"/>`,
  todo: `<rect x="3.5" y="3.5" width="17" height="17" rx="3.5"/><path d="M8 12.4l2.7 2.7L16.2 9.6"/>`,
  snippet: `<rect x="2" y="2.5" width="12" height="11" rx="2.5"/><path d="M4.8 6.2h4.2M4.8 9.4h6.4"/>`,
  pin: `<path d="M9 4h6l-1 6 3.5 3.5H5.5L9 10z"/><path d="M12 13.5V20"/>`,
  text: `<path d="M4 6h16"/><path d="M4 12h12"/><path d="M4 18h8"/>`,
  link: `<path d="M10 13a5 5 0 0 0 7.07 0l1.4-1.4a5 5 0 0 0-7.07-7.07L10.6 5.3"/><path d="M14 11a5 5 0 0 0-7.07 0l-1.4 1.4a5 5 0 0 0 7.07 7.07l.8-.8"/>`,
  image: `<rect x="3" y="4" width="18" height="16" rx="2.5"/><circle cx="9" cy="10" r="1.6"/><path d="m4 18 5-5 4 4 3-2.5 4 3.5"/>`,
  files: `<path d="M4 7a2 2 0 0 1 2-2h4l2 2.5h6a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z"/><path d="M4 11h16"/>`,
  settings: `<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-2.9 1.2 2 2 0 1 1-4 0 1.7 1.7 0 0 0-2.9-1.2l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1A1.7 1.7 0 0 0 3 15a2 2 0 1 1 0-4 1.7 1.7 0 0 0 1.2-2.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1A1.7 1.7 0 0 0 10 4.6a2 2 0 1 1 4 0 1.7 1.7 0 0 0 2.9 1.2l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1A1.7 1.7 0 0 0 21 11a2 2 0 1 1 0 4z"/>`,
  search: `<circle cx="11" cy="11" r="6.5"/><path d="m16 16 4 4"/>`,
  pause: `<rect x="7" y="5" width="3.5" height="14" rx="1.2"/><rect x="13.5" y="5" width="3.5" height="14" rx="1.2"/>`,
  sync: `<path d="M20 12a8 8 0 0 1-13.7 5.6L4 15.4"/><path d="M4 12A8 8 0 0 1 17.7 6.4L20 8.6"/><path d="M20 4v4.6h-4.6M4 20v-4.6h4.6"/>`,
};

function titleBar() {
  return `<header class="tb">
  <div class="tb-brand">
    <span class="tb-mark" aria-hidden="true">
      <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="6" y="4" width="12" height="17" rx="2.6"/><path d="M9.5 4.2V3.4A1.4 1.4 0 0 1 10.9 2h2.2a1.4 1.4 0 0 1 1.4 1.4v.8"/>
        <path d="M9.5 10h5M9.5 13.5h5M9.5 17h3"/>
      </svg>
    </span>
    <strong>ClipNest</strong>
  </div>
  <button class="tb-pill is-live"><span class="tb-dot"></span>记录中</button>
  <div class="tb-spacer"></div>
  <div class="tb-controls">
    <button class="win-btn"><svg viewBox="0 0 10 10" width="10" height="10"><path d="M2 5h6" stroke="currentColor" stroke-width="1.1" stroke-linecap="round"/></svg></button>
    <button class="win-btn"><svg viewBox="0 0 10 10" width="10" height="10"><rect x="2.2" y="2.2" width="5.6" height="5.6" rx="0.6" fill="none" stroke="currentColor" stroke-width="1.1"/></svg></button>
    <button class="win-btn is-close"><svg viewBox="0 0 10 10" width="10" height="10"><path d="m2.6 2.6 4.8 4.8M7.4 2.6 2.6 7.4" stroke="currentColor" stroke-width="1.15" stroke-linecap="round"/></svg></button>
  </div>
</header>`;
}

const FILTERS = [
  { icon: "all", name: "全部内容", n: 36, active: true },
  { icon: "memo", name: "我写的", n: 7 },
  { icon: "text", name: "剪贴板", n: 27 },
  { icon: "snippet", name: "片段", n: 3 },
  { icon: "link", name: "链接", n: 4 },
  { icon: "image", name: "图片", n: 2 },
  { icon: "pin", name: "置顶", n: 1 },
];

function calendar() {
  const cells = [];
  const firstDow = 6; // 2026-11: 1 号是周日 → 在周一开头的表里排最后
  const counts = { 3: 4, 7: 1, 11: 9, 12: 2, 17: 6, 18: 3, 19: 14, 20: 1 };
  for (let i = 0; i < firstDow; i++) cells.push(`<span class="cal-cell is-empty"></span>`);
  for (let d = 1; d <= 30; d++) {
    const n = counts[d] ?? 0;
    const cls = [
      "cal-cell",
      d === 19 ? "is-today" : "",
      d === 19 ? "is-active" : "",
      n ? "has-" + (n <= 2 ? 1 : n <= 5 ? 2 : n <= 12 ? 3 : 4) : "",
    ]
      .filter(Boolean)
      .join(" ");
    cells.push(
      `<button class="${cls}"><span class="cal-day">${d}</span>${
        n ? `<span class="cal-dot" style="opacity:${(0.35 + (0.65 * n) / 14).toFixed(2)}"></span>` : ""
      }</button>`,
    );
  }
  return `<div class="cal">
  <div class="cal-head"><button class="cal-nav">‹</button><span class="cal-title">2026 年 11 月</span><button class="cal-nav">›</button></div>
  <div class="cal-grid cal-weekdays"><span class="cal-weekday">一</span><span class="cal-weekday">二</span><span class="cal-weekday">三</span><span class="cal-weekday">四</span><span class="cal-weekday">五</span><span class="cal-weekday">六</span><span class="cal-weekday">日</span></div>
  <div class="cal-grid">${cells.join("")}</div>
</div>`;
}

function sidebar(active = "entries") {
  const items = FILTERS.map(
    (f) => `<div class="side-item-wrap">
      <div class="side-item ${f.active ? "is-active" : ""}" role="button" tabindex="0">
        ${icon(ICONS[f.icon] ?? ICONS.memo, 14)}<span>${f.name}</span><em>${f.n}</em>
        <button class="side-more">⋯</button>
      </div>
    </div>`,
  ).join("\n      ");

  return `<aside class="side">
  <div class="side-scroll">
    <div class="side-label">内容</div>
    <div class="side-sub">
      <button class="side-item ${active === "todo" ? "is-active" : ""}">${icon(ICONS.todo)}<span>待办</span><em class="side-todo-count">5</em></button>
      ${items}
      <button class="side-add">＋ 新建筛选器</button>
    </div>
    <div class="side-label">日历</div>
    ${calendar()}
  </div>
  <div class="side-foot">
    <button class="side-item">${icon(ICONS.pause, 15)}<span>暂停收集</span></button>
    <button class="side-item ${active === "settings" ? "is-active" : ""}">${icon(ICONS.settings, 15)}<span>设置</span>${icon(ICONS.sync, 13, 'class="side-sync"')}</button>
  </div>
</aside>`;
}

function card(o) {
  const cls = ["memo-card", "entry-card", o.collected ? "is-collected" : "", o.pinned ? "is-pinned" : "", o.collapsible ? "is-collapsible" : "", o.expanded ? "is-expanded" : ""]
    .filter(Boolean)
    .join(" ");
  const body = o.image
    ? `<div class="entry-image clampable"><p><img alt="" src="${o.image}"/></p></div>`
    : `<div class="memo-body clampable${o.folded ? " is-clipped" : ""}">${o.body}</div>`;
  const toggle = o.collapsible ? `<button class="entry-toggle ${o.expanded ? "is-open" : ""}">${o.expanded ? "收起 ⌃" : "展开全文 ⌄"}</button>` : "";
  const meta = [
    `<span>${o.time}</span>`,
    o.todo ? `<span class="entry-todo">1/3 待办</span>` : "",
    ...(o.actions ?? []).map((a) => `<span class="entry-autotag">${a}</span>`),
    ...(o.tags ?? []).map((t) => `<span class="memo-tag">#${t}</span>`),
    o.trigger ? `<span class="entry-trigger">${o.trigger}</span>` : "",
    o.src ? `<span class="memo-src">${o.src}</span>` : "",
    `<span class="memo-actions">
      <button class="link-btn">编辑</button><button class="link-btn ${o.pinned ? "is-on" : ""}">${o.pinned ? "取消置顶" : "置顶"}</button>
      <button class="link-btn">设为片段</button><button class="link-btn is-on">移出粘贴列表</button>
      <button class="link-btn">归档</button><button class="link-btn is-danger">删除</button>
    </span>`,
  ]
    .filter(Boolean)
    .join("");

  return `<article class="${cls}">
  ${o.title ? `<h3 class="memo-card-title">${o.title}</h3>` : ""}
  ${body}
  ${toggle}
  <footer class="memo-meta">${meta}</footer>
</article>`;
}

const IMG =
  "data:image/svg+xml;base64," +
  Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" width="900" height="560"><rect width="900" height="560" fill="#233049"/><rect x="60" y="60" width="520" height="26" rx="13" fill="#4d6bff"/><rect x="60" y="120" width="700" height="14" rx="7" fill="#4a5a7d"/><rect x="60" y="152" width="620" height="14" rx="7" fill="#4a5a7d"/><circle cx="720" cy="380" r="90" fill="#e0a253"/></svg>`,
  ).toString("base64");

function entriesPane() {
  const cards = [
    card({ time: "14:31", collected: true, src: "来自 chrome.exe", actions: ["剪贴板"], tags: ["工作"], body: "<p>把 <code>hook 注入</code> 那段挪到 <code>service_capture.go</code>，合并窗口从 45ms 提到 60ms 再测一次抖动。</p>" }),
    card({ time: "14:12", collected: true, src: "来自 Code.exe", body: "<p>https://github.com/bbqisbbq/clipnest</p>" }),
    card({ time: "13:40", tags: ["想法"], title: "卡片密度", body: "<p>列表已经够密了，问题在上下的呼吸：分组标题贴着第一张卡片，最后一张卡片又贴着输入区，看起来像一整块。</p><p>给分组加一点上边距、给时间线底部留白，视觉上就分成几块了。</p>", collapsible: true, folded: true }),
    card({ time: "11:05", collected: true, src: "来自 WeChat.exe", actions: ["剪贴板"], body: "<p>周五 15:00 评审，会议室 B，带上迭代清单。</p>" }),
    card({ time: "10:22", image: IMG, collected: true, src: "来自 Snipaste.exe" }),
    card({ time: "09:48", collected: true, todo: true, body: "<ul><li>整理 2.6 的 release note</li><li>把 Wails 绑定重新生成一次</li><li>确认托盘菜单里的开机自启开关</li></ul>" }),
    card({ time: "09:30", trigger: "addr-", actions: ["片段"], body: "<p>四川省阆中市……（一条长地址片段）</p>" }),
    card({ time: "08:55", collected: true, src: "来自 explorer.exe", actions: ["剪贴板"], body: "<p>E:\\default\\clipnest\\frontend\\src\\styles</p>" }),
  ];

  return `<div class="entries">
  <div class="entries-main">
    <header class="memo-head">
      <input class="input" placeholder="搜索内容（支持中文子串）…"/>
      <span class="memo-count">全部内容 · 8 条</span>
    </header>
    <div class="memo-timeline">
      <section class="memo-day">
        <div class="memo-day-label">今天</div>
        ${cards.slice(0, 6).join("\n        ")}
      </section>
      <section class="memo-day">
        <div class="memo-day-label">昨天</div>
        ${cards.slice(6).join("\n        ")}
      </section>
    </div>
  </div>
  <div class="memo-composer" style="height:184px">
    <div class="memo-composer-resizer" role="separator" aria-orientation="horizontal"></div>
    <div class="composer-box">
      <div class="md-surface" style="padding:12px 14px">
        <div class="memo-body"><p style="color:var(--text-3)">随手记点什么…（Ctrl+Enter 保存，写 - [ ] 就是待办）</p></div>
      </div>
      <div class="composer-actions">
        <span class="composer-tags"><span class="composer-hint">写 #标签 归类 · - [ ] 待办 · Ctrl+Enter 保存</span></span>
        <div class="composer-btns"><button class="link-btn">清空</button><button class="btn btn-primary btn-sm">保存</button></div>
      </div>
    </div>
  </div>
</div>`;
}

function detailPane() {
  return `<section class="detail">
  <header class="detail-head">
    <button class="detail-back"><span aria-hidden>←</span> 全部内容</button>
    <span class="detail-sub">2026-11-19 10:22 · 采集自 Snipaste.exe · 复制 3 次</span>
    <div class="detail-head-right">
      <span class="detail-dirty">未保存</span>
      <button class="btn btn-primary btn-sm">保存</button>
    </div>
  </header>
  <div class="detail-body">
    <div class="detail-editor">
      <div class="md-surface" style="padding:8px 4px">
        <div class="memo-body">
          <p>把 <code>hook 注入</code> 那段挪到 <code>service_capture.go</code>，合并窗口从 45ms 提到 60ms 再测一次抖动。</p>
          <p>- [x] 合并窗口改 60ms</p>
          <p>- [ ] 重跑 scratch/dav 的两台机器用例</p>
          <pre><code>func (s *ClipService) onClipboardUpdate() {
    s.merge.enqueue(time.Now())
}
</code></pre>
          <p>顺手把 <a href="#">打包格式</a> 的版本号升到 4。</p>
        </div>
      </div>
    </div>
    <aside class="detail-side">
      <section class="detail-block">
        <h4>能力</h4>
        <label class="sf-check"><input type="checkbox" checked/> 剪贴板（出现在快速粘贴列表）</label>
        <label class="sf-check"><input type="checkbox"/> 片段（可用缩写触发）</label>
      </section>
      <section class="detail-block">
        <h4>元信息</h4>
        <dl class="detail-meta">
          <dt>创建</dt><dd>2026-11-19 10:22</dd>
          <dt>更新</dt><dd>2026-11-19 14:31</dd>
          <dt>类型</dt><dd>text</dd>
          <dt>哈希</dt><dd class="is-mono">9f2c4e1ab77d…</dd>
        </dl>
      </section>
      <section class="detail-block detail-block--danger">
        <h4>操作</h4>
        <div class="detail-ops">
          <button class="link-btn">复制内容</button>
          <button class="link-btn">用系统默认程序打开</button>
          <button class="link-btn">归档</button>
          <button class="link-btn is-danger">删除这条</button>
        </div>
      </section>
    </aside>
  </div>
</section>`;
}

function todoPane() {
  const row = (t, meta, done = false, indent = 0) =>
    `<div class="todo-row ${done ? "is-done" : ""}">
      <input class="todo-check" type="checkbox" ${done ? "checked" : ""}/>
      <button class="todo-text" style="padding-left:${indent}px">${t}</button>
      <span class="todo-meta">${meta}</span>
    </div>`;
  return `<div class="todo">
  <header class="todo-head">
    <h2>待办</h2>
    <span class="todo-hint">在任意条目里写 <code>- [ ] 一件事</code>，就会出现在这里；勾选改的是那行正文。</span>
    <span class="todo-count">5 未完成 · 2 已完成</span>
  </header>
  <div class="todo-body">
    <section class="todo-group">
      ${row("整理 2.6 的 release note", "今天 09:48")}
      ${row("把 Wails 绑定重新生成一次", "今天 09:48")}
      ${row("确认托盘菜单里的开机自启开关", "今天 09:48", false, 14)}
      ${row("重跑 scratch/dav 的两台机器用例", "今天 10:22")}
      ${row("问一下双屏下 picker 的定位", "昨天 20:11")}
    </section>
    <section class="todo-group todo-group--done">
      <button class="todo-group-head">展开已完成（2）</button>
    </section>
  </div>
</div>`;
}

function settingsPane() {
  const field = (label, ctrl) =>
    `<div class="field"><div class="field-label"><span>${label}</span></div><div class="field-control">${ctrl}</div></div>`;
  const sw = (on) => `<button class="switch ${on ? "is-on" : ""}"><span class="switch-knob"></span></button>`;
  return `<main class="main is-settings">
  <div class="settings">
    <div class="settings-inner">
      <section class="card">
        <div class="card-head"><h3>采集</h3><p>开机后自动监听剪贴板，文本 / 链接 / 图片 / 文件都会收。</p></div>
        <div class="card-body">
          ${field("自动采集", sw(true))}
          ${field("开机自启", sw(true))}
          ${field("合并窗口（毫秒）", `<input class="input is-num" value="60"/><span class="unit">ms</span>`)}
        </div>
      </section>
      <section class="card">
        <div class="card-head"><h3>交互</h3><p>快捷键与光标处弹窗。</p></div>
        <div class="card-body">
          ${field("快速粘贴", `<button class="btn btn-ghost hotkey-box">Ctrl+Shift+V</button>`)}
          ${field("快速记一条", `<button class="btn btn-ghost hotkey-box">Ctrl+Alt+N</button>`)}
        </div>
      </section>
      <section class="card">
        <div class="card-head"><h3>同步</h3><p>WebDAV 打包同步，一台机器一个包，不写别人的文件。</p></div>
        <div class="card-body">
          ${field("启用同步", sw(true))}
          ${field("服务器地址", `<input class="input" value="https://dav.example.com/dav"/>`)}
          <div class="sync-actions"><button class="btn btn-ghost">测试连接</button><button class="btn btn-primary btn-sm">立即同步</button><span class="sync-state">上次 3 分钟前 · 成功</span></div>
        </div>
      </section>
      <section class="card">
        <div class="card-head"><h3>外观</h3><p>主题与配色。</p></div>
        <div class="card-body"><div class="preset-grid">
          <button class="preset-card is-on"><span class="preset-swatch"><i style="background:#0e1015"></i><i style="background:#7d8cff"></i><i style="background:#45c78d"></i></span><span class="preset-name">夜航</span><span class="preset-note">默认深色</span></button>
          <button class="preset-card"><span class="preset-swatch"><i style="background:#12141a"></i><i style="background:#8b8cf0"></i><i style="background:#e0a253"></i></span><span class="preset-name">石墨</span><span class="preset-note">低饱和</span></button>
          <button class="preset-card"><span class="preset-swatch"><i style="background:#f6f7fb"></i><i style="background:#5b73ff"></i><i style="background:#2f9e6a"></i></span><span class="preset-name">晨光</span><span class="preset-note">浅色</span></button>
        </div></div>
      </section>
    </div>
  </div>
  <footer class="settings-bar"><span class="is-dirty">有未保存的修改</span>
    <div class="settings-bar-actions"><button class="btn btn-ghost">撤销</button><button class="btn btn-primary">保存设置</button></div>
  </footer>
</main>`;
}

// ---------------------------------------------------------------- page shell

function page(name, mainPane, opts = {}) {
  const bodyClass = opts.bodyClass ?? "app is-entries";
  return `<!doctype html><html lang="zh-CN" data-theme="dark"><head><meta charset="utf-8"/>
<style>${css}</style>
<style>html,body{height:100%;margin:0}#root{height:100%}</style>
</head><body>
<div id="root"><div class="${bodyClass}">
${titleBar()}
<div class="workspace">
${sidebar(opts.active ?? "entries")}
<div class="main-pane">${mainPane}</div>
</div>
</div></div>
</body></html>`;
}

// ---------------------------------------------------------------- 独立窗口

function pickerWindow() {
  const row = (o) => `<div class="picker-row ${o.active ? "is-active" : ""}">
    <div class="picker-icon kind-${o.kind}">${o.thumb ? `<img class="picker-thumb" src="${o.thumb}" alt=""/>` : icon(ICONS[o.kind] ?? ICONS.text, 15)}</div>
    <div class="picker-main">
      <span class="picker-preview">${o.preview}</span>
      <span class="picker-meta">${o.meta}</span>
    </div>
    <span class="picker-actions">
      <button class="picker-act">${icon(ICONS.pin, 12)}</button>
      <button class="picker-act">${icon(ICONS.files, 12)}</button>
      <button class="picker-act is-danger"><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 7h16"/><path d="M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12"/></svg></button>
    </span>
    <span class="picker-num">${o.n}</span>
    ${o.active ? "<kbd>↵</kbd>" : ""}
  </div>`;

  return `<div class="picker">
  <div class="picker-search">${icon(ICONS.search, 15)}
    <input placeholder="搜索历史，输入 -- 搜索片段…"/>
    <span class="picker-count">42</span>
  </div>
  <div class="picker-list">
    ${row({ kind: "text", n: 1, active: true, preview: "把 hook 注入 那段挪到 service_capture.go，合并窗口从 45ms 提到 60ms", meta: "chrome.exe · 96 B · 3 分钟前" })}
    ${row({ kind: "link", n: 2, preview: "https://github.com/bbqisbbq/clipnest", meta: "Code.exe · 41 B · 12 分钟前" })}
    ${row({ kind: "image", n: 3, thumb: IMG, preview: "截图 2026-11-19 101522.png", meta: "Snipaste.exe · 412 KB · 1 小时前" })}
    ${row({ kind: "files", n: 4, preview: "styles  （4 个文件）", meta: "explorer.exe · 128 B · 昨天" })}
    ${row({ kind: "text", n: 5, preview: "周五 15:00 评审，会议室 B，带上迭代清单。", meta: "WeChat.exe · 24 B · 昨天" })}
  </div>
  <div class="picker-foot">
    <span>↑↓ 选择</span><span>↵ 粘贴</span><span>1–9 直选</span><span>-- 搜片段</span>
    <span>Alt+P 置顶</span><span>Alt+A 归档</span><span>Ctrl+Del 删除</span>
  </div>
</div>`;
}

function captureWindow() {
  return `<div class="capture">
  <div class="capture-head">
    <span class="capture-dot"></span>
    <span class="capture-title">记一笔</span>
    <span class="capture-tags"><span class="composer-chip">#工作</span><span class="composer-chip">#clipnest</span></span>
    <span class="capture-keys">Enter 保存 · Esc 关闭</span>
    <button class="capture-close">×</button>
  </div>
  <div class="capture-box">
    <div class="md-surface" style="padding:10px 8px">
      <div class="memo-body"><p>把 <code>hook 注入</code> 那段挪到 <code>service_capture.go</code> <span style="color:var(--text-3)">|</span></p></div>
    </div>
  </div>
  <div class="capture-foot"><div class="capture-foot-right"><button class="btn btn-primary btn-sm">保存</button></div></div>
</div>`;
}

function standalone(bodyClass, inner, w, h) {
  // 小窗（picker / capture）不能靠 --window-size：Windows 的头less 窗口有最小宽度
  // （约 500px），量出来的视口比真实窗口宽，截图右边会被裁掉。
  // 所以固定一个 w×h 的容器，并把 100vh 改成 100%。
  return `<!doctype html><html lang="zh-CN" data-theme="dark"><head><meta charset="utf-8"/>
<style>${css}</style>
<style>html,body{height:100%;margin:0;background:#05070a}
#win{width:${w}px;height:${h}px}.picker,.capture{height:100%}</style></head>
<body class="${bodyClass}"><div id="win">${inner}</div></body></html>`;
}

const PAGES = [
  { name: "entries", html: page("entries", entriesPane()), w: 1280, h: 860 },
  { name: "detail", html: page("detail", entriesPane() + detailPane()), w: 1280, h: 860 },
  { name: "todo", html: page("todo", todoPane(), { active: "todo" }), w: 1280, h: 860 },
  { name: "settings", html: page("settings", settingsPane(), { active: "settings" }), w: 1280, h: 860 },
  { name: "narrow", html: page("entries", entriesPane()), w: 1000, h: 760 },
  { name: "detail-narrow", html: page("detail", entriesPane() + detailPane()), w: 1000, h: 760 },
  { name: "settings-narrow", html: page("settings", settingsPane(), { active: "settings" }), w: 900, h: 700 },
  { name: "picker", html: standalone("is-picker", pickerWindow(), 468, 424), w: 520, h: 545 },
  { name: "capture", html: standalone("is-capture", captureWindow(), 620, 220), w: 660, h: 340 },
];



export { PAGES };


