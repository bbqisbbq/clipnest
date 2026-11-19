// 工具栏按钮：可选清单 + 当前生效的选择。
//
// 为什么要这么一个模块：三个输入场景（详情 / 记一条 / 快速输入窗）用的是同一个
// Vditor，但需要的按钮不同；用户还想自己挑。设置存在 Settings.editorToolbar /
// composerToolbar 里，这里负责"id → 中文名"的目录与默认值，并给 MarkdownSurface
// 一个同步读取的入口（Vditor 的 options 只在初始化时读一次）。
//
// id 必须和 Vditor 的 toolbar item 名一致；`|` 是它约定的分隔线。

export type ToolbarItem = { id: string; label: string };

/** 可选按钮（顺序就是设置页里的展示顺序） */
export const TOOLBAR_CATALOG: ToolbarItem[] = [
  { id: "headings", label: "标题" },
  { id: "bold", label: "粗体" },
  { id: "italic", label: "斜体" },
  { id: "strike", label: "删除线" },
  { id: "link", label: "链接" },
  { id: "list", label: "无序列表" },
  { id: "ordered-list", label: "有序列表" },
  { id: "check", label: "任务清单" },
  { id: "quote", label: "引用" },
  { id: "line", label: "分隔线" },
  { id: "code", label: "代码块" },
  { id: "inline-code", label: "行内代码" },
  { id: "table", label: "表格" },
  { id: "undo", label: "撤销" },
  { id: "redo", label: "重做" },
  { id: "fullscreen", label: "全屏" },
  { id: "outline", label: "大纲" },
];

/** 只给内部用的分隔线 id */
export const TOOLBAR_DIVIDER = "|";

export const DEFAULT_DETAIL_TOOLBAR: string[] = [
  "headings", "bold", "italic", "strike", "|",
  "list", "ordered-list", "check", "|",
  "quote", "line", "code", "inline-code", "table", "link", "|",
  "undo", "redo", "|",
  "fullscreen",
];

export const DEFAULT_COMPOSER_TOOLBAR: string[] = [
  "bold", "italic", "list", "check", "inline-code", "link",
];

export type SurfacePresetName = "detail" | "composer" | "capture";

/**
 * 当前生效的工具栏。**模块级状态**是有意的：Vditor 只在 new 的时候读一次
 * options，所以这里不追求响应式 —— 改完设置后重新打开编辑器/重启即可生效
 * （设置页会提示「重新打开编辑器后生效」）。
 */
const state: Record<"detail" | "composer", string[]> = {
  detail: DEFAULT_DETAIL_TOOLBAR,
  composer: DEFAULT_COMPOSER_TOOLBAR,
};

/** 由 main.tsx 在启动时用设置里的值初始化一次。 */
export function applyToolbarSettings(detail?: string[] | null, composer?: string[] | null): void {
  state.detail = sanitize(detail, DEFAULT_DETAIL_TOOLBAR);
  state.composer = sanitize(composer, DEFAULT_COMPOSER_TOOLBAR);
}

/** 某个场景该显示哪些按钮。 */
export function toolbarFor(preset: SurfacePresetName): string[] {
  if (preset === "detail") return state.detail;
  if (preset === "composer") return state.composer;
  return []; // capture：工具栏整体隐藏，按钮由 Capture.tsx 自己画
}

/** 去掉不认识的 id、压缩连续分隔线；空结果回落到默认值。 */
function sanitize(input: string[] | null | undefined, fallback: string[]): string[] {
  if (!input || input.length === 0) return fallback;
  const known = new Set(TOOLBAR_CATALOG.map((i) => i.id));
  const out: string[] = [];
  for (const raw of input) {
    const id = String(raw ?? "").trim();
    if (id === TOOLBAR_DIVIDER) {
      if (out.length === 0 || out[out.length - 1] === TOOLBAR_DIVIDER) continue;
      out.push(TOOLBAR_DIVIDER);
      continue;
    }
    if (!known.has(id)) continue;
    out.push(id);
  }
  while (out.length > 0 && out[out.length - 1] === TOOLBAR_DIVIDER) out.pop();
  return out.length > 0 ? out : fallback;
}
