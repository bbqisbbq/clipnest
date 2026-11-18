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

export default function Sidebar(props: Props) {
  const { view, kind, pinnedOnly, snippetGroup, stats } = props;
  const history = view === "history";
  const snippets = view === "snippets";
  const memos = view === "memos";
  const count = (k: string) => stats?.byKind?.[k] ?? 0;

  return (
    <aside className="side">
      <div className="side-scroll">
        <div className="side-group">
          <button
            className={`side-item side-primary ${history ? "is-active" : ""}`}
            onClick={() => props.onSelectHistory("", false)}
          >
            <IconPin size={15} />
            <span>剪贴板</span>
            <em>{stats?.total ?? 0}</em>
          </button>
          <button
            className={`side-item side-primary ${snippets ? "is-active" : ""}`}
            onClick={() => props.onSelectSnippets("")}
          >
            <IconSnippet size={15} />
            <span>片段</span>
            <em>{props.snippetCount}</em>
          </button>
          <button
            className={`side-item side-primary ${memos ? "is-active" : ""}`}
            onClick={() => props.onSelectMemos({})}
          >
            <IconMemo size={15} />
            <span>备忘</span>
            <em>{props.memoCount}</em>
          </button>
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
