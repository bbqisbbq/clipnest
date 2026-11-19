import { useCallback, useMemo, useState } from "react";
import Composer from "./Composer";
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
//   底部输入框   → 只负责记新的
//
// 分工：取数/分页/分组在 useEntries，图片缓存在 useEntryImages，
// 单条卡片在 entries/EntryCard；本组件只做搜索框、写新条目和卡片操作的编排。

type Props = {
  filter: EntryFilter;
  day: string;
  onNotify: (message: string) => void;
  onChanged: () => void;
  onOpenDetail: (entry: Entry) => void;
};

export default function EntriesView({ filter, day, onNotify, onChanged, onOpenDetail }: Props) {
  const [query, setQuery] = useState("");
  const askForTrigger = usePrompt();

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [lightbox, setLightbox] = useState<string | null>(null);

  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const baseQuery = useMemo<EntryQuery>(() => {
    const q = { ...(filter.query ?? {}) } as EntryQuery;
    if (day) q.day = day;
    if (query.trim()) q.text = query.trim();
    return q;
  }, [filter, day, query]);

  const { entries, grouped, hasMore, loadingOlder, loadLatest, timelineRef, stickToBottom, onTimelineScroll } =
    useEntries(baseQuery);

  // 需要图片的正文：正在写的草稿 + 已加载的条目
  const imageTexts = useMemo(() => [draft, ...entries.map((e) => e.content)], [draft, entries]);
  const { imgCache } = useEntryImages(imageTexts);

  // ---- 写新的 ------------------------------------------------------------

  const save = useCallback(
    async (content?: string) => {
      // content 由 Composer 从编辑器里直接读（state 可能落后一个 keystroke）
      const text = (content ?? draft).trim();
      if (!text) {
        setError("写点什么再保存");
        return;
      }
      setBusy(true);
      setError("");
      try {
        await ClipService.SaveEntry({ content: text, source: "manual", kind: "text" } as never);
        onNotify("已记下");
        setDraft("");
        stickToBottom.current = true;
        await loadLatest();
        onChanged();
      } catch (err) {
        setError(String(err));
      } finally {
        setBusy(false);
      }
    },
    [draft, loadLatest, onChanged, onNotify, stickToBottom],
  );

  // 粘贴/拖入图片：存成附件，返回正文要写的 token，由编辑器自己插进光标处
  const pasteImage = useCallback(async (dataUrl: string) => {
    try {
      return await ClipService.SaveImage(dataUrl);
    } catch (err) {
      setError(String(err));
      return null;
    }
  }, []);

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

      <Composer
        value={draft}
        onChange={setDraft}
        onSave={(content) => void save(content)}
        onImageFile={pasteImage}
        busy={busy}
        error={error}
      />

      {lightbox ? <Lightbox src={lightbox} onClose={() => setLightbox(null)} /> : null}
    </div>
  );
}
