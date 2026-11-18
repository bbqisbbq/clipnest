import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { marked } from "marked";
import MarkdownEditor from "./MarkdownEditor";
import { ClipService, type Memo } from "../lib/api";

// 「备忘」= Memos 形态的碎片笔记，但完全本地：没有用户、权限、分享、评论。
// 布局：上面是时间线（可滚动），下面是固定在底部的输入框（像聊天输入）。

type Props = {
  memos: Memo[];
  tags: string[];
  filter: { tag: string; pinned: boolean; archived: boolean };
  onReload: () => Promise<void>;
  onNotify: (message: string) => void;
};

// toSavePayload 去掉空时间戳：绑定层会把 "" 当成 RFC3339 解析并报错。
function toSavePayload(m: Memo): Memo {
  const now = new Date().toISOString();
  return {
    ...m,
    createdAt: m.createdAt || now,
    updatedAt: m.updatedAt || now,
  };
}

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

const COMPOSER_MIN = 120;
const COMPOSER_DEFAULT = 184;

function maxComposer(): number {
  return Math.max(COMPOSER_MIN + 40, Math.round(window.innerHeight * 0.5));
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
  return `${d.getMonth() + 1} 月 ${d.getDate()} 日`;
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
  const timelineRef = useRef<HTMLDivElement>(null);

  // 输入区高度：默认 132px，可拖顶部分隔条调整，按内容自动长高（封顶视口一半）
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

  // 搜索走服务端（FTS5 trigram），所以输入后重新拉一次
  useEffect(() => {
    const timer = setTimeout(() => void props.onReload(), 220);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return props.memos;
    return props.memos.filter(
      (m) =>
        m.content.toLowerCase().includes(q) ||
        (m.tags ?? []).some((t) => t.toLowerCase().includes(q)),
    );
  }, [props.memos, query]);

  const grouped = useMemo(() => {
    const out: Array<{ day: string; items: Memo[] }> = [];
    for (const m of visible) {
      const day = dayLabel(m.createdAt || m.updatedAt);
      const last = out[out.length - 1];
      if (last && last.day === day) last.items.push(m);
      else out.push({ day, items: [m] });
    }
    return out;
  }, [visible]);

  // CodeMirror 自己滚动，输入区高度只由拖拽决定（默认 132px）

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
      setDraft({ ...saved });
      await props.onReload();
      props.onNotify(draft.id ? "已更新备忘" : "已记下");
      // 存完滚回顶部，新条目就在眼前
      if (timelineRef.current) timelineRef.current.scrollTop = 0;
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [draft, props]);

  const toggle = useCallback(
    async (m: Memo, field: "pinned" | "archived") => {
      try {
        await ClipService.SaveMemo(toSavePayload({ ...m, [field]: !m[field] }));
        await props.onReload();
      } catch (err) {
        props.onNotify(`操作失败：${String(err)}`);
      }
    },
    [props],
  );

  const remove = useCallback(
    async (m: Memo) => {
      try {
        await ClipService.DeleteMemo(m.id);
        if (draft.id === m.id) setDraft(EMPTY);
        await props.onReload();
        props.onNotify("已删除");
      } catch (err) {
        props.onNotify(`删除失败：${String(err)}`);
      }
    },
    [draft.id, props],
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
            {filterLabel} · {visible.length} 条
          </span>
        </header>

        <div className="memo-timeline" ref={timelineRef}>
          {grouped.length === 0 ? (
            <p className="snip-empty">
              还没有备忘。在下面写一句，按 Ctrl+Enter 保存；正文里打 <code>#标签</code> 就能归类。
            </p>
          ) : (
            grouped.map((group) => (
              <section key={group.day} className="memo-day">
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
                        __html: marked.parse(m.content, { async: false }) as string,
                      }}
                    />
                    <footer className="memo-meta">
                      <span>{timeLabel(m.createdAt || m.updatedAt)}</span>
                      {(m.tags ?? []).map((tag) => (
                        <button
                          key={tag}
                          className="memo-tag"
                          onClick={(e) => {
                            e.stopPropagation();
                            props.onNotify(`已按 #${tag} 筛选`);
                          }}
                        >
                          #{tag}
                        </button>
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
      <div
        className="memo-composer"
        style={{ height: `${composerHeight}px` }}
      >
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
              dangerouslySetInnerHTML={{ __html: previewHtml }}
            />
          ) : (
            <MarkdownEditor
              value={draft.content}
              onChange={(next) => setDraft({ ...draft, content: next })}
              onSave={() => void save()}
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
