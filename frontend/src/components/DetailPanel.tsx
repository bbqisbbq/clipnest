import { useEffect, useState } from "react";
import type { Item } from "../lib/api";
import { KIND_LABEL } from "../lib/api";
import { formatBytes, formatDateTime, formatRelative } from "../lib/format";
import { IconClose, IconCopy, IconExternal, IconFiles, IconFolder, IconStar, IconTrash, kindIcon } from "./Icons";

type Props = {
  item: Item | null;
  imageData: string | null;
  onCopy: () => void;
  onTogglePin: () => void;
  onDelete: () => void;
  onOpen: () => void;
  onClose: () => void;
  onRefresh: () => void;
  onSaveAsSnippet: () => void;
};

export default function DetailPanel(props: Props) {
  const { item, imageData } = props;
  const [showSource, setShowSource] = useState(false);

  useEffect(() => {
    setShowSource(false);
  }, [item?.id]);

  if (!item) {
    return (
      <aside className="detail is-empty">
        <div className="detail-placeholder">
          <svg viewBox="0 0 24 24" width="26" height="26" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="5" width="18" height="14" rx="2.4" />
            <path d="M7 10h10M7 14h6" />
          </svg>
          <p>选择左侧条目查看详情</p>
          <span>双击条目或按 Enter 直接复制</span>
        </div>
      </aside>
    );
  }

  const Icon = kindIcon(item.kind);
  const canOpen = item.kind === "link" || item.kind === "files" || item.kind === "image";
  const source = item.html || item.rtf || "";
  const formatLabel = item.formatName || "CF_" + String(item.formatId ?? "?");

  return (
    <aside className="detail">
      <header className="detail-head">
        <span className={`row-icon kind-${item.kind}`}>
          <Icon size={16} />
        </span>
        <div className="detail-title">
          <strong>{KIND_LABEL[item.kind] ?? item.kind}</strong>
          <span>{formatRelative(item.updatedAt)}</span>
        </div>
        <button className="icon-btn" title="关闭详情" onClick={props.onClose}>
          <IconClose size={15} />
        </button>
      </header>

      <div className="detail-body">
        {(item.kind === "text" || item.kind === "link") && <pre className="code-block">{item.text}</pre>}

        {item.kind === "rich" && (
          <>
            <pre className="code-block">{item.text || "（无纯文本回退）"}</pre>
            {source ? (
              <>
                <button className="btn btn-ghost btn-sm" onClick={() => setShowSource((v) => !v)}>
                  {showSource ? "隐藏源码" : item.rtf ? "查看 RTF 源码" : "查看 HTML 源码"}
                </button>
                {showSource ? <pre className="code-block is-dim">{source}</pre> : null}
              </>
            ) : null}
          </>
        )}

        {item.kind === "image" && (
          <div className="image-card">
            {imageData ? <img src={imageData} alt="剪贴板图片" /> : <div className="image-loading">加载中…</div>}
            <span>
              {item.width ?? 0} × {item.height ?? 0} · {formatBytes(item.size)}
            </span>
          </div>
        )}

        {item.kind === "files" && (
          <div className="file-list">
            {(item.files ?? []).map((f) => (
              <div className={"file-item" + (f.exists ? "" : " is-missing")} key={f.path}>
                <IconFiles size={14} />
                <div className="file-text">
                  <span className="file-name" title={f.name}>
                    {f.name}
                  </span>
                  <span className="file-path" title={f.path}>
                    {f.path}
                  </span>
                </div>
                <span className="file-size">{f.exists ? formatBytes(f.size) : "失效"}</span>
              </div>
            ))}
            <div className="note">
              只记录引用（路径），不复制实体文件。原文件被移动或删除会标记为失效。
              <button className="btn btn-ghost btn-sm" onClick={props.onRefresh}>
                重新检查
              </button>
            </div>
          </div>
        )}

        {item.kind === "custom" && (
          <div className="custom-card">
            <div className="custom-format">
              {formatLabel}
              <span>{formatBytes(item.size)}</span>
            </div>
            <p className="note">这是 ClipNest 不认识的私有格式，已按原始字节保存；「复制」会原样写回剪贴板。</p>
          </div>
        )}

        <dl className="kv">
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
            <dt>记录 / 复制</dt>
            <dd>
              {item.seenCount} / {item.copyCount}
            </dd>
          </div>
          <div>
            <dt>收藏</dt>
            <dd>{item.pinned ? "是" : "否"}</dd>
          </div>
        </dl>
      </div>

      <footer className="detail-actions">
        <button className="btn btn-primary" onClick={props.onCopy}>
          <IconCopy size={14} />
          复制
        </button>
        <button className={"btn btn-ghost" + (item.pinned ? " is-on" : "")} onClick={props.onTogglePin}>
          <IconStar size={14} filled={item.pinned} />
          {item.pinned ? "已收藏" : "收藏"}
        </button>
        <button className="btn btn-ghost" onClick={props.onSaveAsSnippet} title="把这条历史存成可复用的片段（之后可配缩写）">
          存为片段
        </button>
        {canOpen ? (
          <button className="btn btn-ghost" onClick={props.onOpen}>
            {item.kind === "link" ? <IconExternal size={14} /> : <IconFolder size={14} />}
            {item.kind === "link" ? "打开" : item.kind === "image" ? "打开" : "定位"}
          </button>
        ) : null}
        <button
          className="btn btn-danger"
          title="只删除 ClipNest 的历史记录，不影响原文件或系统剪贴板"
          onClick={props.onDelete}
        >
          <IconTrash size={14} />
          删除
        </button>
      </footer>
    </aside>
  );
}
