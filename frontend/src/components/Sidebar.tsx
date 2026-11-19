import type { EntryFilter } from "../lib/api";
import Calendar from "./Calendar";
import {
  IconAll,
  IconFiles,
  IconFolder,
  IconImage,
  IconLink,
  IconPause,
  IconPin,
  IconPlay,
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
  view: "settings" | "entries";
  capturing: boolean;
  syncActive: boolean;
  filterProps: FilterProps;
  onOpenSettings: () => void;
  onToggleCapture: () => void;
};

function renderNavIcon(name: string, size: number) {
  switch (name) {
    case "all": return <IconAll size={size} />;
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
  const { view } = props;

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

      </div>

      <div className="side-foot">
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
