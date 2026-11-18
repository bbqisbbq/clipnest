import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { marked } from "marked";
import MarkdownEditor from "./MarkdownEditor";
import { ClipService, type Memo } from "../lib/api";

// 「备忘」= Memos 形态的碎片笔记，但完全本地：没有用户、权限、分享、评论。
//
// 布局像聊天：上面是时间线（按创建时间正序，最新的贴着底部输入框），
// 下面是固定在底部的输入卡片。向上滚到顶自动加载更早的一页。

type Props = {
  tags: string[];
  filter: { tag: string; pinned: boolean; archived: boolean };
  onReload: () => Promise<void>;
  onNotify: (message: string) => void;
};

const PAGE_SIZE = 40;
const COMPOSER_MIN = 120;
const COMPOSER_DEFAULT = 184;

const EMPTY: Memo = {
  id: "",
  content: "",
  tags: [],
  pinned: false,
  archived: false,
  source: "manual",
  sourceApp: "",
  hash: "",
  createdAt: "",
  updatedAt: "",
};

const MEMO_IMG_RE = /memoimg:([0-9a-f]{8,})/g;

// 把渲染结果里的 memoimg:<hash> 换成真实 data URL
function resolveMemoImages(html: string, cache: Record<string, string>): string {
  return html.replace(MEMO_IMG_RE, (whole, hash: string) => cache[hash] ?? whole);
}

function collectMemoImageHashes(texts: string[]): string[] {
  const out = new Set<string>();
  for (const text of texts) {
    for (const m of text.matchAll(MEMO_IMG_RE)) out.add(m[1]);
  }
  return [...out];
}

// toSavePayload 去掉空时间戳：绑定层会把 "" 当成 RFC3339 解析并报错。
function toSavePayload(m: Memo): Memo {
  const now = new Date().toISOString();
  return { ...m, createdAt: m.createdAt || now, updatedAt: m.updatedAt || now };
}

function maxComposer(): number {
  return Math.max(COMPOSER_MIN + 40, Math.round(window.innerHeight * 0.5));
}

