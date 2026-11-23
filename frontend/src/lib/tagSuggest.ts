// 标签联想：从光标处往回找"正在输入的 #标签"，以及把选中的标签写回去。
//
// 为什么不放在 MarkdownSurface 里：这段逻辑只碰 DOM 选区与字符串，跟 Vditor 无关。
// 编辑器那层只负责"什么时候刷新、键盘怎么走"，判断与替换留在这里，便于单测与复用。

/** 一个正在输入的标签记号：`#` 的位置、光标位置、`#` 后面已经打出来的字。 */
export type TagToken = {
  /** 文本节点里 `#` 的下标 */
  start: number;
  /** 文本节点里光标的偏移 */
  end: number;
  prefix: string;
  rect: DOMRect | null;
};

// `#` 必须出现在开头或空白/标点之后（`a#b` 不算标签），标签体沿用后端的字符集
// （`#([\p{L}\p{N}_\-/]+)`，见 internal/clip/entry.go 的 tagPattern）。
const TAG_TOKEN_RE = /(?:^|[\s(（[【「《,，;；:：>|、/])#([\p{L}\p{N}_\-\/]*)$/u;

/** 读当前选区，判断光标是否正停在一个 `#标签` 里。 */
export function findTagToken(): TagToken | null {
  const sel = window.getSelection();
  if (!sel || sel.rangeCount === 0 || !sel.isCollapsed) return null;
  const node = sel.anchorNode;
  if (!node || node.nodeType !== Node.TEXT_NODE) return null;
  const el = node.parentElement;
  // 行内码/代码块里不联想：那里的 `#` 是代码，不是标签。
  // 只看 <code>：ID 模式下标题/引用这类块前缀会包在 <pre> 里，而"行首打 #"恰恰
  // 是最常见的场景，按 pre 排除会把最该联想的地方排除掉。
  if (!el || el.closest("code")) return null;
  const text = node.textContent ?? "";
  const upto = text.slice(0, sel.anchorOffset);
  const m = TAG_TOKEN_RE.exec(upto);
  if (!m) return null;
  const prefix = m[1] ?? "";
  const start = sel.anchorOffset - prefix.length - 1;
  if (start < 0) return null;
  let rect: DOMRect | null = null;
  try {
    const range = document.createRange();
    range.setStart(node, start);
    range.setEnd(node, sel.anchorOffset);
    rect = range.getBoundingClientRect();
  } catch {
    /* 选区越界（Vditor 刚重排过）：这次不定位，下一次输入再算 */
  }
  return { start, end: sel.anchorOffset, prefix, rect };
}

/** 按已输入的片段筛标签：空片段给"最常用"的前几个，否则按包含匹配（中文按前缀更顺手）。 */
export function pickTags(tags: string[], prefix: string, limit = 8): string[] {
  const p = prefix.trim().toLowerCase();
  const out: string[] = [];
  const loose: string[] = [];
  for (const t of tags) {
    const lower = t.toLowerCase();
    if (!p) out.push(t);
    else if (lower.startsWith(p)) out.push(t);
    else if (lower.includes(p)) loose.push(t);
    if (out.length >= limit) break;
  }
  for (const t of loose) {
    if (out.length >= limit) break;
    out.push(t);
  }
  return out.slice(0, limit);
}

/**
 * 把 `#prefix` 整段替换成 `#tag `。
 *
 * 优先交给调用方传进来的 insert（编辑器层传的是 `Vditor.insertValue`）：那是编辑器
 * 自己的模型入口，工具条插文本也走它。没有 insert 时退回 `execCommand("insertText")` ——
 * 它会派发真正的 beforeinput/input 事件，DOM 与原生的 input 监听都收得到；直接改文本
 * 节点是"绕过模型"，下一次 setValue/重排就把改动吞了。
 */
export function applyTag(token: TagToken, tag: string, insert?: (text: string) => void): boolean {
  const sel = window.getSelection();
  const node = sel?.anchorNode;
  if (!sel || !node || node.nodeType !== Node.TEXT_NODE) return false;
  const range = document.createRange();
  range.setStart(node, token.start);
  range.setEnd(node, token.end);
  sel.removeAllRanges();
  sel.addRange(range);
  const text = `#${tag} `;

  if (insert) {
    // 先删掉已经打出来的 `#prefix`（与 MarkdownSurface.wrap() 同一手法），
    // 再让编辑器在光标处插入完整标签，末尾带一个空格好接着写。
    try {
      document.execCommand("delete");
    } catch {
      range.deleteContents();
    }
    insert(text);
    return true;
  }

  try {
    if (typeof document.queryCommandSupported === "function" && document.queryCommandSupported("insertText")) {
      return document.execCommand("insertText", false, text);
    }
  } catch {
    /* 退回到手工替换 */
  }
  range.deleteContents();
  range.insertNode(document.createTextNode(text));
  const after = document.createRange();
  after.setStartAfter(node);
  after.collapse(true);
  sel.removeAllRanges();
  sel.addRange(after);
  return true;
}
