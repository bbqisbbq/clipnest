import { useEffect, useState } from "react";
import { ClipService } from "../../lib/api";
import type { Settings, SyncPanel, SyncStatus } from "../../lib/api";
import { formatBytes, formatDateTime } from "../../lib/format";
import { IconSync } from "../Icons";
import { Row, Section, Switch, type SettingsPatch } from "./ui";

// 「云同步」分区：WebDAV 连接参数、同步间隔、设备包面板。
// 面板数据（panel）与「测试连接」结果（testing / testResult）是本分区专有状态，
// 就留在本组件里；draft 与保存动作仍由 SettingsView 管。
//
// resetSignal：父组件的「撤销」会把不属于草稿的临时提示一并清掉（原来如此），
// 这里靠它来清空测试结果。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
  /** 已保存的设置（按钮的可用性按已保存值判断，与原来的语义一致）。 */
  settings: Settings | null;
  syncStatus: SyncStatus | null;
  onTestSync: () => Promise<string>;
  onSyncNow: () => void;
  resetSignal: number;
};

export default function SyncSection({
  draft,
  patch,
  settings,
  syncStatus,
  onTestSync,
  onSyncNow,
  resetSignal,
}: Props) {
  const [panel, setPanel] = useState<SyncPanel | null>(null);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState("");

  // 设备面板：进设置页拉一次，每次同步结束再刷新
  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const next = await ClipService.GetSyncPanel();
        if (alive) setPanel(next);
      } catch {
        /* 未配置同步时忽略 */
      }
    })();
    return () => {
      alive = false;
    };
  }, [syncStatus?.lastSync, syncStatus?.inProgress]);

  useEffect(() => {
    if (resetSignal > 0) setTestResult("");
  }, [resetSignal]);

  const runTest = async () => {
    setTesting(true);
    setTestResult("");
    try {
      setTestResult(await onTestSync());
    } catch (err) {
      setTestResult(`失败：${String(err)}`);
    } finally {
      setTesting(false);
    }
  };

  const lastSync = syncStatus?.lastSync ? formatDateTime(syncStatus.lastSync) : "从未同步";

  return (
    <Section title="云同步（WebDAV · 每台机器一个包）" desc="把整份历史打成自己的一个 zip 包，按内容哈希与其他机器合并。">
      <Row label="启用同步" tip="开启后会定时拉取并上传；同一内容在多台机器上会合并为一条。">
        <Switch checked={draft.syncEnabled} onChange={(v) => patch({ syncEnabled: v })} />
      </Row>
      <Row label="服务器地址" tip="WebDAV 目录，例如 https://dav.jianguoyun.com/dav/ClipNest 。" stacked>
        <input
          className="input"
          value={draft.syncUrl}
          placeholder="https://dav.example.com/dav/ClipNest"
          onChange={(e) => patch({ syncUrl: e.target.value })}
        />
      </Row>
      <div className="field-pair">
        <Row label="用户名" stacked>
          <input className="input" value={draft.syncUser} onChange={(e) => patch({ syncUser: e.target.value })} />
        </Row>
        <Row label="密码" tip="明文保存在本机配置文件中，请勿使用主账号密码。" stacked>
          <input
            className="input"
            type="password"
            value={draft.syncPass}
            onChange={(e) => patch({ syncPass: e.target.value })}
          />
        </Row>
      </div>

      <Row label="自动同步间隔" tip="分钟，默认 30；0 表示只在手动点击时同步。每次同步会读取其他设备的同步包，本机包没有变化时不会重复上传。">
        <input
          className="input is-num"
          type="number"
          min={0}
          step={5}
          value={draft.syncIntervalMin}
          onChange={(e) => patch({ syncIntervalMin: Number(e.target.value) || 0 })}
        />
        <span className="unit">分钟</span>
      </Row>
      <Row
        label="同步设置项"
        tip="把采集开关、快捷键、粘贴行为与外观一起同步（不含本机专属项：WebDAV 地址/密码、开机自启、启动即显示）。改设置时以最近修改的那台机器为准。"
      >
        <Switch
          checked={draft.syncSettings}
          onChange={(v) => patch({ syncSettings: v })}
          disabled={!draft.syncEnabled}
        />
      </Row>

      <div className="sync-actions">
        <button className="btn btn-ghost" onClick={runTest} disabled={testing}>
          {testing ? "测试中…" : "测试连接"}
        </button>
        <button
          className="btn btn-ghost"
          onClick={onSyncNow}
          disabled={!settings?.syncEnabled || syncStatus?.inProgress}
        >
          <IconSync size={14} />
          {syncStatus?.inProgress ? "同步中…" : "立即同步"}
        </button>
        <span className="sync-state">
          {syncStatus?.lastError ? (
            <span className="is-error">{syncStatus.lastError}</span>
          ) : (
            <>
              <span>{lastSync}</span>
              {syncStatus?.lastResult ? <span>· {syncStatus.lastResult}</span> : null}
              {syncStatus?.devices ? <span>· {syncStatus.devices} 台设备</span> : null}
            </>
          )}
        </span>
      </div>
      {testResult ? <p className="test-result">{testResult}</p> : null}

      {panel && (panel.devices ?? []).length > 0 ? (
        <div className="device-list">
          <div className="device-row is-head">
            <span>设备</span>
            <span>包大小</span>
            <span>最后更新</span>
            <span>记录</span>
            <span>本轮拉取</span>
          </div>
          {(panel.devices ?? []).map((d) => (
            <div className={`device-row ${d.self ? "is-self" : ""}`} key={d.name}>
              <span title={d.name}>
                {d.device}
                {d.self ? <em className="badge">本机</em> : null}
              </span>
              <span className="mono">{formatBytes(d.bytes)}</span>
              <span>{d.updatedAt ? formatDateTime(String(d.updatedAt)) : "—"}</span>
              <span>{d.items ? d.items : "—"}</span>
              <span>
                {d.pulledNew || d.pulledUpdated || d.deleted
                  ? `${d.pulledNew} 新增 / ${d.pulledUpdated} 更新${d.deleted ? ` / ${d.deleted} 删除` : ""}`
                  : "—"}
              </span>
            </div>
          ))}
        </div>
      ) : null}

      {panel?.last?.at ? (
        <p className="test-result">
          上次同步 {formatDateTime(String(panel.last.at))} · 耗时 {panel.last.durationMs} ms · 拉取{" "}
          {panel.last.pulledNew} 新增 / {panel.last.pulledUpdated} 更新
          {panel.last.deletedLocal ? ` / ${panel.last.deletedLocal} 删除` : ""} ·{" "}
          {panel.last.pushed ? `上传 ${formatBytes(panel.last.pushedBytes)}` : "未上传"}
          {panel.last.note ? `（${panel.last.note}）` : ""}
          {panel.last.settingsPulled ? " · 采纳远端设置" : ""}
          {panel.last.settingsPushed ? " · 已上传设置" : ""}
        </p>
      ) : null}
    </Section>
  );
}
