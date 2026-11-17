import type { Stats } from "../lib/api";
import {
  IconAll,
  IconFiles,
  IconImage,
  IconLink,
  IconOther,
  IconPause,
  IconPlay,
  IconRich,
  IconSearch,
  IconSettings,
  IconStar,
  IconSync,
  IconText,
} from "./Icons";

type Props = {
  view: "history" | "settings";
  kind: string;
  pinnedOnly: boolean;
  stats: Stats | null;
  capturing: boolean;
  syncActive: boolean;
  onSelectAll: () => void;
  onSelectKind: (kind: string) => void;
  onSelectPinned: () => void;
  onOpenSearch: () => void;
  onOpenSettings: () => void;
  onToggleCapture: () => void;
};

const KINDS = [
  { id: "text", label: "文本", Icon: IconText },
  { id: "link", label: "链接", Icon: IconLink },
  { id: "image", label: "图片", Icon: IconImage },
  { id: "files", label: "文件", Icon: IconFiles },
  { id: "rich", label: "富文本", Icon: IconRich },
  { id: "custom", label: "其它格式", Icon: IconOther },
];

export default function Sidebar(props: Props) {
  const { view, kind, pinnedOnly, stats } = props;
  const history = view === "history";
  const count = (k: string) => stats?.byKind?.[k] ?? 0;

  return (
    <aside className="side">
      <div className="side-scroll">
        <div className="side-group">
          <button className={`side-item ${history && !kind && !pinnedOnly ? "is-active" : ""}`} onClick={props.onSelectAll}>
            <IconAll size={15} />
            <span>全部</span>
            <em>{stats?.total ?? 0}</em>
          </button>
          <button className={`side-item ${history && pinnedOnly ? "is-active" : ""}`} onClick={props.onSelectPinned}>
            <IconStar size={15} />
            <span>收藏</span>
            <em>{stats?.pinned ?? 0}</em>
          </button>
        </div>

        <div className="side-label">类型</div>
        <div className="side-group">
          {KINDS.map(({ id, label, Icon }) => (
            <button
              key={id}
              className={`side-item ${history && kind === id ? "is-active" : ""}`}
              onClick={() => props.onSelectKind(id)}
            >
              <Icon size={15} />
              <span>{label}</span>
              <em>{count(id)}</em>
            </button>
          ))}
        </div>
      </div>

      <div className="side-foot">
        <button className="side-item" onClick={props.onOpenSearch}>
          <IconSearch size={15} />
          <span>搜索</span>
          <kbd>Ctrl F</kbd>
        </button>
        <button className="side-item" onClick={props.onToggleCapture}>
          {props.capturing ? <IconPause size={15} /> : <IconPlay size={15} />}
          <span>{props.capturing ? "暂停收集" : "继续收集"}</span>
        </button>
        <button className={`side-item ${view === "settings" ? "is-active" : ""}`} onClick={props.onOpenSettings}>
          <IconSettings size={15} />
          <span>设置</span>
          {props.syncActive ? <IconSync size={13} className="side-sync" /> : null}
        </button>
      </div>
    </aside>
  );
}
