// 隐私模式：只有"光标所在的那个段落"保持可读，其余段落遮成极淡（色调贴近背景）。
//
// 为什么又是 CSS Highlight API 而不是往段落上挂 class / style：
//   ① 这是可编辑区（Vditor IR）。加 class / data-* / style 都会被它当正文读回去
//      （`getValue()` 走 innerHTML → HTML2Md），有把标记写进 markdown 的风险；
//   ② Range 自己跟着版面走：滚动、换行、缩放、窗口大小变化都不用重算。
//
// 纯视觉：不碰正文、不碰保存、不碰已存的内容（用户要的就是这个边界）。
// 不支持该 API 的环境自动退化成"什么都没有"，编辑不受影响。

const HIGHLIGHT_NAME = "clipnest-mask";

// 什么算"段落"：IR 的正文是一个 `<pre class="vditor-reset">`，块级元素是这些。
// `pre` 要排除正文本体自己 —— 它就是那个 <pre>，算进去等于整块只有一个段落。
const BLOCK_SELECTOR = "p,li,blockquote,h1,h2,h3,h4,h5,h6,pre:not(.vditor-reset),td,th";

const hosts = new Set<HTMLElement>();
/** 每个编辑器"上一次的光标段"。失焦时按它继续遮，不至于一离开就整屏露字。 */
const lastActive = new WeakMap<HTMLElement, number>();
/** 本轮被遮住的文字节点：标签高亮要跳过它们（见 tagHighlight.ts）。 */
const maskedNodes = new Set<Text>();

let enabled = false;

function supported(): boolean {
  return typeof CSS !== "undefined" && "highlights" in CSS && typeof Highlight !== "undefined";
}

// ---- 纯逻辑（可用 node --experimental-strip-types 直接测）------------------

/**
 * 哪些片段要被遮：所有"不属于光标所在段"的片段。
 *
 * @param blocks 每个片段所属的段号（同段相同、不同段不同）
 * @param active 光标所在段号；负数 = 还不知道光标在哪 → 一个都不遮（宁可不遮也不乱遮）
 */
export function pickMasked(blocks: number[], active: number): number[] {
  if (active < 0) return [];
  const out: number[] = [];
  for (let i = 0; i < blocks.length; i++) {
    if (blocks[i] !== active) out.push(i);
  }
  return out;
}

// ---- 注册与重绘 ----------------------------------------------------------

export function registerPrivacyHost(host: HTMLElement): void {
  hosts.add(host);
  repaintPrivacy();
}

export function unregisterPrivacyHost(host: HTMLElement): void {
  hosts.delete(host);
  lastActive.delete(host);
  repaintPrivacy();
}

export function setPrivacyEnabled(on: boolean): void {
  if (on === enabled) return;
  enabled = on;
  repaintPrivacy();
}

export function isPrivacyEnabled(): boolean {
  return enabled;
}

/** 这个文字节点此刻被遮住了吗（标签高亮据此跳过，免得灰雾里透出一块强调色）。 */
export function isMaskedNode(node: Node | null): boolean {
  return node instanceof Text && maskedNodes.has(node);
}

