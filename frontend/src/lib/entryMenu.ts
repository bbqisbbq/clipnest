import type { Entry } from "./api";

// 卡片右键菜单的**条目清单**（纯函数，不碰 React、不碰 DOM）。
//
// 单独抽出来的理由：这是"哪些操作对哪条内容有意义"的全部规则 —— 图片才有 OCR、
// 已置顶才显示「取消置顶」、有缩写才显示「取消片段」。放在组件里就只能靠手点验证，
// 抽出来之后 scratch/entry-menu-check.mjs 能一条条断言（回归时不用再点鼠标）。

export type MenuItem =
  | {
      kind?: "item";
      id: string;
      label: string;
      /** 右侧灰色小字：快捷键、字数、当前状态 */
      hint?: string;
      danger?: boolean;
      disabled?: boolean;
      onClick: () => void;
    }
  | { kind: "separator"; id: string };

export type MenuHandlers = {
  copy: () => void;
  openDetail: () => void;
  togglePin: () => void;
  toggleClipboard: () => void;
  /** 设为片段 / 取消片段，由调用方按 entry.triggerKey 决定做哪件 */
  toggleSnippet: () => void;
  toggleArchive: () => void;
  remove: () => void;
  /** 识别图里的文字；force=true 表示「重新识别」 */
  ocr: (force: boolean) => void;
  copyOcr: () => void;
};

/**
 * entryMenuItems 按条目内容决定菜单项。
 *
 * 顺序是固定的：先"内容相关"（复制、OCR），再分隔，中间是"组织"（详情、置顶、
 * 粘贴列表、片段），再分隔，最后是"破坏性"（归档、删除）。删除永远在最后且是红的 ——
 * 右键盘菜单最忌讳把危险项放在第一行。
 */
export function entryMenuItems(e: Entry, h: MenuHandlers): MenuItem[] {
  const items: MenuItem[] = [{ id: "copy", label: "复制内容", onClick: h.copy }];

  if (e.kind === "image") {
    const recognized = e.ocrState === "done";
    items.push({
      id: "ocr",
      label: recognized ? "重新识别文字" : "识别图里的文字",
      hint: e.ocrState === "pending" ? "识别中" : undefined,
      onClick: () => h.ocr(recognized),
    });
    if (e.ocrText) {
      items.push({
        id: "copy-ocr",
        label: "复制识别文字",
        hint: `${e.ocrText.length} 字`,
        onClick: h.copyOcr,
      });
    }
  }

  items.push({ id: "sep-1", kind: "separator" });
  items.push({ id: "detail", label: "打开详情", onClick: h.openDetail });
  items.push({
    id: "pin",
    label: e.pinned ? "取消置顶" : "置顶",
    onClick: h.togglePin,
  });
  items.push({
    id: "clipboard",
    label: (e.actions ?? []).includes("clipboard") ? "移出粘贴列表" : "加入粘贴列表",
    onClick: h.toggleClipboard,
  });
  items.push({
    id: "snippet",
    label: e.triggerKey ? "取消片段" : "设为片段",
    hint: e.triggerKey || undefined,
    onClick: h.toggleSnippet,
  });

  items.push({ id: "sep-2", kind: "separator" });
  items.push({
    id: "archive",
    label: e.archived ? "恢复" : "归档",
    onClick: h.toggleArchive,
  });
  items.push({
    id: "delete",
    label: "删除",
    danger: true,
    onClick: h.remove,
  });
  return items;
}
