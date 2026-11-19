import { useCallback, useEffect, useMemo, useState } from "react";
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
//   点「编辑」   → 同上（按钮留着，双击是快捷方式）
//   点图片      → 大图
//
// 底部输入框不在这里：它是全局常驻组件（ComposerDock，挂在 App 上），
// 因为待办页、设置页也要在同一个位置有它，而且草稿不能被卸载清空。
// 本组件只做搜索框、卡片操作与时间线。

type Props = {
  filter: EntryFilter;
  day: string;
  /** 全局输入框里的草稿：正文里的图片引用要提前焐热，保存的瞬间才不会闪 */
  draftText: string;
  /** 输入框存下一条后 +1：重载列表并贴到底部 */
  reloadSignal: number;
  onNotify: (message: string) => void;
  onChanged: () => void;
  onOpenDetail: (entry: Entry) => void;
};

export default function EntriesView({
  filter,
  day,
  draftText,
  reloadSignal,
  onNotify,
  onChanged,
  onOpenDetail,
}: Props) {
  const [query, setQuery] = useState("");
  const askForTrigger = usePrompt();

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [lightbox, setLightbox] = useState<string | null>(null);

  const baseQuery = useMemo<EntryQuery>(() => {
    const q = { ...(filter.query ?? {}) } as EntryQuery;
    if (day) q.day = day;
    if (query.trim()) q.text = query.trim();
    return q;
  }, [filter, day, query]);

  const { entries, grouped, hasMore, loadingOlder, loadLatest, timelineRef, stickToBottom, onTimelineScroll } =
    useEntries(baseQuery);

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

  // ---- 卡片操作 ----------------------------------------------------------

  const patchEntry = useCallback(
    async (e: Entry, patch: Partial<Entry>) => {
      try {
        await ClipService.SaveEntry({ ...e, ...patch } as never);
        await loadLatest();
        onChanged();
      } catch (err) {
        onNotify(`操作失败：${String(err)}`);
      }
    },
    [loadLatest, onChanged, onNotify],
  );

  const remove = useCallback(
    async (e: Entry) => {
      try {
        await ClipService.DeleteEntry(e.id);
        await loadLatest();
        onChanged();
        onNotify("已删除");
      } catch (err) {
        onNotify(`删除失败：${String(err)}`);
      }
    },
    [loadLatest, onChanged, onNotify],
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
        await ClipService.PromoteToSnippet(e.id, trigger.trim());
        await loadLatest();
        onChanged();
        onNotify(`已设为片段：${trigger.trim()}`);
      } catch (err) {
        onNotify(`失败：${String(err)}`);
      }
    },
    [loadLatest, onChanged, onNotify, askForTrigger],
  );

  const dropSnippet = useCallback(
    async (e: Entry) => {
      try {
        await ClipService.DemoteFromSnippet(e.id);
        await loadLatest();
        onChanged();
        onNotify("已取消片段");
      } catch (err) {
        onNotify(`失败：${String(err)}`);
      }
    },
    [loadLatest, onChanged, onNotify],
  );

  const toggleClipboard = useCallback(
    async (e: Entry) => {
      const has = (e.actions ?? []).includes("clipboard");
      const next = has
        ? (e.actions ?? []).filter((a) => a !== "clipboard")
        : [...(e.actions ?? []), "clipboard"];
      await patchEntry(e, { actions: next });
      onNotify(has ? "已从粘贴列表移除" : "已加入粘贴列表");
    },
    [patchEntry, onNotify],
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
            className="input"
            placeholder="搜索内容（支持中文子串）…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <span className="memo-count">
            {filter.name}
            {day ? ` · ${day}` : ""} · {entries.length}
            {hasMore ? "+" : ""} 条
          </span>
        </header>

        <div className="memo-timeline" ref={timelineRef} onScroll={onTimelineScroll}>
          {hasMore ? (
            <div className="memo-more">{loadingOlder ? "正在加载更早的…" : "上滑加载更早"}</div>
          ) : null}

          {grouped.length === 0 ? (
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
                    onTogglePin={() => void patchEntry(e, { pinned: !e.pinned })}
                    onToggleArchive={() => void patchEntry(e, { archived: !e.archived })}
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
