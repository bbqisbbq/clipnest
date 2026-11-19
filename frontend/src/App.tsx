import { useCallback, useEffect, useRef, useState } from "react";
import { useToast } from "./hooks/useToast";
import { useTheme } from "./hooks/useTheme";
import { useAppData } from "./hooks/useAppData";
import { useComposerDraft } from "./hooks/useComposerDraft";
import { useFilters } from "./hooks/useFilters";
import { today } from "./lib/dates";
import { ClipService, type Entry, type EntryFilter, type UpdateInfo } from "./lib/api";
import TitleBar from "./components/TitleBar";
import Sidebar from "./components/Sidebar";
import EntriesView from "./components/EntriesView";
import EntryDetailView from "./components/EntryDetailView";
import TodoView from "./components/TodoView";
import ComposerDock from "./components/ComposerDock";
import FilterDialog from "./components/FilterDialog";
import { useConfirm } from "./components/Dialog";
import SettingsView from "./components/SettingsView";
import { DEFAULT_FILTER } from "./hooks/useFilters";

// App 只做三件事：装全局状态（hooks 里）、切换视图、转发通知。
// 具体内容全部在 EntriesView / EntryDetailView / SettingsView / Sidebar，这里不写业务。
//
// 布局分两格：上面是"当前视图"（.pane-body），下面是全局常驻的快捷输入框
// （ComposerDock）。输入框在侧栏的每一屏都在同一个位置——切筛选器、去待办、
// 进设置都不该让它消失，更不该清空里面的字。
//
// detail 不是"另一个页面"：它盖在 entries 上面（EntriesView 保持挂载），
// 这样返回时列表的滚动位置、展开状态都还在。

type View = "entries" | "detail" | "todo" | "settings";

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
  const [selectedDay, setSelectedDay] = useState(today);
  // 进设置页之前停在哪个视图：关掉设置回到原地（筛选器与日期本就没被清掉）
  const [settingsReturn, setSettingsReturn] = useState<"entries" | "todo">("entries");
  // 输入框存下一条后给时间线的一次性信号（时间线此刻可能没挂载，见 EntriesView）
  const [savedTick, setSavedTick] = useState(0);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  // 确认框走全局 DialogProvider（以前这里有第四套自己写的 modal 状态）
  const confirm = useConfirm();

  const activeFilter = filters.find((f) => f.id === activeFilterId) ?? filters[0] ?? DEFAULT_FILTER;
  const capturing = settings?.capture ?? true;
  const draft = useComposerDraft(notify);

  const resetActiveFilter = useCallback((id: string) => {
    setActiveFilterId((cur) => (cur === id ? "all" : cur));
    // 默认筛选器被删了就别再指着它，否则下次启动会落到一个不存在的 id 上
    if (settings?.defaultFilterId === id) {
      void data.saveSettings({ ...settings, defaultFilterId: "" });
    }
  }, [settings, data]);

  const filterForms = useFilters({
    notify,
    confirm,
    refreshMeta: data.refreshMeta,
    onActiveFilterDeleted: resetActiveFilter,
  });

  // ---- 导航 --------------------------------------------------------------

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

  const openSettings = useCallback(() => {
    void (async () => {
      if (!(await guardDirtyDetail("这条内容有未保存的修改，确定放弃并去设置？"))) return;
      detailDirty.current = false;
      setDetailEntry(null);
      setSettingsReturn(view === "todo" ? "todo" : "entries");
      setView("settings");
    })();
  }, [guardDirtyDetail, view]);

  // 关设置 = 回到进来之前那一屏（点侧栏"设置"、点页面上的返回、按 Esc 都走这里）
  const closeSettings = useCallback(() => setView(settingsReturn), [settingsReturn]);

  const toggleSettings = useCallback(() => {
    if (view === "settings") closeSettings();
    else openSettings();
  }, [view, closeSettings, openSettings]);

  const openTodos = useCallback(() => {
    if (view === "todo") {
      setView("entries");
      return;
    }
    void leaveDetail(() => setView("todo"));
  }, [view, leaveDetail]);

  // ---- 默认筛选器 --------------------------------------------------------

  // 启动时落到设置里的默认筛选器上。只在设置第一次到位时认一次，
  // 之后用户自己点来点去不该被拽回去。
  const defaultApplied = useRef(false);
  useEffect(() => {
    if (defaultApplied.current || !settings) return;
    if (filters.length === 0) return; // 筛选器还没拉回来，等下一轮
    defaultApplied.current = true;
    const want = settings.defaultFilterId;
    if (want && filters.some((f) => f.id === want)) setActiveFilterId(want);
  }, [settings, filters]);

  const setDefaultFilter = useCallback(
    async (f: EntryFilter) => {
      if (!settings) return;
      const nextId = settings.defaultFilterId === f.id ? "" : f.id;
      if (!(await data.saveSettings({ ...settings, defaultFilterId: nextId }))) return;
      notify(nextId ? `已设为默认打开：${f.name}` : "已取消默认筛选器");
    },
    [settings, data, notify],
  );

  // ---- 全局动作 ----------------------------------------------------------

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

  // 输入框存下一条：刷新侧栏计数，并让时间线重载一次（贴到底部）
  const onComposerSaved = useCallback(() => {
    setSavedTick((n) => n + 1);
    void data.refreshMeta();
  }, [data]);

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
            defaultFilterId: settings?.defaultFilterId ?? "",
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
            onEditFilter: filterForms.startEdit,
            onSetDefaultFilter: (f) => void setDefaultFilter(f),
            entryStats,
          }}
          onNewFilter={filterForms.startNew}
          onDuplicateFilter={filterForms.startDuplicate}
          onDeleteFilter={(f) => void filterForms.remove(f)}
          onOpenTodos={openTodos}
          onOpenSettings={toggleSettings}
          onToggleCapture={() => void toggleCapture()}
        />

        <div className="main-pane">
          <div className="pane-body">
            {/* 详情是"盖在上面"而不是替换：EntriesView 常驻，返回时列表位置还在 */}
            {view === "entries" || (view === "detail" && detailFrom === "entries") ? (
              <EntriesView
                filter={activeFilter}
                day={selectedDay}
                draftText={draft.text}
                reloadSignal={savedTick}
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
                onSave={data.saveSettings}
                onClose={closeSettings}
                backLabel={settingsReturn === "todo" ? "待办" : activeFilter.name}
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

          {/* 详情是"在改一条已有的"，不需要（也不该抢地方）底部输入框 */}
          {view !== "detail" ? (
            <ComposerDock draft={draft} onSaved={onComposerSaved} onNotify={notify} />
          ) : null}
        </div>
      </div>

      {toast ? <div className="toast">{toast}</div> : null}

      {filterForms.dialog ? (
        <FilterDialog
          filter={filterForms.dialog.filter}
          isNew={filterForms.dialog.isNew}
          onClose={filterForms.closeDialog}
          onNotify={notify}
          onSaved={(saved) => {
            const isNew = filterForms.dialog?.isNew ?? false;
            filterForms.closeDialog();
            void data.refreshMeta().then(() => {
              // 新筛选器建好就直接选它，省一次点击
              if (isNew && saved?.id) {
                setActiveFilterId(saved.id);
                setSelectedDay("");
                setView("entries");
              }
            });
            notify(isNew ? `已创建筛选器「${saved?.name ?? ""}」` : "筛选器已保存");
          }}
          onDeleted={(id) => {
            filterForms.closeDialog();
            resetActiveFilter(id);
            void data.refreshMeta();
          }}
        />
      ) : null}
    </div>
  );
}
