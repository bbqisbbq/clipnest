// 图片引用的解析：正文里存的是 memoimg:<hash>，渲染时要换成真实 data URL。
//
// 抽成独立模块，是因为"正文里的图片引用"这件事在列表卡片、预览、编辑器里都要
// 处理，而它跟具体组件无关。

export const MEMO_IMG_RE = /memoimg:([0-9a-f]{8,})/g;

// 1x1 透明占位：图片还没加载完时先顶上，避免出现"破图"图标
export const BLANK_PIXEL =
  "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";

/** 把 HTML 里的 memoimg:<hash> 换成缓存里的 data URL（未命中用透明占位）。 */
export function resolveImages(html: string, cache: Record<string, string>): string {
  return html.replace(MEMO_IMG_RE, (_whole, hash: string) => cache[hash] ?? BLANK_PIXEL);
}

/** 从一段正文里取出所有图片 hash（用于按需加载）。 */
export function collectImageHashes(texts: string[]): string[] {
  const out = new Set<string>();
  for (const text of texts) {
    for (const m of text.matchAll(MEMO_IMG_RE)) out.add(m[1]);
  }
  return [...out];
}
