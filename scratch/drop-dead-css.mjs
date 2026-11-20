// 一次性脚本：删掉确认没有任何 TSX/TS 引用的样式规则。
//
// 为什么单独写：项目里 CSS 的 import 顺序就是层叠顺序，手工在五个文件里逐条删
// 很容易删掉相邻规则的括号。这个脚本按大括号配对切块，并且**只摘掉选择器列表里
// 那一个死选择器**（`.memo-body img, .composer-preview img` 只该去掉后半截），
// 整条规则里一个活的都不剩时才删整条；@media / @supports 空了再一起删。
//
// 用法：node scratch/drop-dead-css.mjs [--apply]

import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const root = process.cwd();
const targets = [
  { file: "src/styles/entries.css", dead: ["row-icon", "row-actions", "icon-btn", "chip"] },
  {
    file: "src/styles/snippets.css",
    dead: ["snip-fields", "snip-row", "snip-trigger", "detail-snippet-row", "is-snippets"],
  },
  {
    file: "src/styles/timeline.css",
    dead: ["composer-preview", "memo-preview", "nav-icon-choice", "nav-edit-btn", "is-dragging", "is-memos"],
  },
  { file: "src/styles/settings.css", dead: ["settings-bar"] },
  { file: "src/styles/base.css", dead: ["snip-preview", "composer-input", "memo-input", "cm-editor", "composer-preview"] },
];

/** 把 CSS 拆成 [{ start, end, comment, selector, body }]，selector 去掉前置注释。 */
function blocks(css) {
  const out = [];
  let i = 0;
  while (i < css.length) {
    const open = css.indexOf("{", i);
    if (open === -1) break;
    const raw = css.slice(i, open);
    const lastComment = raw.lastIndexOf("*/");
    const comment = lastComment === -1 ? "" : raw.slice(0, lastComment + 2);
    const selector = (lastComment === -1 ? raw : raw.slice(lastComment + 2)).trim();
    let depth = 0;
    let j = open;
    for (; j < css.length; j++) {
      if (css[j] === "{") depth++;
      else if (css[j] === "}") {
        depth--;
        if (depth === 0) break;
      }
    }
    out.push({ start: i, end: j + 1, comment, selector, body: css.slice(open + 1, j) });
    i = j + 1;
  }
  return out;
}

const dead = (sel, names) =>
  names.some((d) => new RegExp(`\\.${d.replace(/-/g, "\\-")}(?![\\w-])`).test(sel));

/** 只在顶层逗号处切选择器（`:is(a, b)` 这种括号里的逗号不能切）。 */
function splitSelectors(sel) {
  const parts = [];
  let depth = 0;
  let cur = "";
  for (const ch of sel) {
    if (ch === "(") depth++;
    if (ch === ")") depth--;
    if (ch === "," && depth === 0) {
      parts.push(cur);
      cur = "";
      continue;
    }
    cur += ch;
  }
  parts.push(cur);
  return parts.map((s) => s.trim()).filter(Boolean);
}

const apply = process.argv.includes("--apply");
let total = 0;

for (const { file, dead: names } of targets) {
  const path = join(root, file);
  let css = readFileSync(path, "utf8");
  const report = [];

  // 从后往前改，前面的位置就不用重算
  for (const block of [...blocks(css)].reverse()) {
    if (block.selector.startsWith("@")) continue; // @media 外层稍后再看
    if (!dead(block.selector, names)) continue;
    const keep = splitSelectors(block.selector).filter((s) => !dead(s, names));
    const before = css.slice(block.start, block.start + (block.comment + block.selector).length);
    if (keep.length === 0) {
      css = css.slice(0, block.start) + css.slice(block.end);
      report.push(`删整条：${block.selector.replace(/\s+/g, " ").slice(0, 90)}`);
    } else {
      const indent = (before.match(/\n([ \t]*)$/) ?? [, ""])[1];
      css =
        css.slice(0, block.start) +
        block.comment +
        (block.comment ? "\n" + indent : "") +
        keep.join(",\n" + indent) +
        " {" +
        block.body +
        "}" +
        css.slice(block.end);
      report.push(`摘选择器：${block.selector.replace(/\s+/g, " ").slice(0, 90)} → ${keep.join(", ")}`);
    }
  }

  // 空的 @media / @supports 一起删
  let changed = true;
  while (changed) {
    changed = false;
    for (const block of blocks(css)) {
      if (!block.selector.startsWith("@")) continue;
      if (block.body.replace(/\/\*[\s\S]*?\*\//g, "").trim() !== "") continue;
      css = css.slice(0, block.start) + css.slice(block.end);
      report.push(`删空块：${block.selector}`);
      changed = true;
      break;
    }
  }

  css = css.replace(/\n{3,}/g, "\n\n").replace(/[ \t]+\n/g, "\n");
  total += report.length;
  console.log(`${file}: ${report.length} 条`);
  for (const r of report) console.log("  - " + r);
  if (apply) writeFileSync(path, css, "utf8");
}
console.log(`${apply ? "已写回" : "（dry run，加 --apply 才写回）"} 共 ${total} 条`);
