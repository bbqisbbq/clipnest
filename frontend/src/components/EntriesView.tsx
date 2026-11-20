import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Lightbox from "./Lightbox";
import EntryCard from "./entries/EntryCard";
import { ClipService, type Entry, type EntryFilter, type EntryQuery } from "../lib/api";
import { useEntries } from "../hooks/useEntries";
import { usePrompt } from "./Dialog";
import { useEntryImages } from "../hooks/useEntryImages";

// 统一内容视图：剪贴板采集、片段、手写备忘都在这里，靠属性区分。
//
// 交互约定：
//   点卡片      → 展开/收起（看全文）
//   双击卡片     → 右侧打开条目详情（onOpenDetail，由 App 切视图）
//   键盘        → Tab 进卡片，Enter 展开，Ctrl+Enter 进详情，Delete 删除；Ctrl+F 到搜索框
//   点图片      → 大图
//
// 底部输入框不在这里：它是全局常驻组件（ComposerDock，挂在 App 上），
// 因为待办页、设置页也要在同一个位置有它，而且草稿不能被卸载清空。
// 本组件只做搜索框、卡片操作与时间线。

const SEARCH_DEBOUNCE_MS = 250;

type Props = {
  filter: EntryFilter;
  day: string;
  /** 全局输入框里的草稿：正文里的图片引用要提前焐热，保存的瞬间才不会闪 */
  draftText: string;
  /** 输入框存下一条后 +1：重载列表并贴到底部 */
  reloadSignal: number;
  /** 后台采集/同步进来新条目时 +1：把新条目并进列表，但不重载、不跳位 */
  liveSignal: number;
  /** 别处删掉了一条（快速粘贴窗里删的 / 同步拉到对端的删除） */
  removed?: { id: string; tick: number } | null;
  onNotify: (message: string) => void;
  onChanged: () => void;
  onOpenDetail: (entry: Entry) => void;
};

