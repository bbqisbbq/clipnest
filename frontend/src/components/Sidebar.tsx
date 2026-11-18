import type { EntryFilter, Stats } from "../lib/api";
import Calendar from "./Calendar";
import {
  IconAll,
  IconFiles,
  IconFolder,
  IconImage,
  IconLink,
  IconOther,
  IconPause,
  IconPin,
  IconPlay,
  IconRich,
  IconSearch,
  IconSettings,
  IconSnippet,
  IconMemo,
  IconStar,
  IconSync,
  IconText,
} from "./Icons";

// 两级导航：一级是「剪贴板 / 片段」两个功能区，二级是各自的筛选条件。
// 后面加新功能时只需要在 PRIMARY 里加一项，并在下面给它一段二级列表。

// 内容筛选器（保存下来的 EntryQuery）
type FilterProps = {
  filters: EntryFilter[];
  activeFilterId: string;
  onSelectFilter: (f: EntryFilter) => void;
  selectedDay: string;
  onPickDay: (day: string) => void;
  onEditFilter: (f: EntryFilter) => void;
  entryStats: { manual: number; clipboard: number; snippets: number; total: number; pinned: number; archived: number; today: number } | null;
};

type Props = {
  view: "history" | "settings" | "snippets" | "memos" | "entries";
  kind: string;
  pinnedOnly: boolean;
  memoFilter: { tag: string; pinned: boolean; archived: boolean };
  memoTags: string[];
  memoCount: number;
  stats: Stats | null;
  capturing: boolean;
  syncActive: boolean;
  onSelectHistory: (kind: string, pinnedOnly: boolean) => void;
  onSelectSnippets: (group: string) => void;
  onSelectMemos: (filter: { tag?: string; pinned?: boolean; archived?: boolean }) => void;
  filterProps: FilterProps;
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

// 未分组的片段用这个哨兵值筛选（空串表示「全部」）
export const NO_GROUP = "__none__";

function renderNavIcon(name: string, size: number) {
  switch (name) {
    case "snippet": return <IconSnippet size={size} />;
    case "memo": return <IconMemo size={size} />;
    case "star": return <IconStar size={size} />;
    case "folder": return <IconFolder size={size} />;
    case "files": return <IconFiles size={size} />;
    case "image": return <IconImage size={size} />;
    case "text": return <IconText size={size} />;
    case "link": return <IconLink size={size} />;
    case "search": return <IconSearch size={size} />;
    default: return <IconPin size={size} />;
  }
}

export default function Sidebar(props: Props) {
  const { view, kind, pinnedOnly, stats } = props;
  const history = view === "history";
  const count = (k: string) => stats?.byKind?.[k] ?? 0;

  return (
    <aside className="side">
      <div className="side-scroll">
        {/* 统一内容的入口：筛选器 + 日历 */}
        <div className="side-label">内容</div>
        <div className="side-sub">
          {props.filterProps.filters.map((f) => {
            const n =
              f.id === "mine"
                ? props.filterProps.entryStats?.manual
                : f.id === "clipboard"
                  ? props.filterProps.entryStats?.clipboard
                  : f.id === "snippets"
                    ? props.filterProps.entryStats?.snippets
                    : f.id === "all"
                      ? props.filterProps.entryStats?.total
                      : f.id === "pinned"
                        ? props.filterProps.entryStats?.pinned
                        : f.id === "archived"
                          ? props.filterProps.entryStats?.archived
                          : null;
            return (
              <button
                key={f.id}
                className={`side-item ${props.filterProps.activeFilterId === f.id ? "is-active" : ""}`}
                onClick={() => props.filterProps.onSelectFilter(f)}
                onDoubleClick={() => props.filterProps.onEditFilter(f)}
                title="双击可改名 / 换图标"
              >
                {renderNavIcon(f.icon || "memo", 14)}
                <span>{f.name}</span>
                {n !== null && n !== undefined ? <em>{n}</em> : null}
              </button>
            );
          })}
        </div>

        <div className="side-label">日历</div>
        <Calendar
          selectedDay={props.filterProps.selectedDay}
          onPickDay={props.filterProps.onPickDay}
        />

        {history ? (
          <>
            <div className="side-label">筛选</div>
            <div className="side-sub">
              <button
                className={`side-item ${!kind && !pinnedOnly ? "is-active" : ""}`}
                onClick={() => props.onSelectHistory("", false)}
              >
                <IconAll size={15} />
                <span>全部</span>
                <em>{stats?.total ?? 0}</em>
              </button>
              <button
                className={`side-item ${pinnedOnly ? "is-active" : ""}`}
                onClick={() => props.onSelectHistory("", true)}
              >
                <IconStar size={15} />
                <span>收藏</span>
                <em>{stats?.pinned ?? 0}</em>
              </button>
            </div>

            <div className="side-label">类型</div>
            <div className="side-sub">
              {KINDS.map(({ id, label, Icon }) => (
                <button
                  key={id}
                  className={`side-item ${kind === id ? "is-active" : ""}`}
                  onClick={() => props.onSelectHistory(id, false)}
                >
                  <Icon size={15} />
                  <span>{label}</span>
                  <em>{count(id)}</em>
                </button>
              ))}
            </div>
          </>
        ) : null}
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
