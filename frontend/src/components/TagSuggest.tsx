import { createPortal } from "react-dom";

// 输入框里打 `#` 时弹出的标签候选列表。
//
// 只画不判断：候选、高亮下标、屏幕坐标都由 MarkdownSurface 算好传进来。
// 挂到 body 上是刻意的 —— 编辑区在可滚动容器里，就地渲染会被裁掉。

export type TagSuggestAnchor = { left: number; top: number };

type Props = {
  items: string[];
  active: number;
  anchor: TagSuggestAnchor;
  onPick: (tag: string) => void;
  onActive: (index: number) => void;
};

export default function TagSuggest({ items, active, anchor, onPick, onActive }: Props) {
  if (items.length === 0) return null;
  return createPortal(
    <div
      className="tag-suggest"
      style={{ left: anchor.left, top: anchor.top }}
      role="listbox"
      aria-label="已有标签"
      // 别把焦点从编辑器抢走：取焦点一变，选区（光标位置）就没了，替换也就落空
      onMouseDown={(e) => e.preventDefault()}
    >
      {items.map((tag, i) => (
        <div
          key={tag}
          role="option"
          aria-selected={i === active}
          className={`tag-suggest-item${i === active ? " is-active" : ""}`}
          onMouseEnter={() => onActive(i)}
          onMouseDown={(e) => {
            e.preventDefault();
            onPick(tag);
          }}
        >
          <span className="tag-suggest-hash">#</span>
          <span className="tag-suggest-name">{tag}</span>
        </div>
      ))}
      <div className="tag-suggest-foot">↑↓ 选择 · Enter/Tab 插入 · Esc 关闭</div>
    </div>,
    document.body,
  );
}