/** 重算所有已注册编辑器的遮罩。输入、点选、切换开关、挂载之后各调一次。 */
export function repaintPrivacy(): void {
  maskedNodes.clear();
  if (!supported()) return;
  if (!enabled || hosts.size === 0) {
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

/** 一段要遮的文字：`node` 里 [start, end) 这一段，属于第 `block` 段。 */
type Segment = { node: Text; start: number; end: number; block: number };

function collectRanges(host: HTMLElement, out: Range[]): void {
  const body = editorBody(host);
  if (!body) return;

  const segments = collectSegments(body);
  if (segments.length === 0) return;
  const total = Math.max(...segments.map((s) => s.block)) + 1;

  const caret = caretBlock(body, segments);
  if (caret >= 0) lastActive.set(host, caret);
  let active = caret;
  if (active < 0) active = lastActive.get(host) ?? -1;
  // 段落数变少了（删内容、换条目）：把记住的段号夹回来。
  if (active >= total) active = total - 1;
  // 从没在这个编辑器里放过光标：默认最后一段可读——打开输入框时光标就在末尾。
  if (active < 0) active = total - 1;

  for (const i of pickMasked(
    segments.map((s) => s.block),
    active,
  )) {
    const seg = segments[i];
    const range = document.createRange();
    range.setStart(seg.node, seg.start);
    range.setEnd(seg.node, seg.end);
    out.push(range);
    maskedNodes.add(seg.node);
  }
}

/**
 * 把正文切成"段"。
 *
 * 两种排版都得算对，因为它们会同时存在：
 *   · `<p>甲</p><p>乙</p>`：块级元素各成一段（正常打字时就是这样）；
 *   · `<p>甲\n乙\n丙</p>`：**一个 <p> 里带字面换行**。Vditor 的 IR 本体是 `<pre>`，
 *     默认 `white-space: pre-wrap`，所以字面 `\n` 会真的显示成换行 —— 程序化灌进去的
 *     草稿（比如"恢复上次未保存的草稿"）就长这样，只有块级元素判断的话整篇算一段、
 *     一个都不遮（这个坑实测过：日志里 total=1）。
 */
function collectSegments(body: HTMLElement): Segment[] {
  const out: Segment[] = [];
  const ownerIndex = new Map<HTMLElement, number>();
  const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);

  let owner: HTMLElement | null = null;
  let block = -1;
  let fresh = true; // 下一个片段要开新段
  let first = true;

  while (walker.nextNode()) {
    const node = walker.currentNode as Text;
    const data = node.data;
    if (!data) continue;

    const next = blockOf(node, body);
    if (next !== owner) {
      owner = next;
      if (next && !ownerIndex.has(next)) ownerIndex.set(next, ownerIndex.size);
      fresh = true;
    }

    let pos = 0;
    while (pos < data.length) {
      const nl = data.indexOf("\n", pos);
      const end = nl < 0 ? data.length : nl;
      if (end > pos) {
        if (fresh) {
          if (first) {
            block = 0;
            first = false;
          } else {
            block++;
          }
          fresh = false;
        }
        if (block >= 0) out.push({ node, start: pos, end, block });
      }
      if (nl < 0) break;
      fresh = true; // 跨过换行 = 下一段
      pos = nl + 1;
    }
  }
  return out;
}

/**
 * 可编辑的那块正文。
 *
 * 不能用 `querySelector(".vditor-reset")`：Vditor 一次把 IR / wysiwyg 两套
 * `<pre class="vditor-reset">` 都建出来（只显示一套），排在 DOM 前面那套是空的 ——
 * "取第一个"拿到的是空壳，遮罩范围恒为空（实测：resets=[#0 空 kids=0 | #2 有字]）。
 */
function editorBody(host: HTMLElement): HTMLElement | null {
  const all = Array.from(host.querySelectorAll<HTMLElement>(".vditor-reset"));
  if (all.length === 0) return null;

  // ① 光标在哪块就选哪块：点进编辑器之后这是最准的判据。
  const anchor = document.getSelection()?.anchorNode ?? null;
  if (anchor) {
    for (const el of all) if (el.contains(anchor)) return el;
  }
  // ② 否则取"可编辑 + 真的显示着"的那块，多条时取文字最多的。
  const visible = all.filter((el) => el.isContentEditable && el.offsetParent !== null);
  const pool = visible.length ? visible : all;
  return pool.reduce(
    (best, el) => ((el.textContent?.length ?? 0) > (best.textContent?.length ?? 0) ? el : best),
    pool[0],
  );
}

/** 文字节点所属的段（最内层的块级祖先）；正文本体自己不算一段。 */
function blockOf(node: Node, body: HTMLElement): HTMLElement | null {
  const el = node.parentElement;
  if (!el || !body.contains(el)) return null;
  const hit = el.closest(BLOCK_SELECTOR) as HTMLElement | null;
  if (hit && hit !== body && body.contains(hit)) return hit;
  // 直接躺在正文本体里的散字：自己算一段
  return el === body ? null : el;
}

/** 光标所在段的段号；光标不在这个编辑器里就返回 -1。 */
function caretBlock(body: HTMLElement, segments: Segment[]): number {
  const sel = document.getSelection();
  if (!sel || sel.rangeCount === 0 || !sel.anchorNode) return -1;
  const anchor = sel.anchorNode;
  if (!body.contains(anchor)) return -1;

  if (anchor instanceof Text) {
    const off = sel.anchorOffset;
    for (const seg of segments) {
      if (seg.node === anchor && off >= seg.start && off <= seg.end) return seg.block;
    }
    // 落在换行符上（段的边界）：归给前一段
    let prev = -1;
    for (const seg of segments) {
      if (seg.node === anchor && seg.end <= off) prev = seg.block;
    }
    if (prev >= 0) return prev;
  }
  // 空段落（刚按了回车还没打字）没有文字可归：保持上一段可读。
  return -1;
}
