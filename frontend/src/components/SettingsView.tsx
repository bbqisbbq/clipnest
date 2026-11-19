import { useEffect, useMemo, useRef, useState } from "react";
import type { AppInfo, Settings, SyncStatus, UpdateInfo } from "../lib/api";
import CaptureSection from "./settings/CaptureSection";
import InteractionSection from "./settings/InteractionSection";
import SyncSection from "./settings/SyncSection";
import SnippetsSection from "./settings/SnippetsSection";
import AppearanceSection from "./settings/AppearanceSection";
import EditorSection from "./settings/EditorSection";
import UpdateSection from "./settings/UpdateSection";
import AboutSection from "./settings/AboutSection";

// 设置页的编排者：只负责草稿（draft）、脏标记、"保存/撤销"与底部保存条。
// 七个分区各自是 ./settings/ 下的组件，状态与渲染都搬过去；
// 这里不写任何分区的具体字段，免得又长回一个大文件。

type Props = {
  settings: Settings | null;
  appInfo: AppInfo | null;
  storageBytes: number;
  syncStatus: SyncStatus | null;
  onSave: (next: Settings) => void;
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

export default function SettingsView(props: Props) {
  const { settings, appInfo, storageBytes, syncStatus } = props;

  const [draft, setDraft] = useState<Settings | null>(settings);
  const [savedFlash, setSavedFlash] = useState(false);
  // 撤销时用来通知各分区清掉自己的临时提示（同步的「测试结果」）
  const [resetSignal, setResetSignal] = useState(0);
  const flashTimer = useRef<number | null>(null);

  useEffect(() => {
    setDraft(settings);
  }, [settings]);

  const dirty = useMemo(() => {
    if (!draft || !settings) return false;
    return JSON.stringify(draft) !== JSON.stringify(settings);
  }, [draft, settings]);

  useEffect(() => () => { if (flashTimer.current) window.clearTimeout(flashTimer.current); }, []);

  if (!draft) {
    return (
      <main className="main is-settings">
        <p className="muted">正在加载设置…</p>
      </main>
    );
  }

  const patch = (p: Partial<Settings>) => setDraft({ ...draft, ...p });

  const showBar = dirty || savedFlash;

  const save = () => {
    const next = { ...draft };
    props.onSave(next);
    // 保存成功的提示只闪现 2 秒，然后状态栏自动收起，不长期占着屏幕
    setSavedFlash(true);
    if (flashTimer.current) window.clearTimeout(flashTimer.current);
    flashTimer.current = window.setTimeout(() => setSavedFlash(false), 2000);
  };

  const revert = () => {
    setDraft(settings);
    setResetSignal((n) => n + 1);
  };

  return (
    <main className="main is-settings">
      <div className="settings">
        <div className="settings-inner">
          <CaptureSection draft={draft} patch={patch} />

          <InteractionSection draft={draft} patch={patch} appInfo={appInfo} />

          <SyncSection
            draft={draft}
            patch={patch}
            settings={settings}
            syncStatus={syncStatus}
            onTestSync={props.onTestSync}
            onSyncNow={props.onSyncNow}
            resetSignal={resetSignal}
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

      <footer
        className={`settings-bar ${showBar ? "is-shown" : "is-hidden"}`}
        /* 隐藏时用 inert：光 aria-hidden 只是让读屏忽略，里面的按钮还在 Tab 序里。
           React 18 的类型里还没有 inert，所以用属性展开（DOM 本身支持）。 */
        {...(!showBar ? { inert: "" } : {})}
      >
        <span className={dirty ? "is-dirty" : "is-saved"}>
          {dirty ? "有未保存的修改" : "设置已保存"}
        </span>
        <div className="settings-bar-actions">
          <button className="btn btn-ghost" onClick={revert} disabled={!dirty}>
            撤销
          </button>
          <button className="btn btn-primary" onClick={save} disabled={!dirty}>
            保存设置
          </button>
        </div>
      </footer>
    </main>
  );
}
