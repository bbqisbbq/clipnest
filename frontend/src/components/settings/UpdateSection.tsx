import { useState } from "react";
import type { Settings, UpdateInfo } from "../../lib/api";
import { Row, Section, Switch, type SettingsPatch } from "./ui";

// 「更新」分区：自动检查开关、手动检查 / 安装 / 打开发布页。
// checking 是本分区专有的进行中状态；updateInfo 由 App 传入（全局，顶部提示条也要用）。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
  updateInfo: UpdateInfo | null;
  onCheckUpdate: () => Promise<UpdateInfo>;
  onInstallUpdate: () => void;
  onOpenRelease: () => void;
};

export default function UpdateSection({
  draft,
  patch,
  updateInfo,
  onCheckUpdate,
  onInstallUpdate,
  onOpenRelease,
}: Props) {
  const [checking, setChecking] = useState(false);

  const runCheckUpdate = async () => {
    setChecking(true);
    try {
      await onCheckUpdate();
    } finally {
      setChecking(false);
    }
  };

  return (
    <Section title="更新" desc="从 GitHub Releases 检查新版本，下载后静默安装并重启。">
      <Row label="自动检查更新" tip="启动 8 秒后检查一次 GitHub 上的最新发布。发现新版本会在窗口顶部提示。">
        <Switch checked={draft.updateCheck} onChange={(v) => patch({ updateCheck: v })} />
      </Row>
      <div className="sync-actions">
        <button className="btn btn-ghost" onClick={runCheckUpdate} disabled={checking}>
          {checking ? "检查中…" : "检查更新"}
        </button>
        <button
          className="btn btn-primary"
          onClick={onInstallUpdate}
          disabled={!updateInfo?.available}
        >
          下载并安装
        </button>
        <button className="btn btn-ghost" onClick={onOpenRelease}>
          打开发布页
        </button>
        <span className="sync-state">
          {updateInfo?.error ? (
            <span className="is-error">{updateInfo.error}</span>
          ) : updateInfo?.available ? (
            <span>发现 v{updateInfo.version}（当前 v{updateInfo.current}）</span>
          ) : updateInfo?.checked ? (
            <span>已是最新（v{updateInfo.current}）</span>
          ) : (
            <span>尚未检查</span>
          )}
        </span>
      </div>
    </Section>
  );
}
