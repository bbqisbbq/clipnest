// 量尺寸：把 snap.mjs 的标本页加载进 headless Chrome，
// 用脚本读出关键元素的位置，再看有没有"越界 / 贴边 / 不齐"。
// 原理：注入的脚本把测量结果写进 <pre>；--dump-dom 把最终 DOM 打出来。
//
// 用法：node scratch\layout-preview\probe.mjs [页面名…]

import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..", "..");
const distAssets = join(root, "frontend", "dist", "assets");
const CHROME = "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe";

const cssFile = readdirSync(distAssets)
  .filter((f) => /^index-.*\.css$/.test(f))
  .map((f) => ({ f, m: statSync(join(distAssets, f)).mtimeMs }))
  .sort((a, b) => b.m - a.m)[0].f;
const css = readFileSync(join(distAssets, cssFile), "utf8").replace(/url\(\/assets\//g, "url(./");

// 只取 snap.mjs 里的页面构造结果（不触发截图）：直接 import 它的 PAGES 太重，
// 这里用同样的思路，但只保留结构：读 snap.mjs 的源码并把截图段剪掉太脆弱，
// 所以 probe 自带一份最小 DOM（与 snap.mjs 的 markup 构造共享选择器即可）。
const pagesModule = await import("./mock.mjs");
const PAGES = pagesModule.PAGES;

const wanted = process.argv.slice(2).filter((a) => !a.startsWith("--"));
const targets = wanted.length ? PAGES.filter((p) => wanted.includes(p.name)) : PAGES;

const PROBE = `
<script>
const sel = [
  ".tb", ".tb-mark", ".tb-pill", ".win-btn", ".side", ".side-scroll", ".side-label", ".side-item",
  ".cal", ".cal-cell", ".side-foot", ".memo-head", ".memo-head .input", ".memo-count",
  ".memo-timeline", ".memo-day", ".memo-day-label", ".memo-card", ".memo-meta",
  ".memo-composer", ".composer-box", ".composer-actions",
  ".detail-head", ".detail-body", ".detail-editor", ".detail-side",
  ".todo-head", ".todo-body", ".todo-row",
  ".settings-inner", ".card", ".card-head", ".field", ".settings-bar",
  ".picker", ".picker-search", ".picker-list", ".picker-row", ".picker-foot",
  ".capture", ".capture-head", ".capture-foot"
];
const r = (el) => { const b = el.getBoundingClientRect();
  return {x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width), h: Math.round(b.height)}; };
const out = { viewport: {w: innerWidth, h: innerHeight}, scrollWidth: document.documentElement.scrollWidth, items: {} };
for (const s of sel) {
  const el = document.querySelector(s);
  if (el) out.items[s] = r(el);
}
const pad = (el) => { const c = getComputedStyle(el); return [c.paddingTop, c.paddingRight, c.paddingBottom, c.paddingLeft].join(" "); };
for (const s of [".memo-head", ".memo-timeline", ".memo-card", ".side-item", ".composer-box", ".detail-editor", ".todo-row", ".settings-inner", ".card-body", ".picker-row"]) {
  const el = document.querySelector(s);
  if (el) out.items[s + "·padding"] = pad(el);
}
document.body.innerHTML = "<pre id=probe>" + JSON.stringify(out, null, 1) + "</pre>";
</script>`;

for (const p of targets) {
  const html = p.html.replace("</body>", PROBE + "</body>");
  const tmp = join(distAssets, `_probe-${p.name}-${Date.now()}.html`);
  writeFileSync(tmp, html, "utf8");
  const dom = execFileSync(
    CHROME,
    ["--headless=new", "--disable-gpu", "--virtual-time-budget=2000", `--window-size=${p.w},${p.h}`, "--dump-dom", "file:///" + tmp.replace(/\\/g, "/")],
    { encoding: "utf8", maxBuffer: 32 * 1024 * 1024 },
  );
  rmSync(tmp, { force: true });
  const m = dom.match(/<pre id="probe">([\s\S]*?)<\/pre>/);
  console.log(`\n=== ${p.name} (${p.w}x${p.h}) ===`);
  console.log(m ? m[1].replace(/&quot;/g, '"') : "(没有量到，页面可能报错了)");
}