export default function EntriesView({
  filter,
  day,
  draftText,
  reloadSignal,
  liveSignal,
  removed,
  onNotify,
  onChanged,
  onOpenDetail,
}: Props) {
  const [query, setQuery] = useState("");
  // 搜索框的原始输入与真正拿去查库的关键词分开：以前每敲一个字符就查一次库，
  // 顺带把整个时间线（几十上百张卡片）重渲染一遍。
  const [debounced, setDebounced] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);
  const askForTrigger = usePrompt();

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [lightbox, setLightbox] = useState<string | null>(null);

  useEffect(() => {
    const t = window.setTimeout(() => setDebounced(query.trim()), SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [query]);

  const baseQuery = useMemo<EntryQuery>(() => {
    const q = { ...(filter.query ?? {}) } as EntryQuery;
    if (day) q.day = day;
    if (debounced) q.text = debounced;
    return q;
  }, [filter, day, debounced]);

  const {
    entries,
    grouped,
    hasMore,
    loading,
    loadingOlder,
    error,
    loadLatest,
    removeLocal,
    patchLocal,
    timelineRef,
    stickToBottom,
    onTimelineScroll,
  } = useEntries(baseQuery, onNotify);

  // 需要图片的正文：正在写的草稿 + 已加载的条目
  const imageTexts = useMemo(() => [draftText, ...entries.map((e) => e.content)], [draftText, entries]);
  const { imgCache } = useEntryImages(imageTexts);

  // 全局输入框存下一条 → 这里重载一页并贴底。
  // 信号在 App 里累加，所以从设置页回来时也会带着最新的那一次（首次挂载读 0 不触发）。
  useEffect(() => {
    if (reloadSignal === 0) return;
    stickToBottom.current = true;
    void loadLatest();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reloadSignal]);

  // 后台来的新条目（复制了东西 / 同步拉到别人写的）：并进列表，滚动位置不动。
  useEffect(() => {
    if (liveSignal === 0) return;
    void loadLatest({ stick: false });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [liveSignal]);

  // 别处删掉了一条：本地摘掉，不等整页重载
  useEffect(() => {
    if (removed?.id) removeLocal(removed.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [removed?.tick]);

  // Ctrl+F：把焦点交给搜索框（README 里一直写着这个快捷键，代码里却没有）
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "f") {
        ev.preventDefault();
        searchRef.current?.focus();
        searchRef.current?.select();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // ---- 卡片操作 ----------------------------------------------------------

  // 置顶/归档只改那一列。以前是 `SaveEntry({...entry, ...patch})`：拿一份列表里的
  // 旧快照整行回写，会把别处刚改的字段（含同步拉回来的对端修改）一起盖回去。
  const togglePin = useCallback(
    async (e: Entry) => {
      try {
        patchLocal(await ClipService.SetEntryPinned(e.id, !e.pinned));
        onChanged();
      } catch (err) {
        onNotify(`操作失败：${String(err)}`);
      }
    },
    [patchLocal, onChanged, onNotify],
  );

  const toggleArchive = useCallback(
    async (e: Entry) => {
      try {
        patchLocal(await ClipService.SetEntryArchived(e.id, !e.archived));
        onChanged();
      } catch (err) {
        onNotify(`操作失败：${String(err)}`);
      }
    },
    [patchLocal, onChanged, onNotify],
  );

  const remove = useCallback(
    async (e: Entry) => {
      try {
        await ClipService.DeleteEntry(e.id);
        removeLocal(e.id);
        onChanged();
        onNotify("已删除");
      } catch (err) {
        onNotify(`删除失败：${String(err)}`);
      }
    },
    [removeLocal, onChanged, onNotify],
  );

  const makeSnippet = useCallback(
    async (e: Entry) => {
      // 用主题化输入框而不是 window.prompt：系统弹窗和主题完全不搭
      const trigger = await askForTrigger({
        message: "给这条设一个缩写（在任意输入框打它就会展开）",
        placeholder: "例如 addr-",
        initialValue: e.triggerKey ?? "",
        confirmLabel: "设为片段",
      });
      if (!trigger) return;
      try {
        const next = await ClipService.PromoteToSnippet(e.id, trigger.trim());
        patchLocal(next);
        onChanged();
        onNotify(`已设为片段：${trigger.trim()}`);
      } catch (err) {
        onNotify(`失败：${String(err)}`);
      }
    },
    [patchLocal, onChanged, onNotify, askForTrigger],
  );

  const dropSnippet = useCallback(
    async (e: Entry) => {
      try {
        patchLocal(await ClipService.DemoteFromSnippet(e.id));
        onChanged();
        onNotify("已取消片段");
      } catch (err) {
        onNotify(`失败：${String(err)}`);
      }
    },
    [patchLocal, onChanged, onNotify],
  );

  // 能力开关没有单独的后端接口，所以先取一份最新的条目再改它 ——
  // 而不是把列表里那份（可能已经过期的）整行写回去。
  const toggleClipboard = useCallback(
    async (e: Entry) => {
      try {
        const fresh = await ClipService.GetEntry(e.id);
        const has = (fresh.actions ?? []).includes("clipboard");
        const next = has
          ? (fresh.actions ?? []).filter((a) => a !== "clipboard")
          : [...(fresh.actions ?? []), "clipboard"];
        patchLocal(await ClipService.SaveEntry({ ...fresh, actions: next }));
        onChanged();
        onNotify(has ? "已从粘贴列表移除" : "已加入粘贴列表");
      } catch (err) {
        onNotify(`操作失败：${String(err)}`);
      }
    },
    [patchLocal, onChanged, onNotify],
  );

  const toggleExpand = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  return (
    <div className="entries">
      <div className="entries-main">
        <header className="memo-head">
          <input
            ref={searchRef}
            className="input"
            placeholder="搜索内容（支持中文子串）…  Ctrl+F"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape" && query) {
                e.stopPropagation();
                setQuery("");
              }
            }}
          />
          <span className="memo-count">
            {filter.name}
            {day ? ` · ${day}` : ""} · {entries.length}
            {hasMore ? "+" : ""} 条
          </span>
        </header>

        <div className="memo-timeline" ref={timelineRef} onScroll={onTimelineScroll}>
          {error ? <p className="snip-empty is-error">{error}</p> : null}
          {hasMore ? (
            <div className="memo-more">{loadingOlder ? "正在加载更早的…" : "上滑加载更早"}</div>
          ) : null}

          {loading && entries.length === 0 ? (
            <p className="snip-empty">正在加载…</p>
          ) : grouped.length === 0 ? (
            <p className="snip-empty">
              {day ? `${day} ${filter.id === "all" ? "" : `· ${filter.name} `}还没有内容。` : `${filter.name}里还没有内容。`}
              换个筛选器、点日历换一天，或在下面写一句。
            </p>
          ) : (
            grouped.map((g) => (
              <section key={g.key} className="memo-day">
                <div className="memo-day-label">{g.day}</div>
                {g.items.map((e) => (
                  <EntryCard
                    key={e.id}
                    entry={e}
                    expanded={expanded.has(e.id)}
                    imgCache={imgCache}
                    onToggleExpand={() => toggleExpand(e.id)}
                    onEdit={() => onOpenDetail(e)}
                    onImage={(src) => setLightbox(src)}
                    onTogglePin={() => void togglePin(e)}
                    onToggleArchive={() => void toggleArchive(e)}
                    onToggleClipboard={() => void toggleClipboard(e)}
                    onSnippet={() => void (e.triggerKey ? dropSnippet(e) : makeSnippet(e))}
                    onDelete={() => void remove(e)}
                  />
                ))}
              </section>
            ))
          )}
        </div>
      </div>

      {lightbox ? <Lightbox src={lightbox} onClose={() => setLightbox(null)} /> : null}
    </div>
  );
}
