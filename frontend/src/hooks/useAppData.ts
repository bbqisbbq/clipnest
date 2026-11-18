import { useCallback, useEffect, useState } from "react";
import {
  ClipService,
  type AppInfo,
  type EntryFilter,
  type EntryStats,
  type Settings,
  type Stats,
  type SyncStatus,
} from "../lib/api";

// 全局数据：设置、应用信息、同步状态、筛选器、条目统计。
// 这些是"整个窗口共用"的状态，集中在这里，组件只订阅需要的部分。

export type AppData = {
  settings: Settings | null;
  appInfo: AppInfo | null;
  syncStatus: SyncStatus | null;
  stats: Stats | null;
  filters: EntryFilter[];
  entryStats: EntryStats | null;
  refreshMeta: () => Promise<void>;
  refreshAll: () => Promise<void>;
  saveSettings: (next: Settings) => Promise<boolean>;
  setSettings: (next: Settings) => void;
};

export function useAppData(onError: (msg: string) => void): AppData {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [syncStatus, setSyncStatus] = useState<SyncStatus | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const [filters, setFilters] = useState<EntryFilter[]>([]);
  const [entryStats, setEntryStats] = useState<EntryStats | null>(null);

  // 只刷新侧栏要用的轻量数据（筛选器、计数、占用）
  const refreshMeta = useCallback(async () => {
    try {
      const [f, es, s] = await Promise.all([
        ClipService.GetFilters(),
        ClipService.GetEntryStats(),
        ClipService.GetStats(),
      ]);
      setFilters(f ?? []);
      setEntryStats(es);
      setStats(s);
    } catch (err) {
      console.error(err);
    }
  }, []);

  const refreshAll = useCallback(async () => {
    try {
      const [st, info, sync] = await Promise.all([
        ClipService.GetSettings(),
        ClipService.GetAppInfo(),
        ClipService.GetSyncStatus(),
      ]);
      setSettings(st);
      setAppInfo(info);
      setSyncStatus(sync);
    } catch (err) {
      console.error(err);
    }
    await refreshMeta();
  }, [refreshMeta]);

  useEffect(() => {
    void refreshAll();
  }, [refreshAll]);

  const saveSettings = useCallback(
    async (next: Settings) => {
      try {
        setSettings(await ClipService.SaveSettings(next));
        setSyncStatus(await ClipService.GetSyncStatus());
        return true;
      } catch (err) {
        onError(`保存失败：${String(err)}`);
        return false;
      }
    },
    [onError],
  );

  return {
    settings,
    appInfo,
    syncStatus,
    stats,
    filters,
    entryStats,
    refreshMeta,
    refreshAll,
    saveSettings,
    setSettings,
  };
}
