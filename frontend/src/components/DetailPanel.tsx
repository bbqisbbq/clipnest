import { useEffect, useState } from "react";
import type { Item } from "../lib/api";
import { KIND_LABEL } from "../lib/api";
import { formatBytes, formatDateTime, formatRelative } from "../lib/format";
import { IconCopy, IconClose, IconExternal, IconFiles, IconFolder, IconStar, IconTrash, kindIcon } from "./Icons";

type Props = {
  item: Item | null;
  imageData: string | null;
  onCopy: () => void;
  onTogglePin: () => void;
  onDelete: () => void;
  onOpen: () => void;
  onClose: () => void;
  onRefresh: () => void;
};

export default function DetailPanel({ item, imageData, onCopy, onTogglePin, onDelete, onOpen, onClose, onRefresh }: Props) {
  const [showHTML, setShowHTML] = useState(false);

  useEffect(() => {
    setShowHTML(false);
  }, [item?.id]);

  if (!item) {
    return (
      <section className="detail is-empty">
        <div className="empty-detail">
          <p>选择左侧条目查看详情</p>
          <span>双击条目可直接复制回剪贴板</span>
        </div>
      </section>
    );
  }

  const Icon = kindIcon(item.kind);
  const canOpen = item.kind === "link" || item.kind === "files" || item.kind === "image";

  return (
    <section className="detail">
      <header className="detail-head">
        <div className={`row-icon kind-${item.kind}`}>
          <Icon size={17} />
        </div>
        <div className="detail-title">
          <strong>{KIND_LABEL[item.kind] ?? item.kind}</strong>
          <span>{formatRelative(item.updatedAt)}</span>
        </div>
        <button className="icon-btn" title="关闭详情" onClick={onClose}>
          <IconClose size={15} />
        </button>
      </header>

      <div className="detail-body">
        {(item.kind === "text" || item.kind === "link") && (
          <pre className="text-body">{item.text}</pre>
        )}

        {item.kind === "rich" && (
          <>
            <pre className="text-body">{item.text || "（无纯文本）"}</pre>
            {item.html ? (
              <>
                <button className="ghost-btn" onClick={() => setShowHTML((v) => !v)}>
                  {showHTML ? "隐藏 HTML 源码" : "查看 HTML 源码"}
                </button>
                {showHTML ? <pre className="code-body">{item.html}</pre> : null}
              </>
            ) : null}
          </>
        )}

        {item.kind === "image" && (
          <div className="image-wrap">
            {imageData ? (
              <img src={imageData} alt="剪贴板图片" />
            ) : (
              <div className="image-loading">正在加载图片…</div>
            )}
            <span className="muted">
              {item.width ?? 0} × {item.height ?? 0} · {formatBytes(item.size)}
            </span>
          </div>
        )}

        {item.kind === "files" && (
          <div className="file-list">
            {(item.files ?? []).map((f) => (
              <div className={`file-item ${f.exists ? "" : "is-missing"}`} key={f.path}>
                <IconFiles size={15} />
                <div className="file-info">
                  <span className="file-name" title={f.name}>
                    {f.name}
                  </span>
                  <span className="file-path" title={f.path}>
                    {f.path}
                  </span>
                </div>
                <span className="file-size">{f.exists ? formatBytes(f.size) : "已失效"}</span>
              </div>
            ))}
            <div className="file-note">
              文件只记录引用（路径），不复制实体。原文件被移动或删除后会标记为「已失效」。
            </div>
            <button className="ghost-btn" onClick={onRefresh}>
              重新检查文件是否存在
            </button>
          </div>
        )}

        <dl className="meta-grid">
          <div>
            <dt>来源程序</dt>
            <dd>{item.sourceApp || "未知"}</dd>
          </div>
          <div>
            <dt>大小</dt>
            <dd>{formatBytes(item.size)}</dd>
          </div>
          <div>
            <dt>首次记录</dt>
            <dd>{formatDateTime(item.createdAt)}</dd>
          </div>
          <div>
            <dt>最近使用</dt>
            <dd>{formatDateTime(item.updatedAt)}</dd>
          </div>
          <div>
            <dt>记录次数</dt>
            <dd>{item.seenCount}</dd>
          </div>
          <div>
            <dt>复制次数</dt>
            <dd>{item.copyCount}</dd>
          </div>
        </dl>
      </div>

      <footer className="detail-actions">
        <button className="primary-btn" onClick={onCopy}>
          <IconCopy size={15} />
          复制
        </button>
        <button className={`ghost-btn ${item.pinned ? "is-on" : ""}`} onClick={onTogglePin}>
          <IconStar size={15} filled={item.pinned} />
          {item.pinned ? "已收藏" : "收藏"}
        </button>
        {canOpen ? (
          <button className="ghost-btn" onClick={onOpen}>
            {item.kind === "link" ? <IconExternal size={15} /> : <IconFolder size={15} />}
            {item.kind === "link" ? "打开链接" : item.kind === "image" ? "用默认应用打开" : "定位文件"}
          </button>
        ) : null}
        <button className="danger-btn" onClick={onDelete}>
          <IconTrash size={15} />
          删除
        </button>
      </footer>
    </section>
  );
}
