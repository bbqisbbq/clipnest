import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AppInfo, Settings, SyncStatus, UpdateInfo } from "../lib/api";
import CaptureSection from "./settings/CaptureSection";
import OcrSection from "./settings/OcrSection";
import SearchSection from "./settings/SearchSection";
import InteractionSection from "./settings/InteractionSection";
import SyncSection from "./settings/SyncSection";
import SnippetsSection from "./settings/SnippetsSection";
import AppearanceSection from "./settings/AppearanceSection";
import EditorSection from "./settings/EditorSection";
import UpdateSection from "./settings/UpdateSection";
import AboutSection from "./settings/AboutSection";

// 设置页的编排者：草稿、自动保存、顶部状态条。
// 八个分区各自是 ./settings/ 下的组件，状态与渲染都搬过去；
// 这里不写任何分区的具体字段，免得又长回一个大文件。
//
// 保存策略（2.8.0 起）：**没有保存按钮**。改一下就落盘，停手 400ms 写一次，
// 离开设置页前补一次。只记"这次改过哪些字段"（touched），保存时把这些字段
// 合并到最新的服务端设置上——这样同步线程拉回来的对端设置、或者侧栏刚设的
// 默认筛选器不会被一份过期的整对象覆盖掉。

/** 停手多久落盘：比输入框草稿长一点，避免拖动开关时连续写。 */
const SAVE_DELAY_MS = 400;

type Props = {
  settings: Settings | null;
  appInfo: AppInfo | null;
  storageBytes: number;
  syncStatus: SyncStatus | null;
  /** 落盘（App 里是 useAppData 的 saveSettings），返回是否成功 */
  onSave: (next: Settings) => Promise<boolean>;
  /** 关掉设置页，回到进来之前那一屏 */
  onClose: () => void;
  /** 返回按钮上写哪一屏（"全部内容" / "待办"…） */
  backLabel: string;
  onClearHistory: () => void;
  onOpenDataDir: () => void;
  onQuit: () => void;
  onTestSync: () => Promise<string>;
  onSyncNow: () => void;
  updateInfo: UpdateInfo | null;
  onCheckUpdate: () => Promise<UpdateInfo>;
  onInstallUpdate: () => void;
  onOpenRelease: () => void;
};

type Status = { kind: "idle" | "saving" | "saved" | "error"; text?: string };

function clockLabel(): string {
  const d = new Date();
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

export default function SettingsView(props: Props) {
  const { settings, appInfo, storageBytes, syncStatus, onSave } = props;

  const [draft, setDraft] = useState<Settings | null>(settings);
  const [status, setStatus] = useState<Status>({ kind: "idle" });
  // 这次编辑动过哪些字段（只合并这些，别的字段以服务端最新值为准）
  const touched = useRef<Partial<Settings>>({});
  const timer = useRef<number | null>(null);
  const saving = useRef(false);
  // 保存要读"当前服务端设置"，用 ref 拿最新的一份（避免闭包读到旧值）
  const settingsRef = useRef<Settings | null>(settings);
  settingsRef.current = settings;

  // 外部改了设置（侧栏把某个筛选器设成默认、同步拉回了对端设置）：
  // 手上没有待保存的编辑才整体采纳，否则别打断用户。
  useEffect(() => {
    if (!settings) return;
    if (saving.current || Object.keys(touched.current).length > 0) return;
    setDraft(settings);
  }, [settings]);

  const retry = useRef(0);

  const flush = useCallback(async () => {
    if (timer.current) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
    // 上一笔还在飞：等它落地再发，否则后发的那笔会用一份旧快照把字段弹回去
    // （400ms 内连改两个开关时，第一个改动会静默丢失）。
    if (saving.current) {
      timer.current = window.setTimeout(() => void flush(), SAVE_DELAY_MS);
      return;
    }
    const pending = touched.current;
    if (Object.keys(pending).length === 0) return;
    touched.current = {};
    saving.current = true;
    setStatus({ kind: "saving" });
    const ok = await onSave({ ...(settingsRef.current ?? ({} as Settings)), ...pending });
    saving.current = false;
    if (!ok) {
      // 失败：改动放回队列，并真的重试几次（以前状态条写着"稍后会自动重试"，
      // 实际要到用户再动一个字段才会重发）。
      touched.current = { ...pending, ...touched.current };
      if (retry.current < 3) {
        retry.current += 1;
        setStatus({ kind: "error", text: `保存失败，正在重试（${retry.current}/3）` });
        timer.current = window.setTimeout(() => void flush(), 800 * retry.current);
      } else {
        setStatus({ kind: "error", text: "保存失败：改任意一项再试一次" });
      }
      return;
    }
    retry.current = 0;
    setStatus({ kind: "saved", text: `已自动保存 ${clockLabel()}` });
  }, [onSave]);

  const patch = useCallback(
    (p: Partial<Settings>) => {
      touched.current = { ...touched.current, ...p };
      setDraft((prev) => (prev ? { ...prev, ...p } : prev));
      if (timer.current) window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => void flush(), SAVE_DELAY_MS);
    },
    [flush],
  );

  // 离开设置页（切视图 / 关窗口）之前，把还在防抖窗口里的改动补上
  useEffect(
    () => () => {
      if (timer.current) window.clearTimeout(timer.current);
      timer.current = null;
      if (Object.keys(touched.current).length > 0) void flush();
    },
    [flush],
  );

  const statusText = useMemo(() => {
    switch (status.kind) {
      case "saving":
        return "保存中…";
      case "saved":
        return status.text ?? "已自动保存";
      case "error":
        return status.text ?? "保存失败";
      default:
        return "改动会自动保存";
    }
  }, [status]);

  if (!draft) {
    return (
      <main className="main is-settings">
        <p className="muted">正在加载设置…</p>
      </main>
    );
  }

  return (
    <main className="main is-settings">
      <header className="settings-head">
        <button className="btn btn-ghost btn-sm settings-back" onClick={props.onClose} title="关掉设置，回到刚才那一屏">
          ← {props.backLabel}
        </button>
        <h2 className="settings-title">设置</h2>
        <span className={`settings-state is-${status.kind}`}>{statusText}</span>
      </header>

      <div className="settings">
        <div className="settings-inner">
          <CaptureSection draft={draft} patch={patch} />
          <OcrSection draft={draft} patch={patch} />
          <SearchSection draft={draft} patch={patch} />

          <InteractionSection draft={draft} patch={patch} appInfo={appInfo} />

          <SyncSection
            draft={draft}
            patch={patch}
            settings={settings}
            syncStatus={syncStatus}
            onTestSync={props.onTestSync}
            onSyncNow={props.onSyncNow}
          />

          <SnippetsSection draft={draft} patch={patch} />

          <AppearanceSection draft={draft} patch={patch} />

          <EditorSection draft={draft} patch={patch} />

          <UpdateSection
            draft={draft}
            patch={patch}
            updateInfo={props.updateInfo}
            onCheckUpdate={props.onCheckUpdate}
            onInstallUpdate={props.onInstallUpdate}
            onOpenRelease={props.onOpenRelease}
          />

          <AboutSection
            appInfo={appInfo}
            storageBytes={storageBytes}
            onClearHistory={props.onClearHistory}
            onOpenDataDir={props.onOpenDataDir}
            onQuit={props.onQuit}
          />
        </div>
      </div>
    </main>
  );
}
