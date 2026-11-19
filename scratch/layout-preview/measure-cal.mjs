// 量侧栏日历的横向溢出：把 entries 标本页灌进 headless Chrome，
// 用不同窗口尺寸各跑一次，打印 .side-scroll / .cal / .cal-grid 的
// clientWidth vs scrollWidth（以及格子的实际宽度）。
//
// 用法： node scratch\layout-preview\measure-cal.mjs
import { execFileSync } from "node:child_process";
import { rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { PAGES } from "./mock.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..", "..");
const distAssets = join(root, "frontend", "dist", "assets");
const CHROME = "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe";

const probe = `<script>
window.addEventListener("load", () => {
  const q = (s) => document.querySelector(s);
  const sc = q(".side-scroll"), cal = q(".cal"), grid = q(".cal-grid:last-of-type");
  const cell = q(".cal-grid:last-of-type .cal-cell:not(.is-empty)");
  const side = q(".side");
  const out = {
    win: innerWidth + "x" + innerHeight,
    sideW: side.getBoundingClientRect().width,
    sideScrollClient: sc.clientWidth,
    sideScrollScroll: sc.scrollWidth,
    calW: cal.getBoundingClientRect().width,
    calMaxW: getComputedStyle(cal).maxWidth,
    gridW: grid.getBoundingClientRect().width,
    gridScroll: grid.scrollWidth,
    cellW: cell.getBoundingClientRect().width,
    rootFont: getComputedStyle(document.documentElement).fontSize,
    bodyFont: getComputedStyle(document.body).fontSize,
  };
  const pre = document.createElement("pre");
  pre.id = "measure";
  pre.textContent = JSON.stringify(out);
  document.body.appendChild(pre);
});
</script>`;

const pages = PAGES.filter((p) => p.name === "entries");
const html = pages[0].html.replace("</body>", probe + "</body>");
const tmp = join(distAssets, `_measure-${Date.now()}.html`);
writeFileSync(tmp, html, "utf8");

const sizes = [
  [1280, 860],
  [1100, 760],
  [900, 700],
  [760, 600],
  [640, 560],
];

for (const [w, h] of sizes) {
  const dom = execFileSync(
    CHROME,
    [
      "--headless=new",
      "--disable-gpu",
      "--hide-scrollbars",
      "--virtual-time-budget=2500",
      `--window-size=${w},${h}`,
      "--dump-dom",
      "file:///" + tmp.replace(/\\/g, "/"),
    ],
    { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 },
  );
  const m = dom.match(/<pre id="measure">([\s\S]*?)<\/pre>/);
  console.log(m ? m[1] : `(no measure @ ${w}x${h})`);
}
rmSync(tmp, { force: true });
