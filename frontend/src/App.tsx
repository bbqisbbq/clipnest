import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import {
  THEME_PRESETS,
  applyCustomCSS,
  applyTheme as applyThemePreset,
  type ThemeScheme,
} from "./lib/themes";
import { ClipService, EventNames, newFilter, type AppInfo, type EntryFilter, type EntryStats, type Item, type Settings, type Stats, type SyncStatus, type UpdateInfo } from "./lib/api";
import TitleBar from "./components/TitleBar";
import Sidebar from "./components/Sidebar";
import ItemRow from "./components/ItemRow";
import DetailPanel from "./components/DetailPanel";
import SettingsView from "./components/SettingsView";
import EntriesView from "./components/EntriesView";
import { IconSearch } from "./components/Icons";

type View = "history" | "settings" | "entries";

// 一级菜单的默认形状；用户可改顺序、名称与图标（存在 settings 里）
type Confirm = { message: string; action: () => void } | null;

export default function App() {
  const [view, setView] = useState<View>("entries");
  const [kind, setKind] = useState("");
  const [pinnedOnly, setPinnedOnly] = useState(false);
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<Item[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [syncStatus, setSyncStatus] = useState<SyncStatus | null>(null);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [memoCount, setMemoCount] = useState(0);
  // 统一内容视图：筛选器 + 日历
  const [filters, setFilters] = useState<EntryFilter[]>([]);
  const [activeFilterId, setActiveFilterId] = useState("all");
  // 默认只看今天的时间线（像聊天一样，打开就是当下的上下文）
  const [selectedDay, setSelectedDay] = useState(() => {
    const d = new Date();
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  });
  const [entryStats, setEntryStats] = useState<EntryStats | null>(null);
  const [memoTags, setMemoTags] = useState<string[]>([]);
  const [memoFilter, setMemoFilter] = useState<{ tag: string; pinned: boolean; archived: boolean }>({
    tag: "",
    pinned: false,
    archived: false,
  });
  const [updateBanner, setUpdateBanner] = useState(true);
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
        const [st, info, sync] = await Promise.all([
          ClipService.GetSettings(),
          ClipService.GetAppInfo(),
          ClipService.GetSyncStatus(),
        ]);
        setSettings(st);
        setAppInfo(info);
        setSyncStatus(sync);
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
    for (const name of [
      EventNames.ClipNew,
      EventNames.ClipBumped,
      EventNames.ClipUpdated,
      EventNames.ClipDeleted,
      EventNames.ClipCleared,
    ]) {
      offs.push(Events.On(name, () => void loadItems()));
    }
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
      Events.On("sync:status", (ev: any) => {
        setSyncStatus(ev.data as SyncStatus);
      }),
    );
    offs.push(
      Events.On("update:available", (ev: any) => {
        setUpdateInfo(ev.data as UpdateInfo);
        setUpdateBanner(true);
      }),
    );
    offs.push(
      Events.On("ui:navigate", (ev: any) => {
        if (ev.data === "settings") setView("settings");
      }),
    );
    return () => offs.forEach((off) => off());
  }, [loadItems]);

  // 主题：配色预设 + 浅/深 scheme + 用户自定义 CSS（见 lib/themes.ts）
  const themeScheme = (settings?.theme ?? "system") as ThemeScheme;
  const themePreset = settings?.themePreset ?? "default-dark";
  const themeCss = settings?.themeCustomCss ?? "";
  useEffect(() => {
    applyThemePreset(themeScheme, themePreset);
    applyCustomCSS(themeCss);
    localStorage.setItem("clipnest.theme", themeScheme);
    localStorage.setItem("clipnest.preset", themePreset);
    // 原生窗口边框/标题区也跟着换，否则浅色主题下会残留系统深色边
    const preset = THEME_PRESETS.find((p) => p.id === themePreset) ?? THEME_PRESETS[0];
    void ClipService.SetWindowChrome(preset.scheme === "dark", preset.tokens["--bg"] ?? "").catch(
      () => {},
    );
    const mq = window.matchMedia?.("(prefers-color-scheme: light)");
    const onChange = () => {
      if (themeScheme === "system") applyThemePreset(themeScheme, themePreset);
    };
    mq?.addEventListener("change", onChange);
    return () => mq?.removeEventListener("change", onChange);
  }, [themeScheme, themePreset, themeCss]);

  // 把一条剪贴板历史塞进片段编辑器（可改标题/类型/正文再保存）
  // 统一内容的筛选器与统计
  const loadEntryMeta = useCallback(async () => {
    try {
      setFilters((await ClipService.GetFilters()) ?? []);
      setEntryStats(await ClipService.GetEntryStats());
    } catch (err) {
      console.error(err);
    }
  }, []);

  useEffect(() => {
    void loadEntryMeta();
  }, [loadEntryMeta]);

  const activeFilter = useMemo(
    () => filters.find((f) => f.id === activeFilterId) ?? filters[0],
    [filters, activeFilterId],
  );

  // 只刷新侧栏需要的计数与标签；列表本身由 EntriesView 分页拉取
  const loadMemoMeta = useCallback(async () => {
    try {
      const stats = await ClipService.GetEntryStats();
      setMemoCount(stats?.manual ?? 0);
      setEntryStats(stats);
      setMemoTags((await ClipService.GetEntryTags()) ?? []);
    } catch (err) {
      console.error(err);
    }
  }, []);

  useEffect(() => {
    void loadMemoMeta();
  }, [view, loadMemoMeta]);

  const editAsSnippet = useCallback(
    async (item: Item | null) => {
      if (!item) return;
      try {
        const draft = await ClipService.DraftSnippetFromItem(item.id);
        if (!draft?.snippet) return;
        setActiveFilterId("snippets");
        setView("entries");
        void loadEntryMeta();
      } catch (err) {
        notify(`打开为片段失败：${String(err)}`);
      }
    },
    [loadEntryMeta, notify],
  );

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
        message: `删除这条记录？${item.preview ? `「${item.preview.slice(0, 24)}」` : ""}`,
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
        setSyncStatus(await ClipService.GetSyncStatus());
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

  const runSync = useCallback(async () => {
    try {
      const status = await ClipService.SyncNow();
      setSyncStatus(status);
      notify(status.lastError ? `同步失败：${status.lastError}` : status.lastResult || "同步完成");
      void loadItems();
    } catch (err) {
      notify(`同步失败：${String(err)}`);
    }
  }, [loadItems, notify]);

  const testSync = useCallback(async () => {
    return await ClipService.TestSync();
  }, []);

  const checkUpdate = useCallback(async () => {
    try {
      const info = await ClipService.CheckUpdate();
      setUpdateInfo(info);
      if (info.available) {
        setUpdateBanner(true);
        notify(`发现新版本 v${info.version}`);
      } else if (!info.error) {
        notify("已是最新版本");
      } else {
        notify(`检查更新失败：${info.error}`);
      }
      return info;
    } catch (err) {
      notify(`检查更新失败：${String(err)}`);
      throw err;
    }
  }, [notify]);

  const installUpdate = useCallback(() => {
    void (async () => {
      notify("正在下载更新，完成后会自动安装…");
      try {
        await ClipService.InstallUpdate();
      } catch (err) {
        notify(`更新失败：${String(err)}`);
      }
    })();
  }, [notify]);

  const moveSelection = useCallback(
    (delta: number) => {
      if (items.length === 0) return;
      const index = items.findIndex((i) => i.id === selectedId);
      const next = Math.min(items.length - 1, Math.max(0, (index < 0 ? 0 : index) + delta));
      setSelectedId(items[next].id);
    },
    [items, selectedId],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      // CodeMirror 用 contenteditable，必须一并当成"正在输入"，否则回车会漏到列表快捷键上
      const typing =
        !!target &&
        (/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName) || target.isContentEditable);
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "f") {
        e.preventDefault();
        setView("history");
        requestAnimationFrame(() => {
          searchRef.current?.focus();
          searchRef.current?.select();
        });
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
  const syncActive = Boolean(settings?.syncEnabled);

  return (
    <div className={`app ${view === "settings" ? "is-settings" : ""} ${view === "entries" ? "is-entries" : ""}`}>
      <TitleBar capturing={capturing} enabled={settings !== null} onToggleCapture={toggleCapture} />

      {updateInfo?.available && updateBanner ? (
        <div className="update-banner">
          <span>
            有新版本 <strong>v{updateInfo.version}</strong>（当前 v{updateInfo.current}）
            {updateInfo.assetSize ? ` · ${Math.round(updateInfo.assetSize / 1024 / 1024)} MB` : ""}
          </span>
          <span className="update-banner-actions">
            <button className="btn btn-primary btn-sm" onClick={installUpdate}>
              下载并安装
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => void ClipService.OpenReleasePage()}>
              更新说明
            </button>
            <button className="btn btn-ghost btn-sm" onClick={() => setUpdateBanner(false)}>
              稍后
            </button>
          </span>
        </div>
      ) : null}

      <div className="workspace">
        <Sidebar
          view={view}
          kind={kind}
          pinnedOnly={pinnedOnly}
          stats={stats}
          capturing={capturing}
          syncActive={syncActive}
          onSelectHistory={(k, pinned) => {
            setView("history");
            setKind(k);
            setPinnedOnly(pinned);
          }}
          onSelectSnippets={() => {
            setActiveFilterId("snippets");
            setView("entries");
          }}
          filterProps={{
            filters,
            activeFilterId,
            onSelectFilter: (f) => {
              setActiveFilterId(f.id);
              setView("entries");
            },
            selectedDay,
            onPickDay: (day) => {
              setSelectedDay(day);
              setView("entries");
            },
            onEditFilter: (f) => {
              const name = window.prompt("筛选器名称：", f.name);
              if (name === null) return;
              const icon = window.prompt(
                "图标（all/pin/snippet/memo/star/folder/files/image/text/link/search）：",
                f.icon || "memo",
              );
              void ClipService.SaveFilter({ ...f, name: name.trim() || f.name, icon: (icon || f.icon).trim() })
                .then(() => void loadEntryMeta())
                .catch((err) => notify(`保存筛选器失败：${String(err)}`));
            },
            entryStats,
          }}
          memoFilter={memoFilter}
          memoTags={memoTags}
          memoCount={memoCount}
          onSelectMemos={(filter) => {
            setMemoFilter({
              tag: filter.tag ?? "",
              pinned: filter.pinned ?? false,
              archived: filter.archived ?? false,
            });
            setActiveFilterId("mine");
            setView("entries");
          }}
          onOpenSearch={() => {
            setView("history");
            requestAnimationFrame(() => searchRef.current?.focus());
          }}
          onOpenSettings={() => setView("settings")}
          onToggleCapture={toggleCapture}
        />

        {view === "history" ? (
          <>
            <main className="main">
              <header className="list-head">
                <div className="search">
                  <IconSearch size={15} />
                  <input
                    ref={searchRef}
                    value={query}
                    placeholder="搜索文本、链接、文件名…"
                    onChange={(e) => setQuery(e.target.value)}
                  />
                  {query ? (
                    <button className="search-clear" onClick={() => setQuery("")} title="清空">
                      ×
                    </button>
                  ) : (
                    <kbd>Ctrl F</kbd>
                  )}
                </div>
                <span className="list-count">{items.length} 条</span>
              </header>

              <div className="list">
                {loading ? (
                  <div className="empty-state">正在读取历史…</div>
                ) : items.length === 0 ? (
                  <div className="empty-state">
                    <p>{query ? "没有匹配的记录" : "还没有记录"}</p>
                    <span>{query ? "换个关键词试试" : "复制任意文本、图片或文件，ClipNest 会安静地记在这里"}</span>
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
                      onDelete={() => deleteItem(item)}
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
              onEditAsSnippet={() => void editAsSnippet(selected)}
              onSaveAsMemo={() => {
                if (!selected) return;
                ClipService.SaveEntry({ content: selected.text ?? "", source: "manual", kind: "text" } as never)
                  .then(() => {
                    notify("已存为备忘");
                    void loadMemoMeta();
                  })
                  .catch((err) => notify(`保存失败：${String(err)}`));
              }}
              onSaveAsSnippet={() => {
                if (!selected) return;
                ClipService.SaveItemAsSnippet(selected.id)
                  .then(() => {
                    notify("已存为片段，可在「片段」筛选器里配缩写");
                    void loadEntryMeta();
                  })
                  .catch((err) => notify(`保存失败：${String(err)}`));
              }}
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
        ) : view === "entries" ? (
          <EntriesView
            filter={activeFilter ?? { id: "all", name: "全部内容", icon: "all", query: {}, sortOrder: 0, builtin: true }}
            day={selectedDay}
            onNotify={notify}
            onChanged={() => void loadEntryMeta()}
          />
        ) : (
          <SettingsView
            settings={settings}
            appInfo={appInfo}
            storageBytes={stats?.storageBytes ?? 0}
            syncStatus={syncStatus}
            onSave={(next) => void saveSettings(next)}
            onClearHistory={clearHistory}
            onOpenDataDir={() => void ClipService.OpenDataDir()}
            onQuit={() => void ClipService.Quit()}
            updateInfo={updateInfo}
          onCheckUpdate={checkUpdate}
          onInstallUpdate={installUpdate}
          onOpenRelease={() => void ClipService.OpenReleasePage()}
          onTestSync={testSync}
            onSyncNow={() => void runSync()}
          />
        )}
      </div>

      {toast ? <div className="toast">{toast}</div> : null}

      {confirm ? (
        <div className="modal-backdrop" onClick={() => setConfirm(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <p>{confirm.message}</p>
            <div className="modal-actions">
              <button className="btn btn-ghost" onClick={() => setConfirm(null)}>
                取消
              </button>
              <button className="btn btn-danger" onClick={() => void confirm.action()}>
                确认
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
