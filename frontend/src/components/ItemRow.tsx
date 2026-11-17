import type { Item } from "../lib/api";
import { KIND_LABEL } from "../lib/api";
import { formatBytes, formatRelative } from "../lib/format";
import { IconStar, kindIcon } from "./Icons";

type Props = {
  item: Item;
  active: boolean;
  onSelect: () => void;
  onCopy: () => void;
  onTogglePin: () => void;
};

export default function ItemRow({ item, active, onSelect, onCopy, onTogglePin }: Props) {
  const Icon = kindIcon(item.kind);
  const missing = item.kind === "files" ? (item.files ?? []).filter((f) => !f.exists).length : 0;

  return (
    <div
      className={`row ${active ? "is-active" : ""}`}
      onClick={onSelect}
      onDoubleClick={onCopy}
      role="button"
      tabIndex={-1}
    >
      <div className={`row-icon kind-${item.kind}`}>
        <Icon size={17} />
      </div>

      <div className="row-main">
        <div className="row-preview">{item.preview || "（空）"}</div>
        <div className="row-meta">
          <span className={`chip kind-${item.kind}`}>{KIND_LABEL[item.kind] ?? item.kind}</span>
          {item.sourceApp ? <span className="muted">{item.sourceApp}</span> : null}
          <span className="muted">{formatBytes(item.size)}</span>
          {item.seenCount > 1 ? <span className="muted">×{item.seenCount}</span> : null}
          {missing > 0 ? <span className="warn">{missing} 项已失效</span> : null}
          <span className="row-time">{formatRelative(item.updatedAt)}</span>
        </div>
      </div>

      <button
        className={`icon-btn ${item.pinned ? "is-on" : ""}`}
        title={item.pinned ? "取消收藏" : "收藏"}
        onClick={(e) => {
          e.stopPropagation();
          onTogglePin();
        }}
      >
        <IconStar size={15} filled={item.pinned} />
      </button>
    </div>
  );
}
