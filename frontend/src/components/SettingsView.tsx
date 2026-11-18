import UsageStats from "./UsageStats";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ClipService } from "../lib/api";
import type { AppInfo, Settings, SyncPanel, SyncStatus, UpdateInfo } from "../lib/api";
import { formatBytes, formatDateTime } from "../lib/format";
import { IconFolder, IconSync, IconTrash } from "./Icons";
import { THEME_PRESETS, checkContrast, presetByID } from "../lib/themes";
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
  updateInfo: UpdateInfo | null;
  onCheckUpdate: () => Promise<UpdateInfo>;
  onInstallUpdate: () => void;
  onOpenRelease: () => void;
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
  // 当前预设的对比度自检（WCAG AA 4.5:1）——相当于把 Memos 的单测变成界面提示
  const exportMemos = async () => {
    try {
      const path = await ClipService.ExportMemosToFile("");
      setMemoIo(`已导出：${path}`);
    } catch (err) {
      setMemoIo(`导出失败：${String(err)}`);
    }
  };

  const importMemos = async () => {
    try {
      const res = await ClipService.ImportMemosFromFile(memoImportPath.trim());
      setMemoIo(`导入完成：新增 ${res.added} 条，跳过 ${res.skipped} 条`);
    } catch (err) {
      setMemoIo(`导入失败：${String(err)}`);
    }
  };

  const [savedFlash, setSavedFlash] = useState(false);
  const flashTimer = useRef<number | null>(null);
  const [memoImportPath, setMemoImportPath] = useState("");
  const [memoIo, setMemoIo] = useState("");
  const contrast = useMemo(
    () => checkContrast(presetByID(props.settings?.themePreset).tokens),
    [props.settings?.themePreset],
  );
  const { settings, appInfo, storageBytes, syncStatus } = props;
  const [draft, setDraft] = useState<Settings | null>(settings);
  const [patternText, setPatternText] = useState("");
  const [appText, setAppText] = useState("");
  const [testing, setTesting] = useState(false);
  const [checking, setChecking] = useState(false);
  const [panel, setPanel] = useState<SyncPanel | null>(null);
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

  const showBar = dirty || savedFlash;

  const save = () => {
    const next = { ...draft };
    next.ignorePatterns = patternText.split("\n").map((s) => s.trim()).filter(Boolean);
    next.ignoreApps = appText.split(",").map((s) => s.trim()).filter(Boolean);
    props.onSave(next);
    // 保存成功的提示只闪现 2 秒，然后状态栏自动收起，不长期占着屏幕
    setSavedFlash(true);
    if (flashTimer.current) window.clearTimeout(flashTimer.current);
    flashTimer.current = window.setTimeout(() => setSavedFlash(false), 2000);
  };

  useEffect(() => () => { if (flashTimer.current) window.clearTimeout(flashTimer.current); }, []);

  const revert = () => {
    setDraft(settings);
    if (settings) {
      setPatternText((settings.ignorePatterns ?? []).join("\n"));
      setAppText((settings.ignoreApps ?? []).join(", "));
    }
    setTestResult("");
  };

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

  const runCheckUpdate = async () => {
    setChecking(true);
    try {
      await props.onCheckUpdate();
    } finally {
      setChecking(false);
    }
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

          <Section title="忽略程序" desc="这些程序里复制的内容不会被记录，适合密码管理器。">
            <Row label="程序名" tip="可执行文件名，逗号分隔，例如 keepass.exe, bitwarden.exe。" stacked>
              <input
                className="input"
                value={appText}
                placeholder="keepass.exe, bitwarden.exe"
                onChange={(e) => setAppText(e.target.value)}
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

          <Section title="云同步（WebDAV · 每台机器一个包）" desc="把整份历史发布成一个 JSON 文档，按内容哈希合并多台机器。">
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
              tip="把采集开关、保留策略、忽略规则、快捷键与粘贴行为一起同步（不含本机专属项：WebDAV 地址/密码、开机自启、启动即显示）。改设置时以最近修改的那台机器为准。"
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

          <Section title="片段与缩写" desc="片段是「经常要用的内容」；缩写让你在任意输入框里直接把它敲出来。">
            <Row label="弹窗里显示片段" tip="在快速粘贴列表里输入 -- 切换到片段模式（和 eTool 的 -- 搜索一致），回车按类型执行：文本/图片粘贴、网址打开、程序启动、命令执行。">
              <Switch checked={draft.snippetsInPicker} onChange={(v) => patch({ snippetsInPicker: v })} />
            </Row>
            <Row
              label="缩写自动展开"
              tip={
                `打开后键入「缩写 + 后缀」立即替换成片段内容，例如 addr- → 办公地址。这是一个全局键盘钩子：只在普通权限的窗口生效，提权窗口和部分游戏不生效；关掉开关立刻卸载。`
              }
            >
              <Switch checked={draft.expandEnabled} onChange={(v) => patch({ expandEnabled: v })} />
            </Row>
              <Row label="触发字符" tip="终止符：缩写后面打这些字符中的任意一个就展开（空格、回车、Tab、逗号、句号等）。默认包含空格，所以打完 addr 按空格即可。">
                <input
                  className="input is-trigger"
                  value={draft.expandEndChars}
                  placeholder="<space> \\t,.;:!?)]}"
                  onChange={(e) => patch({ expandEndChars: e.target.value })}
                />
              </Row>
          </Section>

            <Section title="外观" desc="配色预设 + 可选的深浅方案 + 你自己的 CSS。三者叠加生效，改完点保存。">
              <Row label="深浅" tip="跟随系统会随 Windows 的深色/浅色设置自动切换；选了预设时以预设的明暗为准。">
                <div className="seg-control">
                  {(["system", "dark", "light"] as const).map((choice) => (
                    <button
                      key={choice}
                      className={`seg-item ${(draft.theme ?? "system") === choice ? "is-on" : ""}`}
                      onClick={() => patch({ theme: choice })}
                    >
                      {choice === "system" ? "跟随系统" : choice === "dark" ? "深色" : "浅色"}
                    </button>
                  ))}
                </div>
              </Row>
              <div className="field is-stacked">
                <span className="field-label">配色预设</span>
                <div className="preset-grid">
                  {THEME_PRESETS.map((preset) => (
                    <button
                      key={preset.id}
                      className={`preset-card ${(draft.themePreset ?? "default-dark") === preset.id ? "is-on" : ""}`}
                      onClick={() => patch({ themePreset: preset.id })}
                      title={preset.note}
                    >
                      <span className="preset-swatch" style={{ background: preset.tokens["--bg"] }}>
                        <i style={{ background: preset.tokens["--surface-2"] }} />
                        <i style={{ background: preset.tokens["--text"] }} />
                        <i style={{ background: preset.tokens["--accent"] }} />
                      </span>
                      <span className="preset-name">{preset.name}</span>
                      <span className="preset-note">{preset.note}</span>
                    </button>
                  ))}
                </div>
              </div>
              <div className="field is-stacked">
                <span className="field-label">
                  自定义 CSS
                  <button className="link-btn" onClick={() => patch({ themeCustomCss: "" })}>
                    清空
                  </button>
                </span>
                <textarea
                  className="input is-area is-mono"
                  rows={5}
                  placeholder={":root { --accent: #ff7a59; }\n.side { border-right: 1px solid var(--accent-line); }"}
                  value={draft.themeCustomCss ?? ""}
                  onChange={(e) => patch({ themeCustomCss: e.target.value })}
                />
                <p className="snip-hint">
                  只作用于本机。为安全起见会自动去掉 @import 与远程 url()；可用变量见预设里那些 -- 开头的名字。
                </p>
              </div>
              {contrast.length > 0 ? (
                <div className="contrast-list">
                  {contrast.map((c) => (
                    <span key={c.label} className={c.ok ? "contrast-ok" : "contrast-bad"}>
                      {c.ok ? "✓" : "!"} {c.label} {c.ratio}:1
                    </span>
                  ))}
                </div>
              ) : null}
            </Section>
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
                onClick={props.onInstallUpdate}
                disabled={!props.updateInfo?.available}
              >
                下载并安装
              </button>
              <button className="btn btn-ghost" onClick={props.onOpenRelease}>
                打开发布页
              </button>
              <span className="sync-state">
                {props.updateInfo?.error ? (
                  <span className="is-error">{props.updateInfo.error}</span>
                ) : props.updateInfo?.available ? (
                  <span>发现 v{props.updateInfo.version}（当前 v{props.updateInfo.current}）</span>
                ) : props.updateInfo?.checked ? (
                  <span>已是最新（v{props.updateInfo.current}）</span>
                ) : (
                  <span>尚未检查</span>
                )}
              </span>
            </div>
          </Section>

          <Section title="备忘" desc="快捷键在光标处弹出一个小输入框，Enter 存成备忘；也可以和 Memos 互相导入导出。">
            <Row label="快速输入快捷键" tip="全局快捷键，例如 Ctrl+Shift+N。留空可关闭。">
              <input
                className="input is-narrow"
                value={draft.captureHotkey ?? ""}
                placeholder="Ctrl+Shift+N"
                onChange={(e) => patch({ captureHotkey: e.target.value })}
              />
            </Row>
            <Row label="导出为 Memos 包" tip="生成 memos-export 1.0 格式的 zip，可直接导入任意 Memos 实例。">
              <div className="settings-inline">
                <button className="btn btn-ghost btn-sm" onClick={() => void exportMemos()}>
                  导出
                </button>
                {memoIo ? <span className="snip-hint">{memoIo}</span> : null}
              </div>
            </Row>
            <Row label="从 Memos 包导入" tip="支持 memos-export 1.0 的 zip；同样的内容不会重复导入。">
              <div className="settings-inline">
                <input
                  className="input"
                  placeholder="C:\\path\\to\\memos-export.zip"
                  value={memoImportPath}
                  onChange={(e) => setMemoImportPath(e.target.value)}
                />
                <button
                  className="btn btn-ghost btn-sm"
                  disabled={!memoImportPath.trim()}
                  onClick={() => void importMemos()}
                >
                  导入
                </button>
              </div>
            </Row>
          </Section>

          <Section title="使用统计" desc="GitHub 公布的安装包下载次数，用来粗略判断有没有人在用；本地读取，不上传任何东西。">
            <UsageStats appVersion={appInfo?.version ?? "1.0.0"} />
          </Section>


          <Section title="数据与关于" desc="所有数据都在本机（SQLite 数据库），不同步时不会产生任何网络请求。">
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

      <footer className={`settings-bar ${showBar ? "is-shown" : "is-hidden"}`} aria-hidden={!showBar}>
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