function dayKey(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

function dayLabel(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date();
  const same = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  const yesterday = new Date(today.getTime() - 86400000);
  if (same(d, today)) return "今天";
  if (same(d, yesterday)) return "昨天";
  const sameYear = d.getFullYear() === today.getFullYear();
  return sameYear
    ? `${d.getMonth() + 1} 月 ${d.getDate()} 日`
    : `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

function timeLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

export default function MemosView(props: Props) {
  const [query, setQuery] = useState("");
  const [draft, setDraft] = useState<Memo>(EMPTY);
  const [preview, setPreview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [memos, setMemos] = useState<Memo[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [imgCache, setImgCache] = useState<Record<string, string>>({});
  const timelineRef = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);

  const [composerHeight, setComposerHeight] = useState(() => {
    const saved = Number(localStorage.getItem("clipnest.memoComposerHeight"));
    return Number.isFinite(saved) && saved >= COMPOSER_MIN ? saved : COMPOSER_DEFAULT;
  });
  useEffect(() => {
    localStorage.setItem("clipnest.memoComposerHeight", String(composerHeight));
  }, [composerHeight]);

  const startComposerResize = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      e.preventDefault();
      const startY = e.clientY;
      const startHeight = composerHeight;
      const onMove = (ev: PointerEvent) => {
        const next = Math.min(maxComposer(), Math.max(COMPOSER_MIN, startHeight - (ev.clientY - startY)));
        setComposerHeight(next);
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

  // ---- 分页加载 ----------------------------------------------------------

  // 第一页：最新的 PAGE_SIZE 条（正序，最新的在末尾）
  const loadLatest = useCallback(async () => {
    try {
      const list =
        (await ClipService.GetMemos({
          query: "",
          tag: props.filter.tag,
          pinned: props.filter.pinned,
          archived: props.filter.archived,
          limit: PAGE_SIZE,
          beforeCreatedAt: "",
          tail: true, // 首屏要最新的 N 条（后端倒序取完再翻正）
        } as never)) ?? [];
      setMemos(list);
      setHasMore(list.length >= PAGE_SIZE);
      stickToBottom.current = true;
    } catch (err) {
      console.error(err);
    }
  }, [props.filter]);

  // 更早的一页：用当前最早一条的 createdAt 作游标
  const loadOlder = useCallback(async () => {
    if (loadingOlder || !hasMore || memos.length === 0) return;
    const oldest = memos[0];
    const cursor = oldest.createdAt || oldest.updatedAt;
    if (!cursor) return;
    setLoadingOlder(true);
    const host = timelineRef.current;
    const prevHeight = host?.scrollHeight ?? 0;
    try {
      const older =
        (await ClipService.GetMemos({
          query: "",
          tag: props.filter.tag,
          pinned: props.filter.pinned,
          archived: props.filter.archived,
          limit: PAGE_SIZE,
          beforeCreatedAt: cursor,
          tail: false,
        } as never)) ?? [];
      if (older.length === 0) {
        setHasMore(false);
      } else {
        setMemos((prev) => [...older, ...prev]);
        setHasMore(older.length >= PAGE_SIZE);
        // 补进来之后把视口锚回原位置，避免"跳一下"
        requestAnimationFrame(() => {
          if (!host) return;
          host.scrollTop = host.scrollHeight - prevHeight;
        });
      }
    } catch (err) {
      console.error(err);
    } finally {
      setLoadingOlder(false);
    }
  }, [loadingOlder, hasMore, memos, props.filter]);

  useEffect(() => {
    void loadLatest();
  }, [loadLatest]);

  // 搜索：本地过滤当前已加载的，同时让父级刷新标签
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return memos;
    return memos.filter(
      (m) =>
        m.content.toLowerCase().includes(q) ||
        (m.tags ?? []).some((t) => t.toLowerCase().includes(q)),
    );
  }, [memos, query]);

  const grouped = useMemo(() => {
    const out: Array<{ day: string; key: string; items: Memo[] }> = [];
    for (const m of visible) {
      const key = dayKey(m.createdAt || m.updatedAt);
      const last = out[out.length - 1];
      if (last && last.key === key) last.items.push(m);
      else out.push({ day: dayLabel(m.createdAt || m.updatedAt), key, items: [m] });
    }
    return out;
  }, [visible]);

  // 新内容进来后贴到底部（只有本来就贴着底部时才自动滚）
  useEffect(() => {
    if (!stickToBottom.current) return;
    const host = timelineRef.current;
    if (!host) return;
    host.scrollTop = host.scrollHeight;
  }, [grouped]);

  // 图片按需加载
  useEffect(() => {
    const texts = [draft.content, ...memos.map((m) => m.content)];
    const missing = collectMemoImageHashes(texts).filter((h) => !imgCache[h]);
    if (missing.length === 0) return;
    let alive = true;
    void (async () => {
      const next: Record<string, string> = {};
      for (const hash of missing) {
        try {
          next[hash] = await ClipService.GetMemoImage(hash);
        } catch {
          // 图片被清理了：保留原样，显示 alt
        }
      }
      if (alive && Object.keys(next).length > 0) setImgCache((prev) => ({ ...prev, ...next }));
    })();
    return () => {
      alive = false;
    };
  }, [draft.content, memos, imgCache]);

  const previewHtml = useMemo(() => {
    const raw = marked.parse(draft.content ?? "", { async: false }) as string;
    return raw
      .replace(/<script[\s\S]*?<\/script>/gi, "")
      .replace(/<iframe[\s\S]*?<\/iframe>/gi, "")
      .replace(/ on[a-z]+="[^"]*"/gi, "")
      .replace(/javascript:/gi, "");
  }, [draft.content]);

  const save = useCallback(async () => {
    if (!draft.content.trim()) {
      setError("写点什么再保存");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const saved = await ClipService.SaveMemo(toSavePayload(draft));
      setDraft(EMPTY);
      stickToBottom.current = true;
      await loadLatest();
      await props.onReload();
      props.onNotify(draft.id ? "已更新备忘" : "已记下");
      // 保存后回到编辑器，方便连续记
      requestAnimationFrame(() => {
        const host = timelineRef.current;
        if (host) host.scrollTop = host.scrollHeight;
      });
      void saved;
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [draft, loadLatest, props]);

  const toggle = useCallback(
    async (m: Memo, field: "pinned" | "archived") => {
      try {
        await ClipService.SaveMemo(toSavePayload({ ...m, [field]: !m[field] }));
        await loadLatest();
        await props.onReload();
      } catch (err) {
        props.onNotify(`操作失败：${String(err)}`);
      }
    },
    [loadLatest, props],
  );

  const remove = useCallback(
    async (m: Memo) => {
      try {
        await ClipService.DeleteMemo(m.id);
        if (draft.id === m.id) setDraft(EMPTY);
        await loadLatest();
        await props.onReload();
        props.onNotify("已删除");
      } catch (err) {
        props.onNotify(`删除失败：${String(err)}`);
      }
    },
    [draft.id, loadLatest, props],
  );

  const filterLabel = props.filter.archived
    ? "已归档"
    : props.filter.pinned
      ? "置顶"
      : props.filter.tag
        ? `#${props.filter.tag}`
        : "全部";

  return (
    <div className="memos">
      <div className="memo-main">
        <header className="memo-head">
          <input
            className="input"
            placeholder="搜索备忘（支持中文子串）…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <span className="memo-count">
            {filterLabel} · {visible.length}
            {hasMore ? "+" : ""} 条
          </span>
        </header>

        <div
          className="memo-timeline"
          ref={timelineRef}
          onScroll={(e) => {
            const el = e.currentTarget;
            const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
            stickToBottom.current = distanceFromBottom < 48;
            if (el.scrollTop < 60) void loadOlder();
          }}
        >
          {hasMore ? (
            <div className="memo-more">
              {loadingOlder ? "正在加载更早的…" : "上滑加载更早"}
            </div>
          ) : null}

          {grouped.length === 0 ? (
            <p className="snip-empty">
              还没有备忘。在下面写一句，按 Ctrl+Enter 保存；正文里打 <code>#标签</code> 就能归类。
            </p>
          ) : (
            grouped.map((group) => (
              <section key={group.key} className="memo-day">
                <div className="memo-day-label">{group.day}</div>
                {group.items.map((m) => (
                  <article
                    key={m.id}
                    className={`memo-card ${draft.id === m.id ? "is-active" : ""} ${m.pinned ? "is-pinned" : ""}`}
                    onClick={() => {
                      setDraft({ ...m });
                      setPreview(false);
                    }}
                  >
                    <div
                      className="memo-body"
                      dangerouslySetInnerHTML={{
                        __html: resolveMemoImages(
                          marked.parse(m.content, { async: false }) as string,
                          imgCache,
                        ),
                      }}
                    />
                    <footer className="memo-meta">
                      <span>{timeLabel(m.createdAt || m.updatedAt)}</span>
                      {(m.tags ?? []).map((tag) => (
                        <span key={tag} className="memo-tag">
                          #{tag}
                        </span>
                      ))}
                      {m.source === "clipboard" ? <span className="memo-src">来自剪贴板</span> : null}
                      <span className="memo-actions">
                        <button
                          className={m.pinned ? "link-btn is-on" : "link-btn"}
                          onClick={(e) => {
                            e.stopPropagation();
                            void toggle(m, "pinned");
                          }}
                        >
                          {m.pinned ? "取消置顶" : "置顶"}
                        </button>
                        <button
                          className="link-btn"
                          onClick={(e) => {
                            e.stopPropagation();
                            void toggle(m, "archived");
                          }}
                        >
                          {m.archived ? "恢复" : "归档"}
                        </button>
                        <button
                          className="link-btn is-danger"
                          onClick={(e) => {
                            e.stopPropagation();
                            void remove(m);
                          }}
                        >
                          删除
                        </button>
                      </span>
                    </footer>
                  </article>
                ))}
              </section>
            ))
          )}
        </div>
      </div>

      {/* 固定在底部的输入区（像聊天输入框） */}
      <div className="memo-composer" style={{ height: `${composerHeight}px` }}>
        <div
          className="memo-composer-resizer"
          role="separator"
          aria-orientation="horizontal"
          aria-label="拖动调整输入区高度"
          onPointerDown={startComposerResize}
          onDoubleClick={() => setComposerHeight(COMPOSER_DEFAULT)}
          title="拖动调整高度，双击复位"
        />

        <div className="composer-box">
          {preview ? (
            <div
              className="snip-preview composer-preview"
              dangerouslySetInnerHTML={{ __html: resolveMemoImages(previewHtml, imgCache) }}
            />
          ) : (
            <MarkdownEditor
              value={draft.content}
              onChange={(next) => setDraft({ ...draft, content: next })}
              onSave={() => void save()}
              onImagePaste={(dataUrl) => {
                void (async () => {
                  try {
                    const token = await ClipService.SaveMemoImage(dataUrl);
                    const sep = draft.content && !draft.content.endsWith("\n") ? "\n" : "";
                    setDraft({ ...draft, content: draft.content + sep + "![](" + token + ")\n" });
                  } catch (err) {
                    setError(String(err));
                  }
                })();
              }}
              placeholder={"随手记点什么…（Enter 换行，Ctrl+Enter 保存）"}
            />
          )}

          <div className="composer-actions">
            <span className="composer-tags">
              {(draft.tags ?? []).length > 0 ? (
                (draft.tags ?? []).map((t) => (
                  <span key={t} className="composer-chip">
                    #{t}
                  </span>
                ))
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
                  setDraft(EMPTY);
                  setPreview(false);
                  setError("");
                }}
              >
                清空
              </button>
              <button className="btn btn-primary btn-sm" onClick={save} disabled={busy}>
                {busy ? "保存中…" : "保存"}
              </button>
            </div>
          </div>
        </div>

        {error ? <p className="snip-error composer-error">{error}</p> : null}
      </div>
    </div>
  );
}
