// 布局截图：把 dist 里最新的 index-*.css 内联进几张"整窗标本页"，
// 用 headless Chrome 量出真实观感（改 padding / margin 前后各跑一次对比）。
//
// 为什么不用 wails dev 截真人窗口：标本页是确定性的——同一份 DOM、同一份 CSS、
// 同一套字体，改前改后能逐像素比对；也能一屏同时看所有页面。
//
// 用法：
//   cd frontend; npx vite build
//   node scratch\layout-preview\snap.mjs              # 输出 scratch\layout-preview\*.png
//   node scratch\layout-preview\snap.mjs --tag before # 文件名带后缀，方便前后对照
//   node scratch\layout-preview\snap.mjs entries todo # 只拍指定页面

import { execFileSync } from "node:child_process";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { PAGES } from "./mock.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..", "..");
const distAssets = join(root, "frontend", "dist", "assets");
const CHROME = "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe";

const argv = process.argv.slice(2);
const tag = argv.includes("--tag") ? argv[argv.indexOf("--tag") + 1] : "";
const wanted = argv.filter((a, i) => !a.startsWith("--") && argv[i - 1] !== "--tag");
const targets = wanted.length ? PAGES.filter((p) => wanted.includes(p.name)) : PAGES;

mkdirSync(here, { recursive: true });
const written = [];
for (const p of targets) {
  const tmp = join(distAssets, `_preview-${p.name}-${Date.now()}.html`);
  writeFileSync(tmp, p.html, "utf8");
  const out = join(here, `${p.name}${tag ? "-" + tag : ""}.png`);
  execFileSync(
    CHROME,
    [
      "--headless=new",
      "--disable-gpu",
      "--hide-scrollbars",
      "--force-device-scale-factor=2",
      "--virtual-time-budget=2500",
      `--window-size=${p.w},${p.h}`,
      `--screenshot=${out}`,
      "file:///" + tmp.replace(/\\/g, "/"),
    ],
    { stdio: "ignore" },
  );
  rmSync(tmp, { force: true });
  written.push(out);
}
console.log(written.join("\n"));
