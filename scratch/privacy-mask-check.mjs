// 隐私模式里"哪些段落要被遮"的逻辑用例（不需要浏览器、不需要应用在跑）。
//
//   node --experimental-strip-types scratch/privacy-mask-check.mjs
//
// 只测 pickMasked —— 它把"DOM 里逐段收集"简化成了"每个片段属于第几段"的数组，
// 剩下的（怎么走 DOM、怎么用 Highlight API 上色）在浏览器里验，见 docs/ARCHITECTURE.md。

const { pickMasked } = await import("../frontend/src/lib/privacyMask.ts");

let failed = 0;
function check(name, ok, detail) {
  if (ok) {
    console.log(`  ok   ${name}`);
  } else {
    failed++;
    console.log(`  FAIL ${name} — ${detail}`);
  }
}

const eq = (a, b) => JSON.stringify(a) === JSON.stringify(b);

console.log("pickMasked:");
const cases = [
  { name: "光标在第 2 段：其余全遮", blocks: [0, 0, 1, 1, 2], active: 1, want: [0, 1, 4] },
  { name: "光标在第 1 段：只留第 1 段", blocks: [0, 0, 1, 1, 2], active: 0, want: [2, 3, 4] },
  { name: "光标在最后一段", blocks: [0, 0, 1, 1, 2], active: 2, want: [0, 1, 2, 3] },
  { name: "只有一段：一个都不遮", blocks: [0, 0, 0], active: 0, want: [] },
  { name: "空文档", blocks: [], active: 0, want: [] },
  { name: "光标未知（-1）：一个都不遮", blocks: [0, 1, 2], active: -1, want: [] },
  { name: "段号越界（内容变短）：全遮", blocks: [0, 1], active: 9, want: [0, 1] },
  { name: "空段落不算一段：段号可以不连续", blocks: [0, 2], active: 2, want: [0] },
];

for (const c of cases) {
  const got = pickMasked(c.blocks, c.active);
  check(c.name, eq(got, c.want), `got ${JSON.stringify(got)} want ${JSON.stringify(c.want)}`);
}

console.log(failed === 0 ? "\n全部通过" : `\n${failed} 条失败`);
process.exit(failed === 0 ? 0 : 1);
