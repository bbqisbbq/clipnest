import { useCallback, useEffect, useMemo, useState } from "react";
import { marked } from "marked";
import { ClipService, type Memo } from "../lib/api";

// 「备忘」= Memos 形态的碎片笔记，但完全本地：没有用户、权限、分享、评论。
// 布局学 Memos 的时间线：单列、按天分组、卡片留白、hover 才出操作。

type Props = {
  memos: Memo[];
  tags: string[];
  filter: { tag: string; pinned: boolean; archived: boolean };
  onReload: () => Promise<void>;
  onNotify: (message: string) => void;
};

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
      const saved = await ClipService.SaveMemo({ ...draft });
      setDraft({ ...saved });
      await props.onReload();
      props.onNotify(draft.id ? "已更新备忘" : "已记下");
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [draft, props]);

  const toggle = useCallback(
    async (m: Memo, field: "pinned" | "archived") => {
      try {
        await ClipService.SaveMemo({ ...m, [field]: !m[field] });
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

        <div className="memo-timeline">
          {grouped.length === 0 ? (
            <p className="snip-empty">
              还没有备忘。右边写一句，按 Ctrl+Enter 保存；正文里打 <code>#标签</code> 就能归类。
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

      <div className="memo-editor">
        <div className="snip-editor-head">
          <h3>{draft.id ? "编辑备忘" : "新备忘"}</h3>
          <div className="snip-editor-actions">
            <button className="link-btn" onClick={() => setPreview((v) => !v)}>
              {preview ? "编辑" : "预览"}
            </button>
            <button className="btn btn-primary btn-sm" onClick={save} disabled={busy}>
              {busy ? "保存中…" : "保存"}
            </button>
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => {
                setDraft(EMPTY);
                setPreview(false);
                setError("");
              }}
            >
              清空
            </button>
          </div>
        </div>

        {preview ? (
          <div className="snip-preview memo-preview" dangerouslySetInnerHTML={{ __html: previewHtml }} />
        ) : (
          <textarea
            className="input is-area memo-input"
            placeholder={"随手记点什么…\n\n支持 Markdown，标签写成 #工作 #灵感"}
            value={draft.content}
            onChange={(e) => setDraft({ ...draft, content: e.target.value })}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                void save();
              }
            }}
          />
        )}

        <div className="memo-editor-foot">
          <span className="snip-hint">
            {(draft.tags ?? []).length > 0 ? (draft.tags ?? []).map((t) => `#${t}`).join(" ") : "还没标签"}
          </span>
          <span className="snip-hint">Ctrl+Enter 保存</span>
        </div>
        {error ? <p className="snip-error">{error}</p> : null}
      </div>
    </div>
  );
}
