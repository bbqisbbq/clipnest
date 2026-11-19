import { useCallback, useRef, useState } from "react";
import { useToast } from "./hooks/useToast";
import { useTheme } from "./hooks/useTheme";
import { useAppData } from "./hooks/useAppData";
import { ClipService, type Entry, type EntryFilter, type UpdateInfo } from "./lib/api";
import TitleBar from "./components/TitleBar";
import Sidebar from "./components/Sidebar";
import EntriesView from "./components/EntriesView";
import EntryDetailView from "./components/EntryDetailView";
import TodoView from "./components/TodoView";
import FilterDialog from "./components/FilterDialog";
import { useConfirm } from "./components/Dialog";
import SettingsView from "./components/SettingsView";

// App 只做三件事：装全局状态（hooks 里）、切换视图、转发通知。
// 具体内容全部在 EntriesView / EntryDetailView / SettingsView / Sidebar，这里不写业务。
//
// 三个视图里 detail 不是"另一个页面"：它盖在 entries 上面（EntriesView 保持挂载），
// 这样返回时列表的滚动位置、展开状态都还在。

type View = "entries" | "detail" | "todo" | "settings";
type Dialog = { filter: EntryFilter; isNew: boolean } | null;

const BLANK_FILTER: EntryFilter = {
  id: "",
  name: "新筛选器",
  icon: "memo",
  query: {} as EntryFilter["query"],
  sortOrder: 0,
  builtin: false,
  deletable: true,
} as EntryFilter;

