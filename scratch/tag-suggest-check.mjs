// 标签联想的逻辑用例（不需要浏览器、不需要应用在跑）。
//
//   node --experimental-strip-types scratch/tag-suggest-check.mjs
//
// 直接 import 真正在用的 frontend/src/lib/tagSuggest.ts，只把 window.getSelection() 与
// document.createRange() 换成桩：这两处是唯一依赖浏览器的地方，其余全是字符串与下标。
// Vditor 那一层（弹窗什么时候弹、Enter 怎么拦）不在这里验，见 docs/ARCHITECTURE.md。

const mod = await import("../frontend/src/lib/tagSuggest.ts");
const { findTagToken, pickTags } = mod;

// ---- 桩 -------------------------------------------------------------------
const RECT = { left: 120, top: 40, bottom: 58, right: 200, width: 80, height: 18 };

function stubDom({ text = "", offset = 0, nodeType = 3, collapsed = true, inCode = false, noRange = false }) {
  const node = {
    nodeType,
    textContent: text,
    parentElement: { closest: (sel) => (inCode && sel.includes("code") ? { tagName: "CODE" } : null) },
  };
  globalThis.window = {
    getSelection: () => ({
      rangeCount: 1,
      isCollapsed: collapsed,
      anchorNode: node,
      anchorOffset: offset,
    }),
  };
  globalThis.document = {
    createRange: () => ({
      setStart() {},
      setEnd() {},
      getBoundingClientRect() {
        if (noRange) throw new Error("range out of bounds");
        return RECT;
      },
    }),
  };
  globalThis.Node = { TEXT_NODE: 3 };
}

let failed = 0;
function check(name, ok, detail = "") {
  if (ok) {
    console.log(`  ok   ${name}`);
  } else {
    failed++;
    console.log(`  FAIL ${name}${detail ? " — " + detail : ""}`);
  }
}

console.log("findTagToken:");
const cases = [
  { name: "只打了一个 #", dom: { text: "#", offset: 1 }, want: { prefix: "", start: 0 } },
  { name: "#w（行首）", dom: { text: "#w", offset: 2 }, want: { prefix: "w", start: 0 } },
  { name: "行文里 hello #work", dom: { text: "hello #work", offset: 11 }, want: { prefix: "work", start: 6 } },
  { name: "中文全角括号后（#标签", dom: { text: "（#读书", offset: 4 }, want: { prefix: "读书", start: 1 } },
  { name: "标签后的空格——不再联想", dom: { text: "#work ", offset: 6 }, want: null },
  { name: "abc#def 不是标签", dom: { text: "abc#def", offset: 7 }, want: null },
  { name: "标记符不是 # 前缀（##）", dom: { text: "##x", offset: 3 }, want: null },
  { name: "行内码里不联想", dom: { text: "#work", offset: 5, inCode: true }, want: null },
  { name: "有选区时不联想", dom: { text: "#work", offset: 5, collapsed: false }, want: null },
  { name: "光标在元素节点上不联想", dom: { text: "", offset: 0, nodeType: 1 }, want: null },
  { name: "量不到位置时 rect 为空（仍返回 token）", dom: { text: "#wo", offset: 3, noRange: true }, want: { prefix: "wo", start: 0, noRect: true } },
];
for (const c of cases) {
  stubDom(c.dom);
  const got = findTagToken();
  if (c.want === null) {
    check(c.name, got === null, `got ${JSON.stringify(got)}`);
    continue;
  }
  const okPrefix = got && got.prefix === c.want.prefix && got.start === c.want.start;
  const okRect = c.want.noRect ? got && got.rect === null : got && !!got.rect;
  check(c.name, Boolean(okPrefix && okRect), `got ${JSON.stringify(got)}`);
}

console.log("pickTags:");
const tags = ["work", "writing", "reading"];
const pk = [
  { name: "空片段给最常用的前几个", args: ["", 8], want: ["work", "writing", "reading"] },
  { name: "前缀匹配 wr", args: ["wr", 8], want: ["writing"] },
  { name: "前缀匹配 wo", args: ["wo", 8], want: ["work"] },
  { name: "大小写不敏感", args: ["WORK", 8], want: ["work"] },
  { name: "前匹配优先、包含匹配兜底", args: ["rit", 8], want: ["writing"] },
  { name: "没有命中", args: ["zzz", 8], want: [] },
  { name: "limit 生效", args: ["", 2], want: ["work", "writing"] },
];
for (const c of pk) {
  const got = pickTags(tags, c.args[0], c.args[1]);
  check(c.name, JSON.stringify(got) === JSON.stringify(c.want), `got ${JSON.stringify(got)} want ${JSON.stringify(c.want)}`);
}

console.log(failed === 0 ? "\n全部通过" : `\n${failed} 条失败`);
process.exit(failed === 0 ? 0 : 1);
