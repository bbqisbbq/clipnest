import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService, type Entry, type Snippet } from "./lib/api";
import { formatBytes, formatRelative } from "./lib/format";
import { IconSearch, kindIcon } from "./components/Icons";

// 片段在弹窗里的显示名与 Enter 行为（和后端 SnippetAction 对应）
const SNIPPET_LABEL: Record<string, string> = {
  text: "文本",
  password: "密码",
  image: "图片",
  file: "文件",
  url: "网址",
  app: "程序",
  command: "命令",
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

export default function Picker() {
  const [items, setItems] = useState<Entry[]>([]);
  const [snippets, setSnippets] = useState<Snippet[]>([]);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [status, setStatus] = useState("");
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
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
      setItems(list ?? []);
    } catch (err) {
      setStatus(`读取失败：${String(err)}`);
    }
  }, []);

  useEffect(() => {
    void loadItems();
    const offs = [
      Events.On("clip:new", () => void loadItems()),
      Events.On("clip:updated", () => void loadItems()),
      Events.On("picker:opened", () => {
        setQuery("");
        setActive(0);
        setStatus("");
        void loadItems();
      }),
    ];
    inputRef.current?.focus();
    return () => offs.forEach((off) => off());
  }, [loadItems]);

  // 片段查询（防抖，随输入变化）
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
  }, [snippetMode, snippetQuery]);

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
      if (e.key === "1" && snippetMode && query === "-") {
        // 输入第二个 "-" 由输入框处理，这里不拦截
      }
      if (/^[1-9]$/.test(e.key) && !e.altKey && !e.metaKey) {
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
  }, [rows, active, pick, snippetMode, query]);

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
            <span>1–9 直选</span>
            <span>↵ 粘贴</span>
            <span>-- 搜片段</span>
            <span>Esc 关闭</span>
          </>
        )}
      </div>
    </div>
  );
}
