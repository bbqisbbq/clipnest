// 右键菜单条目的逻辑检查（纯函数，不需要浏览器、不需要跑应用）。
//
//   node --experimental-strip-types scratch\entry-menu-check.mjs
//
// 为什么值得单独测：菜单的规则是"哪条内容该有哪些操作"——图片才有 OCR、识别过才
// 有「复制识别文字」、已置顶才显示「取消置顶」、有缩写才显示「取消片段」、删除永远
// 在最后且是红的。这些以前只能靠手点验证，改一行就可能悄悄退化。
import assert from "node:assert/strict";
import { entryMenuItems } from "../frontend/src/lib/entryMenu.ts";

const noop = () => {};
const handlers = {
  copy: noop,
  openDetail: noop,
  togglePin: noop,
  toggleClipboard: noop,
  toggleSnippet: noop,
  toggleArchive: noop,
  remove: noop,
  ocr: noop,
  copyOcr: noop,
};

const labels = (items) => items.filter((i) => i.kind !== "separator").map((i) => i.label);
const byId = (items, id) => items.find((i) => i.id === id);

let pass = 0;
function check(name, fn) {
  fn();
  pass++;
  console.log(`  ✓ ${name}`);
}

const textEntry = {
  id: "t1",
  kind: "text",
  content: "一段普通文字",
  actions: [],
};
const imageEntry = {
  id: "i1",
  kind: "image",
  content: "![](memoimg:abcdef12)",
  actions: ["clipboard"],
};
const ocrDone = {
  ...imageEntry,
  ocrState: "done",
  ocrText: "坚果云 WebDAV 配置说明\n同步间隔 120 分钟",
};

console.log("右键菜单条目：");

check("纯文本条目没有 OCR 项", () => {
  const items = entryMenuItems(textEntry, handlers);
  assert.equal(byId(items, "ocr"), undefined);
  assert.equal(byId(items, "copy-ocr"), undefined);
});

check("图片条目有「识别图里的文字」", () => {
  const items = entryMenuItems(imageEntry, handlers);
  assert.equal(byId(items, "ocr").label, "识别图里的文字");
});

check("识别过的图片变成「重新识别文字」，并给出「复制识别文字」+ 字数", () => {
  const items = entryMenuItems(ocrDone, handlers);
  assert.equal(byId(items, "ocr").label, "重新识别文字");
  const copyOcr = byId(items, "copy-ocr");
  assert.ok(copyOcr, "缺「复制识别文字」");
  assert.equal(copyOcr.hint, `${ocrDone.ocrText.length} 字`);
});

check("识别中的图片在 OCR 项右侧标「识别中」", () => {
  const items = entryMenuItems({ ...imageEntry, ocrState: "pending" }, handlers);
  assert.equal(byId(items, "ocr").hint, "识别中");
});

check("ocr(force) 的 force 跟随状态：没识别过是 false，识别过是 true", () => {
  let got = null;
  entryMenuItems(imageEntry, { ...handlers, ocr: (f) => (got = f) }).find(
    (i) => i.id === "ocr",
  ).onClick();
  assert.equal(got, false);
  entryMenuItems(ocrDone, { ...handlers, ocr: (f) => (got = f) }).find(
    (i) => i.id === "ocr",
  ).onClick();
  assert.equal(got, true);
});

check("置顶 / 归档 / 片段 / 粘贴列表四个开关的文案跟着当前状态走", () => {
  const on = entryMenuItems(
    { ...textEntry, pinned: true, archived: true, triggerKey: "addr-", actions: ["clipboard"] },
    handlers,
  );
  assert.equal(byId(on, "pin").label, "取消置顶");
  assert.equal(byId(on, "archive").label, "恢复");
  assert.equal(byId(on, "snippet").label, "取消片段");
  assert.equal(byId(on, "snippet").hint, "addr-");
  assert.equal(byId(on, "clipboard").label, "移出粘贴列表");

  const off = entryMenuItems({ ...textEntry, pinned: false, archived: false }, handlers);
  assert.equal(byId(off, "pin").label, "置顶");
  assert.equal(byId(off, "archive").label, "归档");
  assert.equal(byId(off, "snippet").label, "设为片段");
  assert.equal(byId(off, "snippet").hint, undefined);
  assert.equal(byId(off, "clipboard").label, "加入粘贴列表");
});

check("删除永远是最后一项且标了 danger", () => {
  for (const e of [textEntry, imageEntry, ocrDone]) {
    const items = entryMenuItems(e, handlers);
    const last = items[items.length - 1];
    assert.equal(last.id, "delete");
    assert.equal(last.danger, true);
  }
});

check("分组结构：复制在最前，中间用两条分隔线隔开", () => {
  const items = entryMenuItems(ocrDone, handlers);
  assert.equal(items[0].id, "copy");
  assert.equal(items.filter((i) => i.kind === "separator").length, 2);
  assert.deepEqual(labels(items).slice(0, 3), ["复制内容", "重新识别文字", "复制识别文字"]);
});

check("每个条目都有 id 且不重复（React key 与键盘高亮都靠它）", () => {
  const items = entryMenuItems(ocrDone, handlers);
  const ids = items.map((i) => i.id);
  assert.equal(new Set(ids).size, ids.length, `id 重复: ${ids.join(",")}`);
});

console.log(`\n全部通过（${pass} 项）`);
