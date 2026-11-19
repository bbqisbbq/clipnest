// 找出「CSS 里定义、TSX 里从不出现」的规则并删除。
//
// 判定口径：
//   1. 收集 src/**/*.{ts,tsx} 里所有字符串字面量（含模板串）与其中的空白分隔 token；
//      这就是"实际会被写进 className 的词"。
//   2. 解析 CSS：按大括号配对取每条规则（@media 会递归进去），拿到选择器里的类名。
//   3. 一条规则的类名**全部**命中 dead 名单才删；混着活类名的一律保留。
//   4. 动态拼接的类名（kind-${x}、md-surface--${p}、has-${n}）属于"前缀族"，
//      出现该前缀字面量时把整族视为活，避免误删。
//
// 用法：
//   node scratch/css-cleanup/dead-css.mjs            # 只报告
//   node scratch/css-cleanup/dead-css.mjs --write    # 真删

import fs from "node:fs";
import path from "node:path";

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname).replace(/^\/([A-Za-z]:)/, "$1"), "../..");
const srcDir = path.join(root, "frontend/src");
const stylesDir = path.join(srcDir, "styles");
const write = process.argv.includes("--write");
const verbose = process.argv.includes("--verbose");

// 这些文件里的类由第三方在运行时生成（Vditor 自己插 DOM），TSX 里当然没有，
// 不能按"没出现"判死。
const skipFiles = new Set(["vditor-theme.css"]);

// ---- 1. 收集 TSX/TS 里出现过的类名 token ----
function walk(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name === "assets") continue;
      walk(p, out);
    } else if (/\.(tsx?|jsx?)$/.test(e.name)) {
      out.push(p);
    }
  }
  return out;
}

const tokens = new Set();
const dynamicPrefixes = new Set();

// 把一个字符串片段拆成类名 token（含 ${} 里的静态头/前缀族）
function takeTokens(lit) {
  for (const part of lit.split(/\s+/)) {
    if (!part) continue;
    if (part.includes("${")) {
      // `tip-body${right ? " is-right" : ""}` → 静态头才是要的类名；
      // `kind-${e.kind}` → 记成前缀族（整族视为活）。
      const head = part.slice(0, part.indexOf("${"));
      if (/^[a-z0-9-]+-$/.test(head)) dynamicPrefixes.add(head);
      else if (/^-?[A-Za-z_][\w-]*$/.test(head)) tokens.add(head);
      continue;
    }
    if (/^-?[A-Za-z_][\w-]*$/.test(part)) tokens.add(part);
  }
}

// 扫描一段源码里所有字符串字面量；模板串会递归扫它内部（`${a ? "x" : "y"}` 的
// 两个类名都在里面，第一版正则把整个模板串吃掉就漏了）。
function scanLiterals(text) {
  for (const m of text.matchAll(/"([^"\n]*)"|'([^'\n]*)'|`([^`]*)`/g)) {
    if (m[3] !== undefined) {
      takeTokens(m[3]); // 模板串静态部分（含 `${}` 前缀族识别）
      scanLiterals(m[3]); // 内层引号字面量（含 ${} 表达式里的）
      continue;
    }
    takeTokens(m[1] ?? m[2] ?? "");
  }
}

for (const file of walk(srcDir)) {
  scanLiterals(fs.readFileSync(file, "utf8"));
}

// ---- 2. 解析 CSS ----
function classesOf(selector) {
  const out = new Set();
  for (const m of selector.matchAll(/\.(-?[A-Za-z_][\w-]*)/g)) out.add(m[1]);
  return out;
}

function isLive(cls) {
  if (tokens.has(cls)) return true;
  for (const pre of dynamicPrefixes) if (cls.startsWith(pre)) return true;
  return false;
}

// 返回 [{start, end, selector}] —— 顶层与 @media 内的普通规则
function rules(text) {
  const out = [];
  let i = 0;
  while (i < text.length) {
    const open = text.indexOf("{", i);
    if (open < 0) break;
    const prelude = text.slice(i, open);
    const selector = prelude.trim().replace(/^\/\*[\s\S]*?\*\/\s*/, "");
    // 找配对的大括号
    let depth = 0;
    let j = open;
    for (; j < text.length; j++) {
      if (text[j] === "{") depth++;
      else if (text[j] === "}") {
        depth--;
        if (depth === 0) break;
      }
    }
    if (selector.startsWith("@media") || selector.startsWith("@supports")) {
      const inner = text.slice(open + 1, j);
      for (const r of rules(inner)) {
        out.push({ start: open + 1 + r.start, end: open + 1 + r.end, selector: r.selector });
      }
    } else if (!selector.startsWith("@")) {
      out.push({ start: i, end: j + 1, selector });
    }
    i = j + 1;
  }
  return out;
}

let removedLines = 0;
let removedRules = 0;
const report = [];

for (const name of fs.readdirSync(stylesDir).filter((f) => f.endsWith(".css"))) {
  if (skipFiles.has(name)) continue;
  const file = path.join(stylesDir, name);
  const text = fs.readFileSync(file, "utf8");
  const dead = [];
  for (const r of rules(text)) {
    const cls = classesOf(r.selector);
    if (cls.size === 0) continue;
    if ([...cls].every((c) => !isLive(c))) dead.push(r);
  }
  if (dead.length === 0) continue;
  if (verbose) {
    for (const r of dead) console.log(`  ${name}: ${r.selector.replace(/\s+/g, " ")}`);
  }

  let out = text;
  // 从后往前删，避免位置漂移
  for (const r of dead.sort((a, b) => b.start - a.start)) {
    let start = r.start;
    // 把紧贴在规则上方的注释块一起删掉
    const before = out.slice(0, start);
    const m = before.match(/(?:\s*\/\*[\s\S]*?\*\/\s*)+$/);
    if (m) start -= m[0].length;
    // 连带吃掉行尾换行，避免留下一串空行
    let end = r.end;
    while (out[end] === "\n" || out[end] === "\r") end++;
    out = out.slice(0, start) + out.slice(end);
    removedRules++;
  }
  // 收拢 3 个以上连续空行为 1 个
  out = out.replace(/\n{3,}/g, "\n\n");

  const removed = text.split("\n").length - out.split("\n").length;
  removedLines += removed;
  report.push(`${name}: 删 ${dead.length} 条规则 / ${removed} 行`);
  if (write) fs.writeFileSync(file, out, "utf8");
}

console.log(report.join("\n"));
console.log(`\n合计：${removedRules} 条规则，${removedLines} 行${write ? "（已写入）" : "（干跑）"}`);
if (dynamicPrefixes.size) console.log(`动态族前缀（视为活）：${[...dynamicPrefixes].join(", ")}`);
