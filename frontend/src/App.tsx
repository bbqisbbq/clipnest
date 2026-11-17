import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService, EventNames, newFilter, type AppInfo, type Item, type Settings, type Stats } from "./lib/api";
import Sidebar from "./components/Sidebar";
import ItemRow from "./components/ItemRow";
import DetailPanel from "./components/DetailPanel";
import SettingsView from "./components/SettingsView";
import { IconSearch } from "./components/Icons";

type View = "history" | "settings";

type Confirm = { message: string; action: () => void } | null;

export default function App() {
  const [view, setView] = useState<View>("history");
  const [kind, setKind] = useState("");
  const [pinnedOnly, setPinnedOnly] = useState(false);
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<Item[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [images, setImages] = useState<Record<string, string>>({});
  const [toast, setToast] = useState("");
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [loading, setLoading] = useState(true);

  const searchRef = useRef<HTMLInputElement | null>(null);
  const toastTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const notify = useCallback((message: string) => {
    setToast(message);
    clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(""), 2400);
  }, []);

  const loadItems = useCallback(async () => {
    try {
      const list = (await ClipService.GetItems(newFilter(query, kind, pinnedOnly))) ?? [];
      setItems(list);
      setSelectedId((current) =>
        current && list.some((i) => i.id === current) ? current : list.length > 0 ? list[0].id : null,
      );
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  }, [query, kind, pinnedOnly]);

  // Initial load.
  useEffect(() => {
    (async () => {
      try {
        const [st, info] = await Promise.all([ClipService.GetSettings(), ClipService.GetAppInfo()]);
        setSettings(st);
        setAppInfo(info);
        setStats(await ClipService.GetStats());
      } catch (err) {
        console.error(err);
      }
    })();
  }, []);

  // Debounced reload whenever the filter changes.
  useEffect(() => {
    const id = setTimeout(loadItems, query ? 160 : 0);
    return () => clearTimeout(id);
  }, [loadItems, query]);

  // Live updates from Go.
  useEffect(() => {
    const offs: Array<() => void> = [];
    offs.push(Events.On(EventNames.ClipNew, () => void loadItems()));
    offs.push(Events.On(EventNames.ClipBumped, () => void loadItems()));
    offs.push(Events.On(EventNames.ClipUpdated, () => void loadItems()));
    offs.push(Events.On(EventNames.ClipDeleted, () => void loadItems()));
    offs.push(Events.On(EventNames.ClipCleared, () => void loadItems()));
    offs.push(
      Events.On(EventNames.ClipStats, (ev: any) => {
        setStats(ev.data as Stats);
      }),
    );
    offs.push(
      Events.On(EventNames.SettingsChanged, (ev: any) => {
        setSettings(ev.data as Settings);
      }),
    );
    offs.push(
      Events.On("ui:navigate", (ev: any) => {
        if (ev.data === "settings") setView("settings");
      }),
    );
    return () => offs.forEach((off) => off());
  }, [loadItems]);

  // Lazily fetch image payloads for the selected entry.
  const selected = useMemo(() => items.find((i) => i.id === selectedId) ?? null, [items, selectedId]);

  useEffect(() => {
    if (!selected || selected.kind !== "image" || !selected.imageFile) return;
    if (images[selected.id]) return;
    ClipService.GetImageData(selected.id)
      .then((data) => setImages((prev) => ({ ...prev, [selected.id]: data })))
      .catch((err) => console.error(err));
  }, [selected, images]);

  const copyItem = useCallback(
    async (item: Item | null) => {
      if (!item) return;
      try {
        await ClipService.CopyItem(item.id);
        notify("已复制到剪贴板");
        void loadItems();
      } catch (err) {
        notify(`复制失败：${String(err)}`);
      }
    },
    [loadItems, notify],
  );

  const togglePin = useCallback(
    async (item: Item | null) => {
      if (!item) return;
      try {
        const updated = await ClipService.SetPinned(item.id, !item.pinned);
        setItems((prev) => prev.map((it) => (it.id === updated.id ? updated : it)));
        setStats(await ClipService.GetStats());
        notify(updated.pinned ? "已加入收藏" : "已取消收藏");
      } catch (err) {
        notify(`操作失败：${String(err)}`);
      }
    },
    [notify],
  );

  const deleteItem = useCallback(
    (item: Item | null) => {
      if (!item) return;
      setConfirm({
        message: `删除这条${item.preview ? `「${item.preview.slice(0, 24)}…」` : ""}记录？`,
        action: async () => {
          try {
            await ClipService.DeleteItem(item.id);
            setConfirm(null);
            notify("已删除");
            void loadItems();
          } catch (err) {
            notify(`删除失败：${String(err)}`);
          }
        },
      });
    },
    [loadItems, notify],
  );

  const clearHistory = useCallback(() => {
    setConfirm({
      message: "清空所有未收藏的记录？此操作不可撤销。",
      action: async () => {
        try {
          const n = await ClipService.DeleteWhere(newFilter());
          setConfirm(null);
          notify(`已清空 ${n} 条`);
          void loadItems();
        } catch (err) {
          notify(`清空失败：${String(err)}`);
        }
      },
    });
  }, [loadItems, notify]);

  const saveSettings = useCallback(
    async (next: Settings) => {
      try {
        const saved = await ClipService.SaveSettings(next);
        setSettings(saved);
        notify("设置已保存");
        void loadItems();
      } catch (err) {
        notify(`保存失败：${String(err)}`);
      }
    },
    [loadItems, notify],
  );

  const toggleCapture = useCallback(async () => {
    const current = settings?.capture ?? true;
    try {
      const saved = await ClipService.SetCapture(!current);
      setSettings(saved);
      notify(saved.capture ? "已恢复收集" : "已暂停收集");
    } catch (err) {
      notify(`切换失败：${String(err)}`);
    }
  }, [settings, notify]);

  const moveSelection = useCallback(
    (delta: number) => {
      if (items.length === 0) return;
      const index = items.findIndex((i) => i.id === selectedId);
      const next = Math.min(items.length - 1, Math.max(0, (index < 0 ? 0 : index) + delta));
      setSelectedId(items[next].id);
    },
    [items, selectedId],
  );

  // Keyboard shortcuts that make the list usable without the mouse.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const typing = target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName);
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "f") {
        e.preventDefault();
        setView("history");
        searchRef.current?.focus();
        searchRef.current?.select();
        return;
      }
      if (e.key === "Escape") {
        if (confirm) setConfirm(null);
        else if (typing) (target as HTMLElement).blur();
        else setSelectedId(null);
        return;
      }
      if (typing) return;
      if (e.key === "ArrowDown") {
        e.preventDefault();
        moveSelection(1);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        moveSelection(-1);
      } else if (e.key === "Enter") {
        e.preventDefault();
        void copyItem(selected);
      } else if (e.key === "Delete") {
        e.preventDefault();
        deleteItem(selected);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [confirm, copyItem, deleteItem, moveSelection, selected]);

  const capturing = settings?.capture ?? true;
  const storageBytes = stats?.storageBytes ?? 0;

  return (
    <div className="app">
      <Sidebar
        view={view}
        kind={kind}
        pinnedOnly={pinnedOnly}
        stats={stats}
        capturing={capturing}
        onSelectAll={() => {
          setView("history");
          setKind("");
          setPinnedOnly(false);
        }}
        onSelectKind={(k) => {
          setView("history");
          setKind(k);
          setPinnedOnly(false);
        }}
        onSelectPinned={() => {
          setView("history");
          setKind("");
          setPinnedOnly(true);
        }}
        onOpenSettings={() => setView("settings")}
        onToggleCapture={toggleCapture}
      />

      {view === "history" ? (
        <>
          <main className="main">
            <header className="main-head">
              <div className="search">
                <IconSearch size={16} />
                <input
                  ref={searchRef}
                  value={query}
                  placeholder="搜索文本、链接、文件名…"
                  onChange={(e) => setQuery(e.target.value)}
                />
                {query ? (
                  <button className="search-clear" onClick={() => setQuery("")}>
                    ×
                  </button>
                ) : (
                  <kbd>Ctrl F</kbd>
                )}
              </div>
              <div className="head-meta">
                <span>{items.length} 条</span>
                {!capturing ? <span className="warn">已暂停收集</span> : null}
                <button className="ghost-btn small" onClick={() => ClipService.HideWindow()}>
                  收起到托盘
                </button>
              </div>
            </header>

            <div className="list">
              {loading ? (
                <div className="empty-state">正在读取历史…</div>
              ) : items.length === 0 ? (
                <div className="empty-state">
                  <p>{query ? "没有匹配的记录" : "还没有记录"}</p>
                  <span>
                    {query ? "换个关键词试试" : "复制任意文本、图片或文件，ClipNest 会安静地把它记在这里"}
                  </span>
                </div>
              ) : (
                items.map((item) => (
                  <ItemRow
                    key={item.id}
                    item={item}
                    active={item.id === selectedId}
                    onSelect={() => setSelectedId(item.id)}
                    onCopy={() => void copyItem(item)}
                    onTogglePin={() => void togglePin(item)}
                  />
                ))
              )}
            </div>
          </main>

          <DetailPanel
            item={selected}
            imageData={selected ? (images[selected.id] ?? null) : null}
            onCopy={() => void copyItem(selected)}
            onTogglePin={() => void togglePin(selected)}
            onDelete={() => deleteItem(selected)}
            onOpen={() => {
              if (!selected) return;
              ClipService.OpenItem(selected.id).catch((err) => notify(`打开失败：${String(err)}`));
            }}
            onClose={() => setSelectedId(null)}
            onRefresh={() => {
              if (!selected) return;
              ClipService.RefreshItem(selected.id)
                .then((updated) => {
                  setItems((prev) => prev.map((it) => (it.id === updated.id ? updated : it)));
                  notify("已重新检查文件状态");
                })
                .catch((err) => notify(`检查失败：${String(err)}`));
            }}
          />
        </>
      ) : (
        <main className="main is-settings">
          <SettingsView
            settings={settings}
            appInfo={appInfo}
            storageBytes={storageBytes}
            onSave={(next) => void saveSettings(next)}
            onClearHistory={clearHistory}
            onOpenDataDir={() => void ClipService.OpenDataDir()}
            onQuit={() => void ClipService.Quit()}
          />
        </main>
      )}

      {toast ? <div className="toast">{toast}</div> : null}

      {confirm ? (
        <div className="modal-backdrop" onClick={() => setConfirm(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <p>{confirm.message}</p>
            <div className="modal-actions">
              <button className="ghost-btn" onClick={() => setConfirm(null)}>
                取消
              </button>
              <button className="danger-btn" onClick={() => void confirm.action()}>
                确认
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
