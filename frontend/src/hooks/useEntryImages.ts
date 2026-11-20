import { useCallback, useEffect, useState } from "react";
import { ClipService } from "../lib/api";
import { collectImageHashes } from "../lib/images";

// 图片的按需加载：正文里是 memoimg:<hash>，真正要渲染时才去后端换 data URL。
// 缓存只增不减，所以换筛选器 / 翻页 / 展开编辑时，已经取过的图不会再请求一次。

export type ImageCache = Record<string, string>;

export function useEntryImages(texts: string[]) {
  const [imgCache, setImgCache] = useState<ImageCache>({});

  /** 取一批 hash 的 data URL；失败的（附件已清理）直接跳过。 */
  const ensureImages = useCallback(async (hashes: string[]): Promise<ImageCache> => {
    const next: ImageCache = {};
    for (const hash of hashes) {
      try {
        next[hash] = await ClipService.GetImage(hash);
      } catch {
        /* 附件已清理 */
      }
    }
    return next;
  }, []);

  useEffect(() => {
    const missing = collectImageHashes(texts).filter((h) => !imgCache[h]);
    if (missing.length === 0) return;
    let alive = true;
    void (async () => {
      const next = await ensureImages(missing);
      if (alive && Object.keys(next).length) setImgCache((p) => ({ ...p, ...next }));
    })();
    return () => {
      alive = false;
    };
  }, [texts, imgCache, ensureImages]);

  // 只为内部 effect 服务：外部拿 imgCache 就够了（导出没人用等于多一个过期 API）
  return { imgCache };
}
