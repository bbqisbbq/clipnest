import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { AppInfo, Settings, SyncStatus } from "../lib/api";
import { formatBytes, formatDateTime } from "../lib/format";
import { IconFolder, IconSync, IconTrash } from "./Icons";
import { Tip } from "./Tip";

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
};

function Switch({
  checked,
  onChange,
  disabled,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      className={`switch ${checked ? "is-on" : ""}`}
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span className="switch-knob" />
    </button>
  );
}

function Row({
  label,
  tip,
  children,
  stacked,
}: {
  label: string;
  tip?: string;
  children: ReactNode;
  stacked?: boolean;
}) {
  return (
    <div className={`field ${stacked ? "is-stacked" : ""}`}>
      <div className="field-label">
        <span>
          {label}
          {tip ? <Tip text={tip} right={stacked} /> : null}
        </span>
      </div>
      <div className="field-control">{children}</div>
    </div>
  );
}

function Section({
  title,
  desc,
  children,
}: {
  title: string;
  desc?: string;
  children: ReactNode;
}) {
  return (
    <section className="card">
      <header className="card-head">
        <h3>{title}</h3>
        {desc ? <p>{desc}</p> : null}
      </header>
      <div className="card-body">{children}</div>
    </section>
  );
}

export default function SettingsView(props: Props) {
  const { settings, appInfo, storageBytes, syncStatus } = props;
  const [draft, setDraft] = useState<Settings | null>(settings);
  const [patternText, setPatternText] = useState("");
  const [appText, setAppText] = useState("");
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState("");

  useEffect(() => {
    setDraft(settings);
    if (settings) {
      setPatternText((settings.ignorePatterns ?? []).join("\n"));
      setAppText((settings.ignoreApps ?? []).join(", "));
    }
  }, [settings]);

  const dirty = useMemo(() => {
    if (!draft || !settings) return false;
    return (
      JSON.stringify({ ...draft, ignorePatterns: [], ignoreApps: [] }) !==
        JSON.stringify({ ...settings, ignorePatterns: [], ignoreApps: [] }) ||
      patternText !== (settings.ignorePatterns ?? []).join("\n") ||
      appText !== (settings.ignoreApps ?? []).join(", ")
    );
  }, [draft, settings, patternText, appText]);

  if (!draft) {
    return (
      <main className="main is-settings">
        <p className="muted">正在加载设置…</p>
      </main>
    );
  }

  const patch = (p: Partial<Settings>) => setDraft({ ...draft, ...p });

  const save = () => {
    const next = { ...draft };
    next.ignorePatterns = patternText.split("\n").map((s) => s.trim()).filter(Boolean);
    next.ignoreApps = appText.split(",").map((s) => s.trim()).filter(Boolean);
    props.onSave(next);
  };

  const revert = () => {
    setDraft(settings);
    if (settings) {
      setPatternText((settings.ignorePatterns ?? []).join("\n"));
      setAppText((settings.ignoreApps ?? []).join(", "));
    }
    setTestResult("");
  };

  const runTest = async () => {
    setTesting(true);
    setTestResult("");
    try {
      setTestResult(await props.onTestSync());
    } catch (err) {
      setTestResult(`失败：${String(err)}`);
    } finally {
      setTesting(false);
    }
  };

  const lastSync = syncStatus?.lastSync ? formatDateTime(syncStatus.lastSync) : "从未同步";

  return (
    <main className="main is-settings">
      <div className="settings">
        <div className="settings-inner">
          <Section title="采集" desc="控制记录什么；关闭总开关后程序仍在托盘运行，只是不再记录。">
            <Row label="自动收集剪贴板" tip="关闭后不会记录任何新内容，历史记录保持不变。">
              <Switch checked={draft.capture} onChange={(v) => patch({ capture: v })} />
            </Row>
            <Row label="文本" tip="包括纯文本与单行内容；单独的网址会自动归类为「链接」。">
              <Switch checked={draft.captureText} onChange={(v) => patch({ captureText: v })} />
            </Row>
            <Row label="链接" tip="从浏览器地址栏或 text/uri-list 复制出来的网址。">
              <Switch checked={draft.captureLink} onChange={(v) => patch({ captureLink: v })} />
            </Row>
            <Row label="图片" tip="支持 PNG、CF_DIB、CF_DIBV5 三种来源，截图工具与浏览器复制的图片都能收录。">
              <Switch checked={draft.captureImage} onChange={(v) => patch({ captureImage: v })} />
            </Row>
            <Row label="文件（仅路径引用）" tip="只记录路径与元信息，不复制实体文件；原文件被移动或删除后会标记为失效。">
              <Switch checked={draft.captureFiles} onChange={(v) => patch({ captureFiles: v })} />
            </Row>
            <Row label="富文本" tip="HTML 片段或 RTF（Word、WordPad、Outlook 等），粘贴回去时保留格式。">
              <Switch checked={draft.captureRich} onChange={(v) => patch({ captureRich: v })} />
            </Row>
            <Row label="其它格式" tip="Excel 区间、颜色、音频等私有格式，按原始字节保存（单条上限 1 MiB），可原样粘回。">
              <Switch checked={draft.captureCustom} onChange={(v) => patch({ captureCustom: v })} />
            </Row>
            <Row label="单条文本长度上限" tip="超过该长度的文本会被跳过，0 表示不限制。">
              <input
                className="input is-num"
                type="number"
                min={0}
                step={1000}
                value={draft.maxTextLength}
                onChange={(e) => patch({ maxTextLength: Number(e.target.value) || 0 })}
              />
              <span className="unit">字符</span>
            </Row>
          </Section>

          <Section title="保留策略" desc="收藏（星标）的条目不会被自动清理。">
            <Row label="最多保留条数" tip="超出后删除最旧的未收藏条目。">
              <input
                className="input is-num"
                type="number"
                min={50}
                step={50}
                value={draft.maxItems}
                onChange={(e) => patch({ maxItems: Number(e.target.value) || 1000 })}
              />
              <span className="unit">条</span>
            </Row>
            <Row label="最长保留天数" tip="0 表示永久保留。">
              <input
                className="input is-num"
                type="number"
                min={0}
                step={1}
                value={draft.maxAgeDays}
                onChange={(e) => patch({ maxAgeDays: Number(e.target.value) || 0 })}
              />
              <span className="unit">天</span>
            </Row>
          </Section>

          <Section title="忽略规则" desc="命中的内容不会进入历史，适合密码管理器与内部系统。">
            <Row label="忽略这些程序" tip="可执行文件名，逗号分隔，例如 keepass.exe, bitwarden.exe。" stacked>
              <input
                className="input"
                value={appText}
                placeholder="keepass.exe, bitwarden.exe"
                onChange={(e) => setAppText(e.target.value)}
              />
            </Row>
            <Row label="忽略匹配这些正则的内容" tip="每行一条正则，命中文本、预览或文件名即跳过。" stacked>
              <textarea
                className="input is-area"
                rows={4}
                value={patternText}
                placeholder={"^sk-[A-Za-z0-9]{20,}\npassword\\s*="}
                onChange={(e) => setPatternText(e.target.value)}
              />
            </Row>
          </Section>

          <Section title="交互与启动" desc="快速粘贴窗在光标处弹出，选中后直接粘进当前程序。">
            <Row label="快速粘贴快捷键" tip="例如 Ctrl+Shift+V；留空表示不注册全局快捷键。">
              <input
                className="input is-short"
                value={draft.hotkey}
                placeholder="Ctrl+Shift+V"
                onChange={(e) => patch({ hotkey: e.target.value })}
              />
            </Row>
            <Row label="选中后自动粘贴" tip="关闭后只写入剪贴板，需要自己按 Ctrl+V。">
              <Switch checked={draft.pasteOnPick} onChange={(v) => patch({ pasteOnPick: v })} />
            </Row>
            <Row label="启动时打开管理窗口" tip="关闭则启动后只驻留托盘。" >
              <Switch checked={draft.showOnLaunch} onChange={(v) => patch({ showOnLaunch: v })} />
            </Row>
            <Row
              label="开机自动启动"
              tip={
                appInfo?.autoStartTarget
                  ? `登录 Windows 时自动运行（写入 HKCU\\...\\Run），以 --background 参数启动，只驻留托盘不弹窗。当前命令：${appInfo.autoStartTarget}`
                  : "登录 Windows 时自动运行（写入 HKCU\\...\\Run），以 --background 参数启动，只驻留托盘不弹窗。"
              }
            >
              <Switch checked={draft.autoStart} onChange={(v) => patch({ autoStart: v })} />
            </Row>
          </Section>

          <Section title="云同步（WebDAV）" desc="把整份历史发布成一个 JSON 文档，按内容哈希合并多台机器。">
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
            <Row label="同步文件名" tip="存放在上面目录里的文档名，多台机器保持一致即可。" stacked>
              <input
                className="input"
                value={draft.syncFile}
                placeholder="clipnest-sync.json"
                onChange={(e) => patch({ syncFile: e.target.value })}
              />
            </Row>
            <Row label="自动同步间隔" tip="分钟；0 表示只在手动点击时同步。">
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

            <div className="sync-actions">
              <button className="btn btn-ghost" onClick={runTest} disabled={testing}>
                {testing ? "测试中…" : "测试连接"}
              </button>
              <button
                className="btn btn-ghost"
                onClick={props.onSyncNow}
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
                  </>
                )}
              </span>
            </div>
            {testResult ? <p className="test-result">{testResult}</p> : null}
          </Section>

          <Section title="数据与关于" desc="所有数据都在本机，不同步时不会产生任何网络请求。">
            <dl className="kv is-wide">
              <div>
                <dt>版本</dt>
                <dd>v{appInfo?.version ?? "0.1.0"}</dd>
              </div>
              <div>
                <dt>数据占用</dt>
                <dd>{formatBytes(storageBytes)}</dd>
              </div>
              <div className="span-2">
                <dt>数据目录</dt>
                <dd className="mono">{appInfo?.dataDir ?? "-"}</dd>
              </div>
            </dl>
            <div className="card-actions">
              <button className="btn btn-ghost" onClick={props.onOpenDataDir}>
                <IconFolder size={14} />
                打开数据目录
              </button>
              <button className="btn btn-danger-ghost" onClick={props.onClearHistory}>
                <IconTrash size={14} />
                清空未收藏的历史
              </button>
              <button className="btn btn-danger-ghost" onClick={props.onQuit}>
                退出 ClipNest
              </button>
            </div>
          </Section>
        </div>
      </div>

      <footer className="settings-bar">
        <span className={dirty ? "is-dirty" : "muted"}>{dirty ? "有未保存的修改" : "设置已保存"}</span>
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
