import type { Item } from "../lib/api";
import { KIND_LABEL } from "../lib/api";
import { formatBytes, formatRelative } from "../lib/format";
import { IconCopy, IconStar, IconTrash, kindIcon } from "./Icons";

type Props = {
  item: Item;
  active: boolean;
  onSelect: () => void;
  onCopy: () => void;
  onTogglePin: () => void;
  onDelete: () => void;
};

export default function ItemRow({ item, active, onSelect, onCopy, onTogglePin, onDelete }: Props) {
  const Icon = kindIcon(item.kind);
  const missing = item.kind === "files" ? (item.files ?? []).filter((f) => !f.exists).length : 0;

  return (
    <div
      className={`row ${active ? "is-active" : ""}`}
      onClick={onSelect}
      onDoubleClick={onCopy}
      role="option"
      aria-selected={active}
      tabIndex={-1}
    >
      <span className={`row-icon kind-${item.kind}`}>
        <Icon size={16} />
      </span>

      <div className="row-body">
        <div className="row-title">{item.preview || "（空）"}</div>
        <div className="row-meta">
          <span className={`chip kind-${item.kind}`}>{KIND_LABEL[item.kind] ?? item.kind}</span>
          {item.sourceApp ? <span>{item.sourceApp}</span> : null}
          <span>{formatBytes(item.size)}</span>
          {item.seenCount > 1 ? <span>×{item.seenCount}</span> : null}
          {missing > 0 ? <span className="is-warn">{missing} 项失效</span> : null}
          <span className="row-time">{formatRelative(item.updatedAt)}</span>
        </div>
      </div>

      <div className="row-actions">
        <button
          className="icon-btn"
          title="复制到剪贴板"
          onClick={(e) => {
            e.stopPropagation();
            onCopy();
          }}
        >
          <IconCopy size={14} />
        </button>
        <button
          className={`icon-btn ${item.pinned ? "is-on" : ""}`}
          title={item.pinned ? "取消收藏" : "收藏"}
          onClick={(e) => {
            e.stopPropagation();
            onTogglePin();
          }}
        >
          <IconStar size={14} filled={item.pinned} />
        </button>
        <button
          className="icon-btn is-danger"
          title="删除"
          onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }}
        >
          <IconTrash size={14} />
        </button>
      </div>
    </div>
  );
}
