import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { marked } from "marked";
import Composer from "./Composer";
import SnippetFields from "./SnippetFields";
import { ClipService, type Entry, type EntryFilter, type EntryQuery } from "../lib/api";
import { collectImageHashes, resolveImages } from "../lib/images";

// 统一内容视图：剪贴板采集、片段、手写备忘都在这里，靠属性区分。
// 上面是时间线（按创建时间正序，最新的贴着底部输入框），下面是 Composer。

type Props = {
  filter: EntryFilter;
  day: string;
  onNotify: (message: string) => void;
  onChanged: () => void;
};

const PAGE_SIZE = 40;

function dayKey(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

function dayLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date();
  const same = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  const yesterday = new Date(today.getTime() - 86400000);
  if (same(d, today)) return "今天";
  if (same(d, yesterday)) return "昨天";
  return d.getFullYear() === today.getFullYear()
    ? `${d.getMonth() + 1} 月 ${d.getDate()} 日`
    : `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

function timeLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function sourceLabel(e: Entry): string {
  if (e.source === "clipboard") return e.sourceApp ? `来自 ${e.sourceApp}` : "来自剪贴板";
  if (e.source === "import") return "导入";
  return "";
}

const ACTION_LABEL: Record<string, string> = {
  clipboard: "剪贴板",
  snippet: "片段",
};

export default function EntriesView({ filter, day, onNotify, onChanged }: Props) {
  const [query, setQuery] = useState("");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [imgCache, setImgCache] = useState<Record<string, string>>({});

  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState<Entry | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const timelineRef = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);

  const baseQuery = useMemo<EntryQuery>(() => {
    const q = { ...(filter.query ?? {}) } as EntryQuery;
    if (day) q.day = day;
    if (query.trim()) q.text = query.trim();
    return q;
  }, [filter, day, query]);

  const loadLatest = useCallback(async () => {
    try {
      const list =
        (await ClipService.GetEntries({ ...baseQuery, limit: PAGE_SIZE, tail: true } as never)) ?? [];
      setEntries(list);
      setHasMore(list.length >= PAGE_SIZE);
      stickToBottom.current = true;
    } catch (err) {
      console.error(err);
    }
  }, [baseQuery]);

  const loadOlder = useCallback(async () => {
    if (loadingOlder || !hasMore || entries.length === 0) return;
    const cursor = entries[0].createdAt;
    if (!cursor) return;
    setLoadingOlder(true);
    const host = timelineRef.current;
    const prevHeight = host?.scrollHeight ?? 0;
    try {
      const older =
        (await ClipService.GetEntries({ ...baseQuery, limit: PAGE_SIZE, before: cursor } as never)) ?? [];
      if (older.length === 0) {
        setHasMore(false);
      } else {
        setEntries((prev) => [...older, ...prev]);
        setHasMore(older.length >= PAGE_SIZE);
        requestAnimationFrame(() => {
          if (host) host.scrollTop = host.scrollHeight - prevHeight;
        });
      }
    } catch (err) {
      console.error(err);
    } finally {
      setLoadingOlder(false);
    }
  }, [loadingOlder, hasMore, entries, baseQuery]);

  useEffect(() => {
    void loadLatest();
  }, [loadLatest]);

  const grouped = useMemo(() => {
    const out: Array<{ day: string; key: string; items: Entry[] }> = [];
    for (const e of entries) {
      const key = dayKey(e.createdAt);
      const last = out[out.length - 1];
      if (last && last.key === key) last.items.push(e);
      else out.push({ day: dayLabel(e.createdAt), key, items: [e] });
    }
    return out;
  }, [entries]);

  useEffect(() => {
    if (!stickToBottom.current) return;
    const host = timelineRef.current;
    if (host) host.scrollTop = host.scrollHeight;
  }, [grouped]);

  // 按需加载图片
  useEffect(() => {
    const missing = collectImageHashes([draft, ...entries.map((e) => e.content)]).filter(
      (h) => !imgCache[h],
    );
    if (missing.length === 0) return;
    let alive = true;
    void (async () => {
      const next: Record<string, string> = {};
      for (const hash of missing) {
        try {
          next[hash] = await ClipService.GetImage(hash);
        } catch {
          /* 附件已清理 */
        }
      }
      if (alive && Object.keys(next).length) setImgCache((p) => ({ ...p, ...next }));
    })();
    return () => {
      alive = false;
    };
  }, [draft, entries, imgCache]);

  // ---- 写与改 ------------------------------------------------------------

  const save = useCallback(async () => {
    if (!draft.trim()) {
      setError("写点什么再保存");
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (editing) {
        await ClipService.SaveEntry({ ...editing, content: draft } as never);
        onNotify("已更新");
      } else {
        await ClipService.SaveEntry({ content: draft, source: "manual", kind: "text" } as never);
        onNotify("已记下");
      }
      setDraft("");
      setEditing(null);
      stickToBottom.current = true;
      await loadLatest();
      onChanged();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [draft, editing, loadLatest, onChanged, onNotify]);

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
        if (editing?.id === e.id) {
          setEditing(null);
          setDraft("");
        }
        await loadLatest();
        onChanged();
        onNotify("已删除");
      } catch (err) {
        onNotify(`删除失败：${String(err)}`);
      }
    },
    [editing, loadLatest, onChanged, onNotify],
  );

  const makeSnippet = useCallback(
    async (e: Entry) => {
      const trigger = window.prompt("给这条设一个缩写（例如 addr-）：", e.triggerKey ?? "");
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
    [loadLatest, onChanged, onNotify],
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

  const pasteImage = useCallback(
    async (dataUrl: string) => {
      try {
        const token = await ClipService.SaveImage(dataUrl);
        setDraft((prev) => prev + (prev && !prev.endsWith("\n") ? "\n" : "") + `![](${token})\n`);
      } catch (err) {
        setError(String(err));
      }
    },
    [],
  );

  const sourceHint =
    editing && editing.source === "clipboard"
      ? `采集自 ${editing.sourceApp || "剪贴板"} · 复制 ${editing.copyCount || 1} 次` +
        ((editing.seenCount ?? 0) > 1 ? ` · 出现 ${editing.seenCount} 次` : "")
      : undefined;

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

        <div
          className="memo-timeline"
          ref={timelineRef}
          onScroll={(e) => {
            const el = e.currentTarget;
            stickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
            if (el.scrollTop < 60) void loadOlder();
          }}
        >
          {hasMore ? (
            <div className="memo-more">{loadingOlder ? "正在加载更早的…" : "上滑加载更早"}</div>
          ) : null}

          {grouped.length === 0 ? (
            <p className="snip-empty">
              {day ? `${day} 还没有内容。` : `${filter.name}里还没有内容。`}
              在下面写一句，或点日历换一天 / 换个筛选器。
            </p>
          ) : (
            grouped.map((g) => (
              <section key={g.key} className="memo-day">
                <div className="memo-day-label">{g.day}</div>
                {g.items.map((e) => (
                  <EntryCard
                    key={e.id}
                    entry={e}
                    active={editing?.id === e.id}
                    imgCache={imgCache}
                    onOpen={() => {
                      setEditing(e);
                      setDraft(e.content);
                    }}
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

      {editing && editing.triggerKey ? (
        <SnippetFields
          entry={editing}
          onSaved={() => {
            void loadLatest();
            onChanged();
          }}
          onNotify={onNotify}
        />
      ) : null}

      <Composer
        value={draft}
        onChange={setDraft}
        onSave={() => void save()}
        onImagePaste={(u) => void pasteImage(u)}
        editing={!!editing}
        onReset={() => {
          setDraft("");
          setEditing(null);
          setError("");
        }}
        busy={busy}
        error={error}
        imageCache={imgCache}
        sourceHint={sourceHint}
      />
    </div>
  );
}

// ---- 一条内容的卡片（纯展示 + 回调，不含业务） ---------------------------

type CardProps = {
  entry: Entry;
  active: boolean;
  imgCache: Record<string, string>;
  onOpen: () => void;
  onTogglePin: () => void;
  onToggleArchive: () => void;
  onToggleClipboard: () => void;
  onSnippet: () => void;
  onDelete: () => void;
};

function EntryCard(props: CardProps) {
  const { entry: e, active, imgCache } = props;
  const collected = e.source === "clipboard";
  const inClipboard = (e.actions ?? []).includes("clipboard");

  return (
    <article
      className={`memo-card entry-card ${collected ? "is-collected" : ""} ${active ? "is-active" : ""} ${
        e.pinned ? "is-pinned" : ""
      }`}
      onClick={props.onOpen}
    >
      <div
        className={e.kind === "image" ? "entry-image" : "memo-body"}
        dangerouslySetInnerHTML={{
          __html: resolveImages(marked.parse(e.content, { async: false }) as string, imgCache),
        }}
      />
      <footer className="memo-meta">
        <span>{timeLabel(e.createdAt)}</span>
        {(e.actions ?? []).map((a) => (
          <span key={a} className="entry-autotag">
            {ACTION_LABEL[a] ?? a}
          </span>
        ))}
        {(e.tags ?? []).map((t) => (
          <span key={t} className="memo-tag">
            #{t}
          </span>
        ))}
        {e.triggerKey ? <span className="entry-trigger">{e.triggerKey}</span> : null}
        {collected ? <span className="memo-src">{sourceLabel(e)}</span> : null}
        <span className="memo-actions">
          <button
            className={e.pinned ? "link-btn is-on" : "link-btn"}
            onClick={(ev) => {
              ev.stopPropagation();
              props.onTogglePin();
            }}
          >
            {e.pinned ? "取消置顶" : "置顶"}
          </button>
          <button
            className="link-btn"
            onClick={(ev) => {
              ev.stopPropagation();
              props.onSnippet();
            }}
          >
            {e.triggerKey ? "取消片段" : "设为片段"}
          </button>
          <button
            className={inClipboard ? "link-btn is-on" : "link-btn"}
            onClick={(ev) => {
              ev.stopPropagation();
              props.onToggleClipboard();
            }}
            title="有这项能力才会出现在快速粘贴列表里"
          >
            {inClipboard ? "移出粘贴列表" : "加入粘贴列表"}
          </button>
          <button
            className="link-btn"
            onClick={(ev) => {
              ev.stopPropagation();
              props.onToggleArchive();
            }}
          >
            {e.archived ? "恢复" : "归档"}
          </button>
          <button
            className="link-btn is-danger"
            onClick={(ev) => {
              ev.stopPropagation();
              props.onDelete();
            }}
          >
            删除
          </button>
        </span>
      </footer>
    </article>
  );
}
