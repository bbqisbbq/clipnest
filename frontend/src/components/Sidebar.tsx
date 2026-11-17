import type { Stats } from "../lib/api";
import { IconAll, IconFiles, IconImage, IconLink, IconPause, IconPlay, IconRich, IconSettings, IconStar, IconText } from "./Icons";

type Props = {
  view: "history" | "settings";
  kind: string;
  pinnedOnly: boolean;
  stats: Stats | null;
  capturing: boolean;
  onSelectAll: () => void;
  onSelectKind: (kind: string) => void;
  onSelectPinned: () => void;
  onOpenSettings: () => void;
  onToggleCapture: () => void;
};

const KINDS = [
  { id: "text", label: "文本", Icon: IconText },
  { id: "link", label: "链接", Icon: IconLink },
  { id: "image", label: "图片", Icon: IconImage },
  { id: "files", label: "文件", Icon: IconFiles },
  { id: "rich", label: "富文本", Icon: IconRich },
];

export default function Sidebar(props: Props) {
  const { view, kind, pinnedOnly, stats, capturing } = props;
  const count = (k: string) => stats?.byKind?.[k] ?? 0;
  const history = view === "history";

  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="brand-mark" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
            <rect x="6" y="4" width="12" height="17" rx="2.6" />
            <path d="M9.5 4.2V3.4a1.4 1.4 0 0 1 1.4-1.4h2.2a1.4 1.4 0 0 1 1.4 1.4v.8" />
            <path d="M9.5 10h5M9.5 13.5h5M9.5 17h3" />
          </svg>
        </div>
        <div className="brand-text">
          <strong>ClipNest</strong>
          <span>{capturing ? "正在记录" : "已暂停"}</span>
        </div>
      </div>

      <nav className="nav">
        <button className={`nav-item ${history && !kind && !pinnedOnly ? "is-active" : ""}`} onClick={props.onSelectAll}>
          <IconAll size={16} />
          <span>全部</span>
          <em>{stats?.total ?? 0}</em>
        </button>
        {KINDS.map(({ id, label, Icon }) => (
          <button
            key={id}
            className={`nav-item ${history && kind === id ? "is-active" : ""}`}
            onClick={() => props.onSelectKind(id)}
          >
            <Icon size={16} />
            <span>{label}</span>
            <em>{count(id)}</em>
          </button>
        ))}
        <div className="nav-divider" />
        <button className={`nav-item ${history && pinnedOnly ? "is-active" : ""}`} onClick={props.onSelectPinned}>
          <IconStar size={16} />
          <span>收藏</span>
          <em>{stats?.pinned ?? 0}</em>
        </button>
      </nav>

      <div className="sidebar-footer">
        <button className="nav-item" onClick={props.onToggleCapture}>
          {capturing ? <IconPause size={16} /> : <IconPlay size={16} />}
          <span>{capturing ? "暂停收集" : "继续收集"}</span>
        </button>
        <button className={`nav-item ${view === "settings" ? "is-active" : ""}`} onClick={props.onOpenSettings}>
          <IconSettings size={16} />
          <span>设置</span>
        </button>
      </div>
    </aside>
  );
}
