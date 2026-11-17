import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService, KIND_LABEL, newFilter, type Item } from "./lib/api";
import { formatBytes, formatRelative } from "./lib/format";
import { IconSearch, kindIcon } from "./components/Icons";

/**
 * Picker is the hotkey popup: it appears at the text caret, lists recent
 * entries, and pastes the chosen one straight into the window that had focus.
 * Keyboard: ↑/↓ move, Enter pastes, Ctrl+Enter copies only, Esc dismisses.
 */
export default function Picker() {
  const [items, setItems] = useState<Item[]>([]);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [status, setStatus] = useState("");
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const load = useCallback(async (q: string) => {
    try {
      const list = (await ClipService.GetItems(newFilter(q, "", false, 100))) ?? [];
      setItems(list);
      setActive(0);
    } catch (err) {
      setStatus(`读取失败：${String(err)}`);
    }
  }, []);

  // Reload (and refocus) every time the popup is opened from the hotkey.
  useEffect(() => {
    const offs: Array<() => void> = [];
    offs.push(
      Events.On("picker:opened", () => {
        setQuery("");
        setStatus("");
        void load("");
        requestAnimationFrame(() => inputRef.current?.focus());
      }),
    );
    offs.push(
      Events.On("clip:new", () => {
        void load(query);
      }),
    );
    offs.push(
      Events.On("picker:paste-failed", (ev: any) => {
        setStatus(`自动粘贴失败：${ev.data}（内容已在剪贴板，可手动 Ctrl+V）`);
      }),
    );
    void load("");
    inputRef.current?.focus();
    return () => offs.forEach((off) => off());
  }, [load, query]);

  // Debounced search.
  useEffect(() => {
    const id = setTimeout(() => void load(query), query ? 120 : 0);
    return () => clearTimeout(id);
  }, [query, load]);

  // Keep the active row visible.
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [active, items]);

  // 缩略图：只为前 10 条图片条目按需拉取 data URL，避免一次性解码整段历史
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
  useEffect(() => {
    const wanted = items
      .slice(0, 10)
      .filter((it) => it.kind === "image" && it.imageFile && !thumbs[it.id]);
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

  const pick = useCallback(
    (item: Item | undefined, paste: boolean) => {
      if (!item) return;
      ClipService.PickItem(item.id, paste)
        .then((msg) => setStatus(msg))
        .catch((err) => setStatus(`失败：${String(err)}`));
    },
    [],
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
        setActive((i) => Math.min(items.length - 1, i + 1));
        return;
      }
      if (e.key === "ArrowUp" || (e.ctrlKey && e.key.toLowerCase() === "p")) {
        e.preventDefault();
        setActive((i) => Math.max(0, i - 1));
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        pick(items[active], !e.ctrlKey);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [items, active, pick]);

  return (
    <div className="picker">
      <div className="picker-search">
        <IconSearch size={15} />
        <input
          ref={inputRef}
          value={query}
          placeholder="搜索后回车粘贴…"
          autoFocus
          onChange={(e) => setQuery(e.target.value)}
        />
        <span className="picker-count">{items.length}</span>
      </div>

      <div className="picker-list" ref={listRef}>
        {items.length === 0 ? (
          <div className="picker-empty">{query ? "没有匹配的记录" : "还没有记录"}</div>
        ) : (
          items.map((item, index) => {
            const Icon = kindIcon(item.kind);
            const thumb = item.kind === "image" ? thumbs[item.id] : undefined;
            return (
              <div
                key={item.id}
                data-index={index}
                className={`picker-row ${index === active ? "is-active" : ""}`}
                onMouseEnter={() => setActive(index)}
                onClick={() => pick(item, true)}
              >
                <div className={`picker-icon kind-${item.kind} ${thumb ? "has-thumb" : ""}`}>
                  {thumb ? <img className="picker-thumb" src={thumb} alt="" /> : <Icon size={15} />}
                </div>
                <div className="picker-main">
                  <span className="picker-preview">{item.preview || "（空）"}</span>
                  <span className="picker-meta">
                    {KIND_LABEL[item.kind] ?? item.kind}
                    {item.sourceApp ? ` · ${item.sourceApp}` : ""} · {formatBytes(item.size)} ·{" "}
                    {formatRelative(item.updatedAt)}
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
        ) : (
          <>
            <span>↑↓ 选择</span>
            <span>1–9 直选</span>
            <span>↵ 粘贴</span>
            <span>Ctrl+↵ 仅复制</span>
            <span>Esc 关闭</span>
          </>
        )}
      </div>
    </div>
  );
}
