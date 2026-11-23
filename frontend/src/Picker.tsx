import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService, EventNames, type Entry, type Snippet } from "./lib/api";
import { useSettingsTheme } from "./hooks/useTheme";
import { useMinuteTick } from "./hooks/useClock";
import { formatBytes, formatRelative } from "./lib/format";
import { IconPin, IconSearch, IconTrash, IconFolder, kindIcon } from "./components/Icons";

// 片段在弹窗里的显示名与 Enter 行为（和后端 SnippetAction 对应）
const SNIPPET_LABEL: Record<string, string> = {
  text: "文本",
  password: "密码",
  image: "图片",
  file: "文件",
  url: "网址",
  app: "程序",
  command: "命令",
  // 富文本片段：以前标签表里没有它，界面上直接露出英文 "html"
  html: "富文本",
};

const SNIPPET_ACTION: Record<string, string> = {
  text: "粘贴",
  password: "粘贴",
  image: "粘贴图片",
  file: "复制文件",
  url: "打开",
  app: "启动",
  command: "执行",
};

type Row = { kind: "entry"; entry: Entry } | { kind: "snippet"; snippet: Snippet };

// 行内管理动作：只动 ClipNest 自己的数据，不触发粘贴。
type RowAction = "pin" | "archive" | "delete";

