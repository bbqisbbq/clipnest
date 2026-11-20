// 主题对比度体检：把 lib/themes.ts 里的每个预设解析出来，按"文字对 × 实际出现的底色"
// 算 WCAG 对比度，报告低于阈值的组合。
//
// 为什么不用 lib/themes.ts 里的 checkContrast：它只比 --bg / --surface 两种底，
// 而侧栏行、卡片、chip、输入框实际落在 --surface-2 / --surface-3 上。
//
// 用法：node scratch/theme-audit.mjs
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../frontend/src/lib/themes.ts", import.meta.url), "utf8");

// 粗解析：每个 { id: "...", ... tokens: { ... } } 块
const presets = [];
const blockRe = /\{\s*id:\s*"([^"]+)",\s*name:\s*"([^"]+)"[\s\S]*?tokens:\s*\{([\s\S]*?)\n\s*\},?\s*\n\s*\},/g;
let m;
while ((m = blockRe.exec(src))) {
  const tokens = {};
  for (const t of m[3].matchAll(/"(--[a-z0-9-]+)":\s*"([^"]+)"/g)) tokens[t[1]] = t[2];
  presets.push({ id: m[1], name: m[2], tokens });
}

function toRGB(color) {
  const c = String(color).trim();
  const hex = c.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i);
  if (hex) {
    let v = hex[1];
    if (v.length === 3) v = v.split("").map((ch) => ch + ch).join("");
    return [0, 2, 4].map((i) => parseInt(v.slice(i, i + 2), 16));
  }
  const rgb = c.match(/^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i);
  if (rgb) return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])];
  return null;
}

function lum([r, g, b]) {
  const f = (v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

function ratio(fg, bg) {
  const a = toRGB(fg);
  const b = toRGB(bg);
  if (!a || !b) return 0;
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const SURFACES = ["--bg", "--surface", "--surface-2", "--surface-3"];
const TEXTS = ["--text", "--text-2", "--text-3", "--accent", "--ok", "--warn", "--danger"];

let worst = [];
for (const p of presets) {
  console.log(`\n=== ${p.id}（${p.name}） ===`);
  for (const t of TEXTS) {
    const row = SURFACES.map((s) => {
      const r = ratio(p.tokens[t] ?? "#888", p.tokens[s] ?? p.tokens["--bg"]);
      const flag = r >= 4.5 ? "  " : r >= 3 ? " !" : "!!";
      worst.push({ preset: p.id, text: t, surface: s, ratio: r });
      return `${flag}${r.toFixed(2)}`;
    });
    console.log(`${t.padEnd(12)} ${SURFACES.map((s) => s.replace("--", "")).map((s) => s.padEnd(10)).join("")}`);
    console.log(`${"".padEnd(12)} ${row.map((x) => x.padEnd(10)).join("")}`);
  }
  // 实心强调色上的字
  const onAccent = ratio(p.tokens["--on-accent"] ?? "#000", p.tokens["--accent"]);
  console.log(`--on-accent  on --accent: ${onAccent.toFixed(2)}${onAccent >= 4.5 ? "" : "  ← 低于 4.5"}`);
}

worst.sort((a, b) => a.ratio - b.ratio);
console.log("\n=== 最差的 12 个组合 ===");
for (const w of worst.slice(0, 12)) {
  console.log(`${w.ratio.toFixed(2)}  ${w.preset.padEnd(14)} ${w.text.padEnd(11)} on ${w.surface}`);
}
