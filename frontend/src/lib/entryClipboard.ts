import { marked } from "marked";

// 回填剪贴板要的两份载荷。
//
// html 用**卡片同一套渲染器**（marked）产出，图片引用保持 `memoimg:<hash>` 不变 ——
// 换成真字节交给 Go 做（字节在 entry_blobs 里，前端拿不到全部）。
// plain 是纯文本兜底：把整段 Markdown 图片语法删掉，绝不把 `![](memoimg:…)`
// 丢进别人的聊天框（那既丢了图，又多一串垃圾）。
//
// 为什么不在 Go 里渲染 Markdown：那会变成第二套渲染器，卡片看到的和粘贴出去的内容
// 迟早会不一致。谁最会渲染这条正文，就由谁来渲染。

const MEMO_MD_IMG_RE = /!\[[^\]]*\]\(\s*memoimg:[0-9a-f]{8,64}[^)]*\)/g;

export type ClipboardPayload = { html: string; plain: string };

/** 只有正文里真的带图片时才值得走富文本那条路（纯文字走原来的纯文本更干净）。 */
export function hasInlineImages(content: string): boolean {
  return /memoimg:[0-9a-f]{8,64}/.test(content);
}

export function entryClipboardPayload(content: string): ClipboardPayload {
  const html = marked.parse(content, { async: false }) as string;
  const plain = content
    .replace(MEMO_MD_IMG_RE, "")
    .replace(/\r\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
  return { html, plain };
}
