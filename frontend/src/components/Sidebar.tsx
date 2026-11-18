import { useCallback, useRef, useState } from "react";
import type { Stats } from "../lib/api";
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

export type NavEntry = { id: string; label: string; icon: string };

type Props = {
  view: "history" | "settings" | "snippets" | "memos";
  kind: string;
  pinnedOnly: boolean;
  snippetGroup: string;
  snippetGroups: string[];
  memoFilter: { tag: string; pinned: boolean; archived: boolean };
  memoTags: string[];
  memoCount: number;
  stats: Stats | null;
  snippetCount: number;
  capturing: boolean;
  syncActive: boolean;
  onSelectHistory: (kind: string, pinnedOnly: boolean) => void;
  onSelectSnippets: (group: string) => void;
  onSelectMemos: (filter: { tag?: string; pinned?: boolean; archived?: boolean }) => void;
  nav: NavEntry[];
  onNavChange: (next: NavEntry[]) => void;
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

const NAV_ICONS = ["pin", "snippet", "memo", "star", "folder", "files", "image", "text", "link", "search"];

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
  // 一级菜单的拖动排序与改名状态
  const [dragId, setDragId] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ id: string; label: string; icon: string } | null>(null);
  const navRef = useRef<HTMLDivElement>(null);
  const draggedRef = useRef(false);

  const startDrag = useCallback(
    (e: React.PointerEvent<HTMLButtonElement>, id: string) => {
      const startY = e.clientY;
      draggedRef.current = false;
      const onMove = (ev: PointerEvent) => {
        if (!draggedRef.current && Math.abs(ev.clientY - startY) < 5) return;
        draggedRef.current = true;
        setDragId(id);
        const host = navRef.current;
        if (!host) return;
        const buttons = Array.from(host.querySelectorAll<HTMLElement>("[data-nav-id]"));
        const over = buttons.find((b: HTMLElement) => {
          const r = b.getBoundingClientRect();
          return ev.clientY >= r.top && ev.clientY <= r.bottom;
        });
        const targetId = over?.dataset.navId;
        if (!targetId || targetId === id) return;
        const next = [...props.nav];
        const from = next.findIndex((n) => n.id === id);
        const to = next.findIndex((n) => n.id === targetId);
        if (from < 0 || to < 0) return;
        const [moved] = next.splice(from, 1);
        next.splice(to, 0, moved);
        props.onNavChange(next);
      };
      const onUp = () => {
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        setDragId(null);
        window.setTimeout(() => {
          draggedRef.current = false;
        }, 0);
      };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
    },
    [props],
  );

  const commitEdit = useCallback(() => {
    if (!editing) return;
    const label = editing.label.trim();
    const next = props.nav.map((n) =>
      n.id === editing.id ? { ...n, label: label || n.label, icon: editing.icon } : n,
    );
    props.onNavChange(next);
    setEditing(null);
  }, [editing, props]);
  const { view, kind, pinnedOnly, snippetGroup, stats } = props;
  const history = view === "history";
  const snippets = view === "snippets";
  const memos = view === "memos";
  const count = (k: string) => stats?.byKind?.[k] ?? 0;

  return (
    <aside className="side">
      <div className="side-scroll">
        <div className="side-group" ref={navRef}>
          {props.nav.map((entry) => {
            const active =
              (entry.id === "history" && history) ||
              (entry.id === "snippets" && snippets) ||
              (entry.id === "memos" && memos);
            const badge =
              entry.id === "history"
                ? (stats?.total ?? 0)
                : entry.id === "snippets"
                  ? props.snippetCount
                  : entry.id === "memos"
                    ? props.memoCount
                    : null;
            if (editing?.id === entry.id) {
              return (
                <div className="nav-edit" key={entry.id}>
                  <input
                    className="input nav-edit-input"
                    autoFocus
                    value={editing.label}
                    onChange={(e) => setEditing({ ...editing, label: e.target.value })}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") commitEdit();
                      if (e.key === "Escape") setEditing(null);
                    }}
                    placeholder="菜单名称"
                  />
                  <div className="nav-icon-picker">
                    {NAV_ICONS.map((name) => (
                      <button
                        key={name}
                        className={`nav-icon-choice ${editing.icon === name ? "is-on" : ""}`}
                        title={name}
                        onClick={() => setEditing({ ...editing, icon: name })}
                      >
                        {renderNavIcon(name, 14)}
                      </button>
                    ))}
                  </div>
                  <div className="nav-edit-actions">
                    <button className="link-btn" onClick={() => setEditing(null)}>
                      取消
                    </button>
                    <button className="btn btn-primary btn-sm" onClick={commitEdit}>
                      完成
                    </button>
                  </div>
                </div>
              );
            }
            return (
              <button
                key={entry.id}
                className={`side-item side-primary ${active ? "is-active" : ""} ${dragId === entry.id ? "is-dragging" : ""}`}
                data-nav-id={entry.id}
                onPointerDown={(e) => startDrag(e, entry.id)}
                onClick={(e) => {
                  if (draggedRef.current) {
                    e.preventDefault();
                    draggedRef.current = false;
                    return;
                  }
                  if (entry.id === "history") props.onSelectHistory("", false);
                  else if (entry.id === "snippets") props.onSelectSnippets("");
                  else if (entry.id === "memos") props.onSelectMemos({});
                }}
                onDoubleClick={() => setEditing({ id: entry.id, label: entry.label, icon: entry.icon })}
                title="拖动可排序，双击可改名换图标"
              >
                {renderNavIcon(entry.icon, 15)}
                <span>{entry.label}</span>
                {badge !== null ? <em>{badge}</em> : null}
                <span
                  className="nav-edit-btn"
                  title="改名 / 换图标"
                  onClick={(e) => {
                    e.stopPropagation();
                    setEditing({ id: entry.id, label: entry.label, icon: entry.icon });
                  }}
                >
                  ✎
                </span>
              </button>
            );
          })}
        </div>

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

        {memos ? (
          <>
            <div className="side-label">筛选</div>
            <div className="side-sub">
              <button
                className={`side-item ${!props.memoFilter.pinned && !props.memoFilter.archived && !props.memoFilter.tag ? "is-active" : ""}`}
                onClick={() => props.onSelectMemos({})}
              >
                <IconAll size={15} />
                <span>全部备忘</span>
              </button>
              <button
                className={`side-item ${props.memoFilter.pinned ? "is-active" : ""}`}
                onClick={() => props.onSelectMemos({ pinned: true })}
              >
                <IconStar size={15} />
                <span>置顶</span>
              </button>
              <button
                className={`side-item ${props.memoFilter.archived ? "is-active" : ""}`}
                onClick={() => props.onSelectMemos({ archived: true })}
              >
                <IconFolder size={15} />
                <span>已归档</span>
              </button>
            </div>

            {props.memoTags.length > 0 ? (
              <>
                <div className="side-label">标签</div>
                <div className="side-sub">
                  {props.memoTags.slice(0, 30).map((tag) => (
                    <button
                      key={tag}
                      className={`side-item ${props.memoFilter.tag === tag ? "is-active" : ""}`}
                      onClick={() => props.onSelectMemos({ tag })}
                    >
                      <span>#{tag}</span>
                    </button>
                  ))}
                </div>
              </>
            ) : null}
          </>
        ) : null}

        {snippets ? (
          <>
            <div className="side-label">筛选</div>
            <div className="side-sub">
              <button
                className={`side-item ${snippetGroup === "" ? "is-active" : ""}`}
                onClick={() => props.onSelectSnippets("")}
              >
                <IconAll size={15} />
                <span>全部片段</span>
                <em>{props.snippetCount}</em>
              </button>
              <button
                className={`side-item ${snippetGroup === NO_GROUP ? "is-active" : ""}`}
                onClick={() => props.onSelectSnippets(NO_GROUP)}
              >
                <IconSnippet size={15} />
                <span>未分组</span>
              </button>
            </div>

            {props.snippetGroups.length > 0 ? (
              <>
                <div className="side-label">分组</div>
                <div className="side-sub">
                  {props.snippetGroups.map((g) => (
                    <button
                      key={g}
                      className={`side-item ${snippetGroup === g ? "is-active" : ""}`}
                      onClick={() => props.onSelectSnippets(g)}
                    >
                      <IconSnippet size={15} />
                      <span>{g}</span>
                    </button>
                  ))}
                </div>
              </>
            ) : null}
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
