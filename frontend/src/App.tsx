import { useCallback, useRef, useState } from "react";
import { useToast } from "./hooks/useToast";
import { useTheme } from "./hooks/useTheme";
import { useAppData } from "./hooks/useAppData";
import { ClipService, type Entry, type EntryFilter, type UpdateInfo } from "./lib/api";
import TitleBar from "./components/TitleBar";
import Sidebar from "./components/Sidebar";
import EntriesView from "./components/EntriesView";
import EntryDetailView from "./components/EntryDetailView";
import SettingsView from "./components/SettingsView";

// App 只做三件事：装全局状态（hooks 里）、切换视图、转发通知。
// 具体内容全部在 EntriesView / EntryDetailView / SettingsView / Sidebar，这里不写业务。
//
// 三个视图里 detail 不是"另一个页面"：它盖在 entries 上面（EntriesView 保持挂载），
// 这样返回时列表的滚动位置、展开状态都还在。

type View = "entries" | "detail" | "settings";
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
  const { toast, notify } = useToast();
  const data = useAppData(notify);
  const { settings, appInfo, syncStatus, stats, filters, entryStats } = data;

  useTheme(settings);

  const [view, setView] = useState<View>("entries");
  const [detailEntry, setDetailEntry] = useState<Entry | null>(null);
  const detailDirty = useRef(false);
  const [activeFilterId, setActiveFilterId] = useState("all");
  const [selectedDay, setSelectedDay] = useState(todayKey);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [confirm, setConfirm] = useState<Confirm>(null);

  const activeFilter = filters.find((f) => f.id === activeFilterId) ?? filters[0] ?? DEFAULT_FILTER;
  const capturing = settings?.capture ?? true;

  const toggleCapture = useCallback(async () => {
    try {
      const current = settings ?? (await ClipService.GetSettings());
      data.setSettings(await ClipService.SetCapture(!current.capture));
    } catch (err) {
      notify(`切换失败：${String(err)}`);
    }
  }, [settings, data, notify]);

  const clearHistory = useCallback(() => {
    setConfirm({
      message: "清空所有未置顶的内容？这个操作不可撤销。",
      action: async () => {
        try {
          const n = await ClipService.DeleteAll();
          await data.refreshMeta();
          notify(`已清空 ${n} 条`);
        } catch (err) {
          notify(`清空失败：${String(err)}`);
        }
      },
    });
  }, [data, notify]);

  const editFilter = useCallback(
    (f: EntryFilter) => {
      const name = window.prompt("筛选器名称：", f.name);
      if (name === null) return;
      const icon = window.prompt(
        "图标（all/pin/snippet/memo/star/folder/files/image/text/link/search）：",
        f.icon || "memo",
      );
      void ClipService.SaveFilter({ ...f, name: name.trim() || f.name, icon: (icon || f.icon).trim() })
        .then(() => void data.refreshMeta())
        .catch((err) => notify(`保存筛选器失败：${String(err)}`));
    },
    [data, notify],
  );

  const runSync = useCallback(async () => {
    try {
      await ClipService.SyncNow();
      await data.refreshAll();
      notify("同步完成");
    } catch (err) {
      notify(`同步失败：${String(err)}`);
    }
  }, [data, notify]);

  const checkUpdate = useCallback(async () => {
    const info = await ClipService.CheckUpdate();
    setUpdateInfo(info);
    return info;
  }, []);

  const testSync = useCallback(async () => {
    const r = await ClipService.TestSync();
    return typeof r === "string" ? r : JSON.stringify(r);
  }, []);

  // 离开详情：有未保存修改就先问一句（详情自己的返回按钮也走同一个判断）
  const leaveDetail = useCallback((then?: () => void) => {
    const finish = () => {
      detailDirty.current = false;
      setDetailEntry(null);
      setView("entries");
      then?.();
    };
    if (view === "detail" && detailDirty.current) {
      setConfirm({
        message: "这条内容有未保存的修改，确定放弃并离开？",
        action: finish,
      });
      return;
    }
    finish();
  }, [view]);

  return (
    <div className="app is-entries">
      <TitleBar capturing={capturing} enabled={capturing} onToggleCapture={() => void toggleCapture()} />

      <div className="workspace">
        <Sidebar
          view={view === "settings" ? "settings" : "entries"}
          capturing={capturing}
          syncActive={settings?.syncEnabled ?? false}
          filterProps={{
            filters,
            activeFilterId,
            onSelectFilter: (f) => {
              setActiveFilterId(f.id);
              setSelectedDay("");
              leaveDetail();
            },
            selectedDay,
            onPickDay: (d) => {
              setSelectedDay(d);
              leaveDetail();
            },
            onEditFilter: editFilter,
            entryStats,
          }}
          onOpenSettings={() => {
            if (view === "detail" && detailDirty.current) {
              setConfirm({
                message: "这条内容有未保存的修改，确定放弃并去设置？",
                action: () => {
                  detailDirty.current = false;
                  setDetailEntry(null);
                  setView("settings");
                },
              });
              return;
            }
            setDetailEntry(null);
            setView("settings");
          }}
          onToggleCapture={() => void toggleCapture()}
        />

        <div className="main-pane">
          {/* 详情是"盖在上面"而不是替换：EntriesView 常驻，返回时列表位置还在 */}
          {view !== "settings" ? (
            <EntriesView
              filter={activeFilter}
              day={selectedDay}
              onNotify={notify}
              onChanged={() => void data.refreshMeta()}
              onOpenDetail={(e) => {
                setDetailEntry(e);
                setView("detail");
              }}
            />
          ) : null}

          {view === "detail" && detailEntry ? (
            <EntryDetailView
              entry={detailEntry}
              backLabel={activeFilter.name}
              onBack={() => {
                detailDirty.current = false;
                setDetailEntry(null);
                setView("entries");
              }}
              onDirtyChange={(d) => {
                detailDirty.current = d;
              }}
              onSaved={(next) => {
                setDetailEntry(next);
                void data.refreshMeta();
              }}
              onDeleted={() => {
                detailDirty.current = false;
                setDetailEntry(null);
                setView("entries");
                void data.refreshMeta();
              }}
              onNotify={notify}
            />
          ) : null}

          {view === "settings" ? (
            <SettingsView
              settings={settings}
              appInfo={appInfo}
              storageBytes={stats?.storageBytes ?? 0}
              syncStatus={syncStatus}
              onSave={(next) => void data.saveSettings(next)}
              onClearHistory={clearHistory}
              onOpenDataDir={() => void ClipService.OpenDataDir()}
              onQuit={() => void ClipService.Quit()}
              updateInfo={updateInfo}
              onCheckUpdate={checkUpdate}
              onInstallUpdate={() => void ClipService.InstallUpdate()}
              onOpenRelease={() => void ClipService.OpenReleasePage()}
              onTestSync={testSync}
              onSyncNow={() => void runSync()}
            />
          ) : null}
        </div>
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
