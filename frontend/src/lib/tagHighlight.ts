// 给编辑器里的 `#标签` 上色（IR 模式里标签就是普通文本，没有任何元素能挂样式）。
//
// 用 Chromium 的 **CSS Custom Highlight API**（`CSS.highlights` + `::highlight()`）：
// 它只注册"要上色的文本范围"，**不往 DOM 里插节点**。对 contenteditable 来说这一点
// 很关键——往正文里包 `<span>` 会把光标打断、把 Vditor 的 IR 结构弄乱（撤销也会乱）。
//
// 不支持该 API 的环境（老 WebView2）自动退化成"没有高亮"，不影响编辑。

import { isMaskedNode } from "./privacyMask";

const HIGHLIGHT_NAME = "clipnest-tag";// 与后端 tagPattern 同一字符集（internal/clip/entry.go）
const TAG_RE = /#([\p{L}\p{N}_\-\/]+)/gu;
// `#` 前面必须是行首/空白/标点，`abc#def` 不算标签
const PREV_OK = /[\s(（[【「《,，;；:：>|、/]/;

const hosts = new Set<HTMLElement>();

function supported(): boolean {
  return typeof CSS !== "undefined" && "highlights" in CSS && typeof Highlight !== "undefined";
}

/** 一个编辑器实例挂上来（用于多编辑器时把范围并起来，别互相覆盖）。 */
export function registerTagHost(host: HTMLElement): void {
  hosts.add(host);
  repaintTags();
}

export function unregisterTagHost(host: HTMLElement): void {
  hosts.delete(host);
  repaintTags();
}

/** 重算所有已注册编辑器里的标签范围。输入、换内容、挂载之后各调一次。 */
export function repaintTags(): void {
  if (!supported()) return;
  if (hosts.size === 0) {
    CSS.highlights.delete(HIGHLIGHT_NAME);
    return;
  }
  const ranges: Range[] = [];
  for (const host of hosts) {
    if (!host.isConnected) {
      hosts.delete(host);
      continue;
    }
    collectRanges(host, ranges);
  }
  if (ranges.length === 0) {
    CSS.highlights.delete(HIGHLIGHT_NAME);
    return;
  }
  CSS.highlights.set(HIGHLIGHT_NAME, new Highlight(...ranges));
}

function collectRanges(root: HTMLElement, out: Range[]): void {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const el = node.parentElement;
      if (!el) return NodeFilter.FILTER_REJECT;
      // 只排 `code`：**IR 模式的正文本体自己就是一个 `<pre class="vditor-reset">`**，
      // 按 `pre` 排除等于把整个编辑区排除掉（实测 ranges 恒为 0、一个高亮都不出）。
      // 围栏代码块里的文字在 `<code>` 里，照样会被排掉。
      if (el.closest("code")) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  while (walker.nextNode()) {
    const text = walker.currentNode as Text;
    // 隐私模式下被遮住的段落：连标签底色一起省掉 —— 否则灰雾里会透出一块强调色，
    // 等于把"这里有标签"漏出去。
    if (isMaskedNode(text)) continue;
    const data = text.data;
    if (!data.includes("#")) continue;
    TAG_RE.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = TAG_RE.exec(data)) !== null) {
      const at = m.index;
      if (at > 0 && !PREV_OK.test(data[at - 1])) continue;
      const range = document.createRange();
      range.setStart(text, at);
      range.setEnd(text, at + m[0].length);
      out.push(range);
    }
  }
}
