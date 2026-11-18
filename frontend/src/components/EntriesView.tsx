import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { marked } from "marked";
import MarkdownEditor from "./MarkdownEditor";
import SnippetFields from "./SnippetFields";
import { ClipService, type Entry, type EntryFilter, type EntryQuery } from "../lib/api";

// 统一内容视图：剪贴板采集、片段、手写备忘都在这里，靠属性区分。
// 上面是时间线（按创建时间正序，最新的贴着底部输入框），下面是输入卡片。

type Props = {
  filter: EntryFilter;
  day: string;
  onNotify: (message: string) => void;
  onChanged: () => void;
};

const PAGE_SIZE = 40;
const COMPOSER_MIN = 120;
const COMPOSER_DEFAULT = 184;
const MEMO_IMG_RE = /memoimg:([0-9a-f]{8,})/g;
const BLANK_PIXEL =
  "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";

function resolveMemoImages(html: string, cache: Record<string, string>): string {
  return html.replace(MEMO_IMG_RE, (_w, hash: string) => cache[hash] ?? BLANK_PIXEL);
}

function collectHashes(texts: string[]): string[] {
  const out = new Set<string>();
  for (const t of texts) for (const m of t.matchAll(MEMO_IMG_RE)) out.add(m[1]);
  return [...out];
}

function maxComposer(): number {
  return Math.max(COMPOSER_MIN + 40, Math.round(window.innerHeight * 0.5));
}

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