const DEFAULT_FILTER: EntryFilter = {
  id: "all",
  name: "全部内容",
  icon: "all",
  query: {} as EntryFilter["query"],
  sortOrder: 0,
  builtin: true,
  deletable: false,
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
  // 详情是从哪儿点开的：决定"返回"回到列表还是回到待办
  const [detailFrom, setDetailFrom] = useState<"entries" | "todo">("entries");
  const detailDirty = useRef(false);
  const [activeFilterId, setActiveFilterId] = useState("all");
  const [selectedDay, setSelectedDay] = useState(todayKey);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  // 确认框走全局 DialogProvider（以前这里有第四套自己写的 modal 状态）
  const confirm = useConfirm();
  const [dialog, setDialog] = useState<Dialog>(null);

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

  const clearHistory = useCallback(async () => {
    if (!(await confirm({ message: "清空所有未置顶的内容？这个操作不可撤销。", danger: true, confirmLabel: "清空" }))) {
      return;
    }
    try {
      const n = await ClipService.DeleteAll();
      await data.refreshMeta();
      notify(`已清空 ${n} 条`);
    } catch (err) {
      notify(`清空失败：${String(err)}`);
    }
  }, [data, notify, confirm]);

  // 筛选器编辑：主题化模态（原来是 window.prompt —— 系统原生弹窗，和主题完全不搭）
  const editFilter = useCallback((f: EntryFilter) => {
    setDialog({ filter: f, isNew: false });
  }, []);

  const duplicateFilter = useCallback((f: EntryFilter) => {
    setDialog({
      filter: {
        ...f,
        id: "",
        name: `${f.name} 副本`,
        builtin: false,
        deletable: true,
        query: { ...(f.query ?? {}) } as EntryFilter["query"],
      },
      isNew: true,
    });
  }, []);

  const deleteFilter = useCallback(
    async (f: EntryFilter) => {
      if (!(await confirm({ message: `删除筛选器「${f.name}」？条目本身不受影响。`, danger: true, confirmLabel: "删除" }))) {
        return;
      }
      try {
        const ok = await ClipService.DeleteFilter(f.id);
        if (!ok) {
          notify("这个筛选器不能删除");
          return;
        }
        await data.refreshMeta();
        if (activeFilterId === f.id) setActiveFilterId("all");
        notify(`已删除筛选器「${f.name}」`);
      } catch (err) {
        notify(`删除失败：${String(err)}`);
      }
    },
    [activeFilterId, data, notify, confirm],
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

  // guardDirtyDetail 是"离开详情前的脏数据拦截"。原来 leaveDetail 与
  // onOpenSettings 各手写了一遍同样的 if（文案不同、逻辑必须一致），
  // 现在只有这一份判断。
  const guardDirtyDetail = useCallback(
    async (message: string) => {
      if (view !== "detail" || !detailDirty.current) return true;
      return await confirm({ message, danger: true, confirmLabel: "放弃修改" });
    },
    [view, confirm],
  );

  // 离开详情（详情自己的返回按钮也走同一个判断）
  const leaveDetail = useCallback(
    async (then?: () => void) => {
      if (!(await guardDirtyDetail("这条内容有未保存的修改，确定放弃并离开？"))) return;
      detailDirty.current = false;
      setDetailEntry(null);
      setView("entries");
      then?.();
    },
    [guardDirtyDetail],
  );

  // 打开详情：记住是从列表还是从待办进来的，返回时回到原地
  const openDetail = useCallback((entry: Entry, from: "entries" | "todo") => {
    setDetailEntry(entry);
    setDetailFrom(from);
    setView("detail");
  }, []);

  return (
    <div className="app is-entries">
      <TitleBar capturing={capturing} enabled={capturing} onToggleCapture={() => void toggleCapture()} />

      <div className="workspace">
        <Sidebar
          view={view === "settings" ? "settings" : view === "todo" ? "todo" : "entries"}
          capturing={capturing}
          syncActive={settings?.syncEnabled ?? false}
          filterProps={{
            filters,
            activeFilterId,
            onSelectFilter: (f) => {
              setActiveFilterId(f.id);
              setSelectedDay("");
              void leaveDetail();
            },
            selectedDay,
            onPickDay: (d) => {
              setSelectedDay(d);
              void leaveDetail();
            },
            onEditFilter: editFilter,
            entryStats,
          }}
          onNewFilter={() => setDialog({ filter: BLANK_FILTER, isNew: true })}
          onDuplicateFilter={duplicateFilter}
          onDeleteFilter={deleteFilter}
          onOpenTodos={() => {
            if (view === "todo") return;
            void leaveDetail(() => setView("todo"));
          }}
          onOpenSettings={() => {
            void (async () => {
              if (!(await guardDirtyDetail("这条内容有未保存的修改，确定放弃并去设置？"))) return;
              detailDirty.current = false;
              setDetailEntry(null);
              setView("settings");
            })();
          }}
          onToggleCapture={() => void toggleCapture()}
        />

        <div className="main-pane">
          {/* 详情是"盖在上面"而不是替换：EntriesView 常驻，返回时列表位置还在 */}
          {view === "entries" || (view === "detail" && detailFrom === "entries") ? (
            <EntriesView
              filter={activeFilter}
              day={selectedDay}
              onNotify={notify}
              onChanged={() => void data.refreshMeta()}
              onOpenDetail={(e) => openDetail(e, "entries")}
            />
          ) : null}

          {view === "todo" ? (
            <TodoView
              onNotify={notify}
              onChanged={() => void data.refreshMeta()}
              onOpenEntry={(e) => openDetail(e, "todo")}
              onGoEntries={() => setView("entries")}
            />
          ) : null}

          {view === "detail" && detailEntry ? (
            <EntryDetailView
              entry={detailEntry}
              backLabel={detailFrom === "todo" ? "待办" : activeFilter.name}
              onBack={() => {
                detailDirty.current = false;
                setDetailEntry(null);
                setView(detailFrom);
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
                setView(detailFrom);
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

      {dialog ? (
        <FilterDialog
          filter={dialog.filter}
          isNew={dialog.isNew}
          onClose={() => setDialog(null)}
          onNotify={notify}
          onSaved={(saved) => {
            setDialog(null);
            void data.refreshMeta().then(() => {
              // 新筛选器建好就直接选它，省一次点击
              if (dialog.isNew && saved?.id) {
                setActiveFilterId(saved.id);
                setSelectedDay("");
                setView("entries");
              }
            });
            notify(dialog.isNew ? `已创建筛选器「${saved?.name ?? ""}」` : "筛选器已保存");
          }}
          onDeleted={(id) => {
            setDialog(null);
            if (activeFilterId === id) setActiveFilterId("all");
            void data.refreshMeta();
          }}
        />
      ) : null}


    </div>
  );
}