export default function Picker() {
  // 快速粘贴窗和「记一笔」一样是常驻窗口：不订阅设置就永远停在启动时那套配色。
  useSettingsTheme();
  // 行尾的"刚刚 / 3 分钟前"是渲染时算的：窗口一直挂着不动就不会变。一分钟一 tick，
  // 让它自己改口（只在弹窗打开这一小会儿生效，代价可以忽略）。
  useMinuteTick();
  const [items, setItems] = useState<Entry[]>([]);
  const [snippets, setSnippets] = useState<Snippet[]>([]);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [status, setStatus] = useState("");
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
  const [snippetTick, setSnippetTick] = useState(0);
  const [acting, setActing] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // "--" 前缀切到片段模式（和 eTool 的 -- 搜索一致）
  const snippetMode = query.startsWith("--");
  const snippetQuery = snippetMode ? query.slice(2).trim() : "";

  const rows: Row[] = useMemo(() => {
    if (snippetMode) return snippets.map((snippet) => ({ kind: "snippet", snippet }));
    return items.map((entry) => ({ kind: "entry", entry }));
  }, [snippetMode, snippets, items]);

  const loadItems = useCallback(async () => {
    try {
      const list = await ClipService.GetEntries({ action: "clipboard", limit: 60, tail: true } as never);
      const next = list ?? [];
      setItems(next);
      setActive((i) => Math.min(Math.max(0, i), Math.max(0, next.length - 1)));
    } catch (err) {
      setStatus(`读取失败：${String(err)}`);
    }
  }, []);

  useEffect(() => {
    void loadItems();
    const offs = [
      Events.On(EventNames.ClipNew, () => void loadItems()),
      Events.On(EventNames.ClipUpdated, () => void loadItems()),
      Events.On(EventNames.PickerOpened, () => {
        setQuery("");
        setActive(0);
        setStatus("");
        void loadItems();
      }),
      // 粘贴失败发生在窗口已经藏起来之后：不在这里显示，用户只会觉得"回车没反应"。
      Events.On(EventNames.PickerPasteFailed, (ev) => setStatus(`粘贴失败：${String(ev.data ?? "")}`)),
    ];
    inputRef.current?.focus();
    return () => offs.forEach((off) => off());
  }, [loadItems]);

  // 上下键移动时把选中行滚进视口：以前 listRef 只挂不用，
  // 选到第 10 条以后高亮就跑到视野外，等于盲选。
  useEffect(() => {
    const host = listRef.current;
    if (!host) return;
    const el = host.querySelector<HTMLElement>(`[data-index="${active}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [active, rows.length]);

  // 片段查询（防抖，随输入变化；tick 用于动作后手动刷新）
  useEffect(() => {
    if (!snippetMode) {
      setSnippets([]);
      return;
    }
    let alive = true;
    const timer = setTimeout(() => {
      void (async () => {
        try {
          const list = await ClipService.PickerSnippets(snippetQuery, 30);
          if (alive) {
            setSnippets(list ?? []);
            setActive(0);
          }
        } catch (err) {
          if (alive) setStatus(`片段读取失败：${String(err)}`);
        }
      })();
    }, 80);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [snippetMode, snippetQuery, snippetTick]);

  // 缩略图：只为前 10 条图片条目按需拉取
  useEffect(() => {
    const wanted = items
      .slice(0, 10)
      .filter((it) => it.kind === "image" && !thumbs[it.id]);
    if (wanted.length === 0) return;
    let alive = true;
    void (async () => {
      for (const it of wanted) {
        try {
          const url = await ClipService.GetImageData(it.id);
          if (alive && url) setThumbs((prev) => ({ ...prev, [it.id]: url }));
        } catch {
          /* 缩略图失败不影响选择 */
        }
      }
    })();
    return () => {
      alive = false;
    };
  }, [items, thumbs]);

  const pick = useCallback(async (row: Row | undefined, paste: boolean) => {
    if (!row) return;
    try {
      const msg =
        row.kind === "snippet"
          ? await ClipService.PickSnippet(row.snippet.id, paste)
          : await ClipService.PickEntry(row.entry.id, paste);
      if (msg) setStatus(msg);
    } catch (err) {
      setStatus(`失败：${String(err)}`);
    }
  }, []);

  // 行内管理：置顶 / 归档 / 删除。列表原地刷新，弹窗不关、不粘贴。
  const act = useCallback(
    async (row: Row | undefined, action: RowAction) => {
      if (!row) return;
      const id = row.kind === "snippet" ? row.snippet.id : row.entry.id;
      const entry = row.kind === "entry" ? row.entry : null;
      setActing(id);
      try {
        if (action === "delete") {
          await ClipService.DeleteEntry(id);
          setStatus("已删除");
        } else if (action === "pin") {
          const next = !(entry?.pinned ?? false);
          await ClipService.SetEntryPinned(id, next);
          setStatus(next ? "已置顶" : "已取消置顶");
        } else {
          const next = !(entry?.archived ?? false);
          await ClipService.SetEntryArchived(id, next);
          setStatus(next ? "已归档" : "已恢复");
        }
        setThumbs((prev) => {
          if (!(id in prev)) return prev;
          const { [id]: _drop, ...rest } = prev;
          return rest;
        });
        await loadItems();
        if (snippetMode) setSnippetTick((n) => n + 1);
      } catch (err) {
        setStatus(`操作失败：${String(err)}`);
      } finally {
        setActing("");
      }
    },
    [loadItems, snippetMode],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        void ClipService.HidePicker();
        return;
      }
      if (e.key === "ArrowDown" || (e.ctrlKey && e.key.toLowerCase() === "n")) {
        e.preventDefault();
        setActive((i) => Math.min(rows.length - 1, i + 1));
        return;
      }
      if (e.key === "ArrowUp" || (e.ctrlKey && e.key.toLowerCase() === "p")) {
        e.preventDefault();
        setActive((i) => Math.max(0, i - 1));
        return;
      }
      // 行内管理（不用鼠标也能整理）：Alt+P 置顶 / Alt+A 归档 / Ctrl+Del 删除。
      // 删除必须带 Ctrl：搜索框一直有焦点，裸 Del 是删字符，不是删条目。
      if (e.altKey && e.key.toLowerCase() === "p") {
        e.preventDefault();
        void act(rows[active], "pin");
        return;
      }
      if (e.altKey && e.key.toLowerCase() === "a") {
        e.preventDefault();
        void act(rows[active], "archive");
        return;
      }
      if (e.ctrlKey && e.key === "Delete") {
        e.preventDefault();
        void act(rows[active], "delete");
        return;
      }
      // 数字直选：只在没输入关键词时生效。搜索框一直有焦点，
      // 以前无条件拦 1-9，搜 "2026" 会直接粘第 2 条。
      if (query === "" && /^[1-9]$/.test(e.key) && !e.altKey && !e.metaKey) {
        const idx = Number(e.key) - 1;
        if (idx < rows.length) {
          e.preventDefault();
          setActive(idx);
          void pick(rows[idx], !e.ctrlKey);
        }
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        void pick(rows[active], !e.ctrlKey);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [rows, active, pick, act, snippetMode, query]);

  const actions = (row: Row) => {
    const entry = row.kind === "entry" ? row.entry : null;
    const id = row.kind === "snippet" ? row.snippet.id : row.entry.id;
    const busy = acting === id;
    return (
      <span className="picker-actions" onMouseDown={(e) => e.stopPropagation()}>
        {snippetMode ? null : (
          <button
            type="button"
            className={`picker-act ${entry?.pinned ? "is-on" : ""}`}
            title={entry?.pinned ? "取消置顶（Alt+P）" : "置顶（Alt+P）"}
            disabled={busy}
            onClick={(e) => {
              e.stopPropagation();
              void act(row, "pin");
            }}
          >
            <IconPin size={12} filled={entry?.pinned} />
          </button>
        )}
        {snippetMode ? null : (
          <button
            type="button"
            className={`picker-act ${entry?.archived ? "is-on" : ""}`}
            title={entry?.archived ? "取消归档（Alt+A）" : "归档（Alt+A）"}
            disabled={busy}
            onClick={(e) => {
              e.stopPropagation();
              void act(row, "archive");
            }}
          >
            <IconFolder size={12} />
          </button>
        )}
        <button
          type="button"
          className="picker-act is-danger"
          title="删除（Ctrl+Del）"
          disabled={busy}
          onClick={(e) => {
            e.stopPropagation();
            void act(row, "delete");
          }}
        >
          <IconTrash size={12} />
        </button>
      </span>
    );
  };

  return (
    <div className="picker">
      <div className="picker-search">
        <IconSearch size={15} />
        <input
          ref={inputRef}
          value={query}
          placeholder="搜索历史，输入 -- 搜索片段…"
          autoFocus
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
        />
        <span className="picker-count">{rows.length}</span>
      </div>

      <div className="picker-list" ref={listRef}>
        {rows.length === 0 ? (
          <div className="picker-empty">
            {snippetMode ? "没有匹配的片段" : query ? "没有匹配的记录" : "还没有记录"}
          </div>
        ) : (
          rows.map((row, index) => {
            if (row.kind === "snippet") {
              const sp = row.snippet;
              const Icon = kindIcon(sp.kind === "url" ? "link" : sp.kind === "image" ? "image" : "text");
              const masked = sp.kind === "password";
              return (
                <div
                  key={`s-${sp.id}`}
                  data-index={index}
                  className={`picker-row is-snippet ${index === active ? "is-active" : ""}`}
                  onMouseEnter={() => setActive(index)}
                  onClick={() => void pick(row, true)}
                >
                  <div className={`picker-icon kind-${sp.kind === "url" ? "link" : sp.kind}`}>
                    <Icon size={15} />
                  </div>
                  <div className="picker-main">
                    <span className="picker-preview">
                      {sp.trigger ? <code className="picker-trigger">{sp.trigger}</code> : null}
                      {sp.title}
                      {sp.trigger ? null : <em className="picker-group">{SNIPPET_LABEL[sp.kind] ?? sp.kind}</em>}
                    </span>
                    <span className="picker-meta">
                      片段 · {SNIPPET_LABEL[sp.kind] ?? sp.kind}
                      {sp.group ? ` · ${sp.group}` : ""} ·{" "}
                      {masked ? "••••••" : sp.body.replace(/\s+/g, " ").slice(0, 48)}
                    </span>
                  </div>
                  {actions(row)}
                  {index < 9 ? <span className="picker-num">{index + 1}</span> : null}
                  {index === active ? <kbd>{SNIPPET_ACTION[sp.kind] ?? "执行"}</kbd> : null}
                </div>
              );
            }

            const item = row.entry;
            const Icon = kindIcon(item.kind);
            const thumb = item.kind === "image" ? thumbs[item.id] : undefined;
            return (
              <div
                key={item.id}
                data-index={index}
                className={`picker-row ${index === active ? "is-active" : ""}`}
                role="option"
                aria-selected={index === active}
                onMouseEnter={() => setActive(index)}
                onClick={() => void pick(row, true)}
              >
                <div className={`picker-icon kind-${item.kind} ${thumb ? "has-thumb" : ""}`}>
                  {thumb ? <img className="picker-thumb" src={thumb} alt="" /> : <Icon size={15} />}
                </div>
                <div className="picker-main">
                  <span className="picker-preview">{(item.content || "") || "（空）"}</span>
                  <span className="picker-meta">
                    {item.sourceApp ? `${item.sourceApp} · ` : ""}
                    {formatBytes((item.content || "").length)} · {formatRelative(item.updatedAt)}
                  </span>
                </div>
                {actions(row)}
                {index < 9 ? <span className="picker-num">{index + 1}</span> : null}
                {index === active ? <kbd>↵</kbd> : null}
              </div>
            );
          })
        )}
      </div>

      <div className="picker-foot">
        {status ? (
          <span className="picker-status">{status}</span>
        ) : snippetMode ? (
          <>
            <span>片段模式</span>
            <span>↑↓ 选择</span>
            <span>↵ 执行</span>
            <span>Ctrl+↵ 仅复制</span>
            <span>Esc 关闭</span>
          </>
        ) : (
          <>
            <span>↑↓ 选择</span>
            <span>↵ 粘贴</span>
            <span>1–9 直选</span>
            <span>-- 搜片段</span>
            <span>Alt+P 置顶</span>
            <span>Alt+A 归档</span>
            <span>Ctrl+Del 删除</span>
          </>
        )}
      </div>
    </div>
  );
}