export default function EntriesView({ filter, day, onNotify, onChanged }: Props) {
  const [query, setQuery] = useState("");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [imgCache, setImgCache] = useState<Record<string, string>>({});
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState<Entry | null>(null);
  const [preview, setPreview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [composerHeight, setComposerHeight] = useState(() => {
    const saved = Number(localStorage.getItem("clipnest.composerHeight"));
    return Number.isFinite(saved) && saved >= COMPOSER_MIN ? saved : COMPOSER_DEFAULT;
  });
  const timelineRef = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);

  useEffect(() => {
    localStorage.setItem("clipnest.composerHeight", String(composerHeight));
  }, [composerHeight]);

  const baseQuery = useMemo<EntryQuery>(() => {
    const q: EntryQuery = { ...(filter.query ?? {}) } as EntryQuery;
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
    const oldest = entries[0];
    const cursor = oldest.createdAt;
    if (!cursor) return;
    setLoadingOlder(true);
    const host = timelineRef.current;
    const prevHeight = host?.scrollHeight ?? 0;
    try {
      const older =
        (await ClipService.GetEntries({
          ...baseQuery,
          limit: PAGE_SIZE,
          before: cursor,
        } as never)) ?? [];
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

  useEffect(() => {
    const missing = collectHashes([draft, ...entries.map((e) => e.content)]).filter((h) => !imgCache[h]);
    if (missing.length === 0) return;
    let alive = true;
    void (async () => {
      const next: Record<string, string> = {};
      for (const h of missing) {
        try {
          next[h] = await ClipService.GetImage(h);
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

  const previewHtml = useMemo(() => {
    const raw = marked.parse(draft, { async: false }) as string;
    return raw
      .replace(/<script[\s\S]*?<\/script>/gi, "")
      .replace(/<iframe[\s\S]*?<\/iframe>/gi, "")
      .replace(/ on[a-z]+="[^"]*"/gi, "")
      .replace(/javascript:/gi, "");
  }, [draft]);

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

  const toggleFlag = useCallback(
    async (e: Entry, field: "pinned" | "archived") => {
      try {
        await ClipService.SaveEntry({ ...e, [field]: !e[field] } as never);
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

  // 切换 clipboard 能力：有它才会出现在快速粘贴列表里
  const toggleClipboard = useCallback(
    async (e: Entry) => {
      const has = (e.actions ?? []).includes("clipboard");
      const next = has
        ? (e.actions ?? []).filter((a) => a !== "clipboard")
        : [...(e.actions ?? []), "clipboard"];
      try {
        await ClipService.SaveEntry({ ...e, actions: next } as never);
        await loadLatest();
        onChanged();
        onNotify(has ? "已从粘贴列表移除" : "已加入粘贴列表");
      } catch (err) {
        onNotify(`失败：${String(err)}`);
      }
    },
    [loadLatest, onChanged, onNotify],
  );

  const startComposerResize = useCallback(
    (ev: React.PointerEvent<HTMLDivElement>) => {
      ev.preventDefault();
      const startY = ev.clientY;
      const startH = composerHeight;
      const onMove = (e: PointerEvent) => {
        setComposerHeight(Math.min(maxComposer(), Math.max(COMPOSER_MIN, startH - (e.clientY - startY))));
      };
      const onUp = () => {
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        document.body.classList.remove("is-row-resizing");
      };
      document.body.classList.add("is-row-resizing");
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
    },
    [composerHeight],
  );

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
              {filter.name}里还没有内容。在下面写一句，或换个筛选器看看。
            </p>
          ) : (
            grouped.map((g) => (
              <section key={g.key} className="memo-day">
                <div className="memo-day-label">{g.day}</div>
                {g.items.map((e) => {
                  const isClip = e.source === "clipboard";
                  return (
                    <article
                      key={e.id}
                      className={`memo-card entry-card ${isClip ? "is-collected" : ""} ${
                        editing?.id === e.id ? "is-active" : ""
                      } ${e.pinned ? "is-pinned" : ""}`}
                      onClick={() => {
                        setEditing(e);
                        setDraft(e.content);
                        setPreview(false);
                      }}
                    >
                      {e.kind === "image" ? (
                        <div
                          className="entry-image"
                          dangerouslySetInnerHTML={{
                            __html: resolveMemoImages(
                              marked.parse(e.content, { async: false }) as string,
                              imgCache,
                            ),
                          }}
                        />
                      ) : (
                        <div
                          className="memo-body"
                          dangerouslySetInnerHTML={{
                            __html: resolveMemoImages(
                              marked.parse(e.content, { async: false }) as string,
                              imgCache,
                            ),
                          }}
                        />
                      )}
                      <footer className="memo-meta">
                        <span>{timeLabel(e.createdAt)}</span>
                        {(e.actions ?? []).map((a) => (
                          <span key={a} className="entry-autotag">
                            {a === "clipboard" ? "剪贴板" : a === "snippet" ? "片段" : a}
                          </span>
                        ))}
                        {(e.tags ?? []).map((t) => (
                          <span key={t} className="memo-tag">
                            #{t}
                          </span>
                        ))}
                        {e.triggerKey ? <span className="entry-trigger">{e.triggerKey}</span> : null}
                        {isClip ? <span className="memo-src">{sourceLabel(e)}</span> : null}
                        <span className="memo-actions">
                          <button
                            className={e.pinned ? "link-btn is-on" : "link-btn"}
                            onClick={(ev) => {
                              ev.stopPropagation();
                              void toggleFlag(e, "pinned");
                            }}
                          >
                            {e.pinned ? "取消置顶" : "置顶"}
                          </button>
                          {e.triggerKey ? (
                            <button
                              className="link-btn"
                              onClick={(ev) => {
                                ev.stopPropagation();
                                void ClipService.DemoteFromSnippet(e.id).then(() => {
                                  void loadLatest();
                                  onChanged();
                                  onNotify("已取消片段");
                                });
                              }}
                            >
                              取消片段
                            </button>
                          ) : (
                            <button
                              className="link-btn"
                              onClick={(ev) => {
                                ev.stopPropagation();
                                void makeSnippet(e);
                              }}
                            >
                              设为片段
                            </button>
                          )}
                          <button
                            className={
                              (e.actions ?? []).includes("clipboard") ? "link-btn is-on" : "link-btn"
                            }
                            onClick={(ev) => {
                              ev.stopPropagation();
                              void toggleClipboard(e);
                            }}
                            title="有这项能力才会出现在快速粘贴列表里"
                          >
                            {(e.actions ?? []).includes("clipboard") ? "移出粘贴列表" : "加入粘贴列表"}
                          </button>
                          <button
                            className="link-btn"
                            onClick={(ev) => {
                              ev.stopPropagation();
                              void toggleFlag(e, "archived");
                            }}
                          >
                            {e.archived ? "恢复" : "归档"}
                          </button>
                          <button
                            className="link-btn is-danger"
                            onClick={(ev) => {
                              ev.stopPropagation();
                              void remove(e);
                            }}
                          >
                            删除
                          </button>
                        </span>
                      </footer>
                    </article>
                  );
                })}
              </section>
            ))
          )}
        </div>
      </div>

      <div className="memo-composer" style={{ height: `${composerHeight}px` }}>
        <div
          className="memo-composer-resizer"
          role="separator"
          aria-orientation="horizontal"
          onPointerDown={startComposerResize}
          onDoubleClick={() => setComposerHeight(COMPOSER_DEFAULT)}
          title="拖动调整高度，双击复位"
        />
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
        <div className="composer-box">
          {preview ? (
            <div
              className="snip-preview composer-preview"
              dangerouslySetInnerHTML={{ __html: resolveMemoImages(previewHtml, imgCache) }}
            />
          ) : (
            <MarkdownEditor
              value={draft}
              onChange={setDraft}
              onSave={() => void save()}
              onImagePaste={(dataUrl) => {
                void (async () => {
                  try {
                    const token = await ClipService.SaveImage(dataUrl);
                    setDraft((prev) => prev + (prev && !prev.endsWith("\n") ? "\n" : "") + `![](${token})\n`);
                  } catch (err) {
                    setError(String(err));
                  }
                })();
              }}
              placeholder={
                editing ? "编辑这条内容…（Ctrl+Enter 保存）" : "随手记点什么…（Ctrl+Enter 保存）"
              }
            />
          )}
          <div className="composer-actions">
            <span className="composer-tags">
              {editing ? (
                <span className="composer-hint">正在编辑一条已有内容</span>
              ) : (
                <span className="composer-hint">写 #标签 就能归类 · Ctrl+Enter 保存</span>
              )}
            </span>
            <div className="composer-btns">
              <button className="link-btn" onClick={() => setPreview((v) => !v)}>
                {preview ? "编辑" : "预览"}
              </button>
              <button
                className="link-btn"
                onClick={() => {
                  setDraft("");
                  setEditing(null);
                  setPreview(false);
                  setError("");
                }}
              >
                {editing ? "取消编辑" : "清空"}
              </button>
              <button className="btn btn-primary btn-sm" onClick={save} disabled={busy}>
                {busy ? "保存中…" : "保存"}
              </button>
            </div>
          </div>
        </div>
        {editing && editing.source === "clipboard" ? (
          <p className="composer-hint entry-source-hint">
            采集自 {editing.sourceApp || "剪贴板"} · 复制 {editing.copyCount || 1} 次
            {(editing.seenCount ?? 0) > 1 ? ` · 出现 ${editing.seenCount} 次` : ""}
          </p>
        ) : null}
        {error ? <p className="snip-error composer-error">{error}</p> : null}
      </div>
    </div>
  );
}
