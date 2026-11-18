import { useEffect, useRef } from "react";
import { EditorState, type Extension } from "@codemirror/state";
import {
  Decoration,
  EditorView,
  ViewPlugin,
  keymap,
  placeholder as cmPlaceholder,
  type DecorationSet,
  type ViewUpdate,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, insertNewlineAndIndent } from "@codemirror/commands";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { markdown } from "@codemirror/lang-markdown";
import { tags as t } from "@lezer/highlight";

// Markdown 编辑器：语法解析、着色、undo、IME、选区、滚动全部交给 CodeMirror。
// 我们只做三件事：把颜色映射到主题 token、给 #标签 上个色、把 Ctrl+Enter 接成保存。

// ---- 主题：颜色全部走 CSS 变量，深浅主题自动跟随 --------------------------

const themeExtension = EditorView.theme({
  "&": {
    height: "100%",
    fontSize: "12.8px",
    backgroundColor: "transparent",
    color: "var(--text)",
  },
  ".cm-scroller": {
    fontFamily: "var(--font-mono)",
    lineHeight: "1.7",
    overflow: "auto",
  },
  ".cm-content": {
    caretColor: "var(--text)",
    padding: "8px 0",
  },
  ".cm-line": { padding: "0 2px 0 0" },
  "&.cm-focused": { outline: "none" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--text)", borderLeftWidth: "1.5px" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection": {
    backgroundColor: "var(--accent-tint)",
  },
  ".cm-placeholder": { color: "var(--text-3)" },
  ".cm-activeLine": { backgroundColor: "transparent" },
  ".cm-gutters": { display: "none" },
  // #标签 由下面的插件打标记
  ".cm-md-tag": {
    color: "var(--accent)",
    backgroundColor: "var(--accent-tint)",
    borderRadius: "3px",
  },
});

const highlightStyle = HighlightStyle.define([
  { tag: t.heading1, color: "var(--accent)", fontWeight: "600" },
  { tag: t.heading2, color: "var(--accent)", fontWeight: "600" },
  { tag: t.heading3, color: "var(--accent)", fontWeight: "600" },
  { tag: t.heading4, color: "var(--accent)" },
  { tag: t.heading5, color: "var(--accent)" },
  { tag: t.heading6, color: "var(--accent)" },
  { tag: t.strong, color: "var(--text)", fontWeight: "700" },
  { tag: t.emphasis, color: "var(--text-2)", fontStyle: "italic" },
  { tag: t.strikethrough, color: "var(--text-3)", textDecoration: "line-through" },
  { tag: t.link, color: "var(--accent)", textDecoration: "underline" },
  { tag: t.url, color: "var(--text-3)" },
  { tag: t.monospace, color: "var(--accent-2)", backgroundColor: "var(--accent-tint)" },
  { tag: t.quote, color: "var(--text-2)", fontStyle: "italic" },
  { tag: t.list, color: "var(--accent-2)" },
  { tag: t.processingInstruction, color: "var(--text-3)" }, // # * ` [ ] 等标记
  { tag: t.contentSeparator, color: "var(--text-3)" },
]);

// ---- #标签：Markdown 语法树里没有这个概念，用一个小装饰器补上 -------------

const tagPattern = /#[\p{L}\p{N}_/-]+/gu;

function tagDecorations(view: EditorView): DecorationSet {
  const marks: ReturnType<Decoration["range"]>[] = [];
  for (const { from, to } of view.visibleRanges) {
    const text = view.state.doc.sliceString(from, to);
    for (const match of text.matchAll(tagPattern)) {
      const start = from + (match.index ?? 0);
      // 行首或空白/括号后的 # 才算标签，避免把 issue#12 之类误标
      const prev = start > 0 ? view.state.doc.sliceString(start - 1, start) : " ";
      if (!/[\s(（]/.test(prev)) continue;
      marks.push(Decoration.mark({ class: "cm-md-tag" }).range(start, start + match[0].length));
    }
  }
  return Decoration.set(marks, true);
}

const tagHighlighter = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;
    constructor(view: EditorView) {
      this.decorations = tagDecorations(view);
    }
    update(update: ViewUpdate) {
      if (update.docChanged || update.viewportChanged) {
        this.decorations = tagDecorations(update.view);
      }
    }
  },
  { decorations: (v) => v.decorations },
);

// ---- React 包装 ----------------------------------------------------------

type Props = {
  value: string;
  onChange: (next: string) => void;
  onSave?: () => void;
  placeholder?: string;
  autoFocus?: boolean;
};

export default function MarkdownEditor({ value, onChange, onSave, placeholder, autoFocus }: Props) {
  const hostRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  const onSaveRef = useRef(onSave);
  onChangeRef.current = onChange;
  onSaveRef.current = onSave;

  useEffect(() => {
    if (!hostRef.current) return;

    const extensions: Extension[] = [
      history(),
      markdown(),
      EditorView.lineWrapping,
      syntaxHighlighting(highlightStyle),
      themeExtension,
      tagHighlighter,
      cmPlaceholder(placeholder ?? ""),
      keymap.of([
        {
          key: "Mod-Enter",
          run: () => {
            onSaveRef.current?.();
            return true;
          },
        },
        { key: "Enter", run: insertNewlineAndIndent },
        ...defaultKeymap,
        ...historyKeymap,
      ]),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          onChangeRef.current(update.state.doc.toString());
        }
      }),
    ];

    const view = new EditorView({
      state: EditorState.create({ doc: value, extensions }),
      parent: hostRef.current,
    });
    viewRef.current = view;
    if (autoFocus) view.focus();

    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // 只初始化一次；外部 value 变化由下面的 effect 同步
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 外部换了内容（比如点了另一条备忘）→ 更新文档，但别打断正在输入
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const current = view.state.doc.toString();
    if (current === value) return;
    view.dispatch({
      changes: { from: 0, to: current.length, insert: value },
    });
  }, [value]);

  return <div className="md-editor" ref={hostRef} />;
}
