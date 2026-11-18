import { useCallback, useEffect, useRef, useState } from "react";
import { THEME_PRESETS, applyCustomCSS, applyTheme as applyThemePreset } from "./lib/themes";
import {
  ClipService,
  type AppInfo,
  type EntryFilter,
  type EntryStats,
  type Settings,
  type Stats,
  type SyncStatus,
  type UpdateInfo,
} from "./lib/api";
import TitleBar from "./components/TitleBar";
import Sidebar from "./components/Sidebar";
import EntriesView from "./components/EntriesView";
import SettingsView from "./components/SettingsView";

// App 只做三件事：装载全局状态（设置/主题/统计）、切换视图、转发通知。
// 具体内容全部在 EntriesView / SettingsView / Sidebar 里，避免这里再长出业务逻辑。

type View = "entries" | "settings";
type Confirm = { message: string; action: () => void } | null;

const DEFAULT_FILTER: EntryFilter = {
  id: "all",
  name: "全部内容",
  icon: "all",
  query: {} as EntryFilter["query"],
  sortOrder: 0,
  builtin: true,
};

function todayKey(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export default function App() {
  const [view, setView] = useState<View>("entries");
  const [settings, setSettings] = useState<Settings | null>(null);
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [syncStatus, setSyncStatus] = useState<SyncStatus | null>(null);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);

  const [filters, setFilters] = useState<EntryFilter[]>([]);
  const [activeFilterId, setActiveFilterId] = useState("all");
  const [selectedDay, setSelectedDay] = useState(todayKey);
  const [entryStats, setEntryStats] = useState<EntryStats | null>(null);

  const [toast, setToast] = useState("");
  const [confirm, setConfirm] = useState<Confirm>(null);
  const toastTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const notify = useCallback((message: string) => {
    setToast(message);
    if (toastTimer.current) clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(""), 2600);
  }, []);

  // ---- 主题 --------------------------------------------------------------
  const themeScheme = (settings?.theme ?? "system") as "system" | "light" | "dark";
  const themePreset = settings?.themePreset ?? "default-dark";
  const themeCss = settings?.themeCustomCss ?? "";

  useEffect(() => {
    applyThemePreset(themeScheme, themePreset);
    applyCustomCSS(themeCss);
    localStorage.setItem("clipnest.theme", themeScheme);
    localStorage.setItem("clipnest.preset", themePreset);
    // 原生窗口边框/标题区也跟着换，否则浅色主题下会残留系统深色边
    const preset = THEME_PRESETS.find((p) => p.id === themePreset) ?? THEME_PRESETS[0];
    void ClipService.SetWindowChrome(preset.scheme === "dark", preset.tokens["--bg"] ?? "").catch(() => {});
    const mq = window.matchMedia?.("(prefers-color-scheme: light)");
    const onChange = () => {
      if (themeScheme === "system") applyThemePreset(themeScheme, themePreset);
    };
    mq?.addEventListener("change", onChange);
    return () => mq?.removeEventListener("change", onChange);
  }, [themeScheme, themePreset, themeCss]);

  // ---- 数据 --------------------------------------------------------------
  const refreshMeta = useCallback(async () => {
    try {
      const [f, st, s] = await Promise.all([
        ClipService.GetFilters(),
        ClipService.GetEntryStats(),
        ClipService.GetStats(),
      ]);
      setFilters(f ?? []);
      setEntryStats(st);
      setStats(s);
    } catch (err) {
      console.error(err);
    }
  }, []);

  useEffect(() => {
    void (async () => {
      try {
        setSettings(await ClipService.GetSettings());
        setAppInfo(await ClipService.GetAppInfo());
        setSyncStatus(await ClipService.GetSyncStatus());
      } catch (err) {
        console.error(err);
      }
      await refreshMeta();
    })();
  }, [refreshMeta]);

  const activeFilter = filters.find((f) => f.id === activeFilterId) ?? filters[0] ?? DEFAULT_FILTER;

  const saveSettings = useCallback(
    async (next: Settings) => {
      try {
        setSettings(await ClipService.SaveSettings(next));
        setSyncStatus(await ClipService.GetSyncStatus());
        notify("设置已保存");
      } catch (err) {
        notify(`保存失败：${String(err)}`);
      }
    },
    [notify],
  );

  const toggleCapture = useCallback(async () => {
    try {
      const st = settings ?? (await ClipService.GetSettings());
      setSettings(await ClipService.SetCapture(!st.capture));
    } catch (err) {
      notify(`切换失败：${String(err)}`);
    }
  }, [settings, notify]);

  const clearHistory = useCallback(() => {
    setConfirm({
      message: "清空所有未置顶的内容？这个操作不可撤销。",
      action: async () => {
        try {
          const n = await ClipService.DeleteAll();
          await refreshMeta();
          notify(`已清空 ${n} 条`);
        } catch (err) {
          notify(`清空失败：${String(err)}`);
        }
      },
    });
  }, [refreshMeta, notify]);

  const runSync = useCallback(async () => {
    try {
      setSyncStatus(await ClipService.SyncNow());
      await refreshMeta();
      notify("同步完成");
    } catch (err) {
      notify(`同步失败：${String(err)}`);
    }
  }, [refreshMeta, notify]);

  const testSync = useCallback(async () => {
    const r = await ClipService.TestSync();
    return typeof r === "string" ? r : JSON.stringify(r);
  }, []);

  const checkUpdate = useCallback(async () => {
    const info = await ClipService.CheckUpdate();
    setUpdateInfo(info);
    return info;
  }, []);

  const installUpdate = useCallback(() => {
    void ClipService.InstallUpdate();
  }, []);

  const capturing = settings?.capture ?? true;

  return (
    <div className="app is-entries">
      <TitleBar capturing={capturing} enabled={capturing} onToggleCapture={() => void toggleCapture()} />

      <div className="workspace">
        <Sidebar
          view={view}
          capturing={capturing}
          syncActive={settings?.syncEnabled ?? false}
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
                .then(() => void refreshMeta())
                .catch((err) => notify(`保存筛选器失败：${String(err)}`));
            },
            entryStats,
          }}
          onOpenSettings={() => setView("settings")}
          onToggleCapture={() => void toggleCapture()}
        />

        {view === "entries" ? (
          <EntriesView
            filter={activeFilter}
            day={selectedDay}
            onNotify={notify}
            onChanged={() => void refreshMeta()}
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
              <button
                className="btn btn-danger"
                onClick={() => {
                  const action = confirm.action;
                  setConfirm(null);
                  void action();
                }}
              >
                确认
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
