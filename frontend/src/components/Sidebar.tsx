import { useState } from "react";
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
  IconTodo,
} from "./Icons";

// 两级导航：一级是「剪贴板 / 片段」两个功能区，二级是各自的筛选条件。
// 后面加新功能时只需要在 PRIMARY 里加一项，并在下面给它一段二级列表。

// 内容筛选器（保存下来的 EntryQuery）
type FilterProps = {
  filters: EntryFilter[];
  activeFilterId: string;
  /** 启动时自动打开的筛选器 id（""=没设，落到第一个） */
  defaultFilterId: string;
  onSelectFilter: (f: EntryFilter) => void;
  selectedDay: string;
  onPickDay: (day: string) => void;
  onEditFilter: (f: EntryFilter) => void;
  onSetDefaultFilter: (f: EntryFilter) => void;
  entryStats: { manual: number; clipboard: number; snippets: number; total: number; pinned: number; archived: number; today: number; todoOpen: number; todoDone: number } | null;
};

type Props = {
  view: "settings" | "entries" | "todo";
  capturing: boolean;
  syncActive: boolean;
  filterProps: FilterProps;
  /** 点「设置」：在设置页时是"关掉回原处"，否则是"打开设置"（由 App 判断） */
  onOpenSettings: () => void;
  onToggleCapture: () => void;
  /** 待办收集页是独立入口（不是筛选器）：它的数据是清单项，不是条目 */
  onOpenTodos: () => void;
  onNewFilter: () => void;
  onDuplicateFilter: (f: EntryFilter) => void;
  onDeleteFilter: (f: EntryFilter) => void;
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
  // 筛选器的 ⋯ 菜单：一次只开一个
  const [menuFor, setMenuFor] = useState<string | null>(null);
  // 日历的圆点跟着当前筛选器走，所以要把它的查询传下去
  const activeQuery =
    props.filterProps.filters.find((f) => f.id === props.filterProps.activeFilterId)?.query ?? ({} as EntryFilter["query"]);

  return (
    <aside className="side">
      <div className="side-scroll">
        {/* 统一内容的入口：筛选器 + 日历 */}
        <div className="side-label">内容</div>
        <div className="side-sub">
          {/* 待办在最上面：它是"今天要做什么"的入口，比翻历史更常点 */}
          <button
            className={`side-item ${view === "todo" ? "is-active" : ""}`}
            onClick={props.onOpenTodos}
            title="收集所有条目里的 - [ ] 任务"
          >
            <IconTodo size={14} />
            <span>待办</span>
            {props.filterProps.entryStats?.todoOpen ? (
              <em className="side-todo-count">{props.filterProps.entryStats.todoOpen}</em>
            ) : null}
          </button>
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
              // 行本身不是 <button>：里面还要放 ⋯ 按钮（button 不能嵌套 button）
              <div className="side-item-wrap" key={f.id}>
                <div
                  className={`side-item ${props.filterProps.activeFilterId === f.id ? "is-active" : ""}`}
                  role="button"
                  tabIndex={0}
                  onClick={() => props.filterProps.onSelectFilter(f)}
                  onDoubleClick={() => props.filterProps.onEditFilter(f)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      props.filterProps.onSelectFilter(f);
                    }
                  }}
                  title="双击可改名 / 编辑规则"
                >
                  {renderNavIcon(f.icon || "memo", 14)}
                  <span>{f.name}</span>
                  {props.filterProps.defaultFilterId === f.id ? (
                    <em className="side-default" title="启动时自动打开这个筛选器">
                      默认
                    </em>
                  ) : null}
                  {n !== null && n !== undefined ? <em>{n}</em> : null}
                  <button
                    className={`side-more ${menuFor === f.id ? "is-open" : ""}`}
                    title="更多"
                    onClick={(e) => {
                      e.stopPropagation();
                      setMenuFor(menuFor === f.id ? null : f.id);
                    }}
                  >
                    ⋯
                  </button>
                </div>

                {menuFor === f.id ? (
                  <div className="side-menu" onMouseLeave={() => setMenuFor(null)}>
                    <button
                      onClick={() => {
                        setMenuFor(null);
                        props.filterProps.onEditFilter(f);
                      }}
                    >
                      编辑（改名 / 规则）
                    </button>
                    <button
                      onClick={() => {
                        setMenuFor(null);
                        props.onDuplicateFilter(f);
                      }}
                    >
                      复制为新筛选器
                    </button>
                    <button
                      onClick={() => {
                        setMenuFor(null);
                        props.filterProps.onSetDefaultFilter(f);
                      }}
                    >
                      {props.filterProps.defaultFilterId === f.id ? "取消默认打开" : "设为默认打开"}
                    </button>
                    {f.deletable ? (
                      <button
                        className="is-danger"
                        onClick={() => {
                          setMenuFor(null);
                          props.onDeleteFilter(f);
                        }}
                      >
                        删除
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </div>
            );
          })}

          <button className="side-add" onClick={props.onNewFilter} title="用规则拼一个新筛选器">
            ＋ 新建筛选器
          </button>
        </div>

        <div className="side-label">日历</div>
        <Calendar
          selectedDay={props.filterProps.selectedDay}
          onPickDay={props.filterProps.onPickDay}
          query={activeQuery}
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
