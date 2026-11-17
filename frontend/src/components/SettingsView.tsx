import { useEffect, useState } from "react";
import type { AppInfo, Settings } from "../lib/api";
import { formatBytes } from "../lib/format";
import { IconFolder, IconTrash } from "./Icons";

type Props = {
  settings: Settings | null;
  appInfo: AppInfo | null;
  storageBytes: number;
  onSave: (next: Settings) => void;
  onClearHistory: () => void;
  onOpenDataDir: () => void;
  onQuit: () => void;
};

function Toggle({
  checked,
  onChange,
  label,
  hint,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint?: string;
}) {
  return (
    <label className="field switch-field">
      <span className="field-label">
        {label}
        {hint ? <em>{hint}</em> : null}
      </span>
      <button
        type="button"
        className={`switch ${checked ? "is-on" : ""}`}
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
      >
        <span className="switch-knob" />
      </button>
    </label>
  );
}

export default function SettingsView({ settings, appInfo, storageBytes, onSave, onClearHistory, onOpenDataDir, onQuit }: Props) {
  const [draft, setDraft] = useState<Settings | null>(settings);
  const [patternText, setPatternText] = useState("");
  const [appText, setAppText] = useState("");

  useEffect(() => {
    setDraft(settings);
    if (settings) {
      setPatternText((settings.ignorePatterns ?? []).join("\n"));
      setAppText((settings.ignoreApps ?? []).join(", "));
    }
  }, [settings]);

  if (!draft) {
    return <section className="settings"><p className="muted">正在加载设置…</p></section>;
  }

  const patch = (p: Partial<Settings>) => setDraft({ ...draft, ...p });

  const save = () => {
    const next = { ...draft };
    next.ignorePatterns = patternText
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    next.ignoreApps = appText
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    onSave(next);
  };

  return (
    <section className="settings">
      <header className="panel-head">
        <div>
          <h2>设置</h2>
          <p>所有配置立即保存在本机，仅对 ClipNest 生效。</p>
        </div>
        <div className="panel-head-actions">
          <button className="ghost-btn" onClick={() => setDraft(settings)}>
            撤销修改
          </button>
          <button className="primary-btn" onClick={save}>
            保存设置
          </button>
        </div>
      </header>

      <div className="settings-body">
        <div className="card">
          <h3>采集</h3>
          <Toggle
            label="自动收集剪贴板"
            hint="关闭后仍在托盘运行，但不再记录新内容"
            checked={draft.capture}
            onChange={(v) => patch({ capture: v })}
          />
          <div className="grid-2">
            <Toggle label="文本" checked={draft.captureText} onChange={(v) => patch({ captureText: v })} />
            <Toggle label="链接" checked={draft.captureLink} onChange={(v) => patch({ captureLink: v })} />
            <Toggle label="图片" checked={draft.captureImage} onChange={(v) => patch({ captureImage: v })} />
            <Toggle label="文件（仅路径引用）" checked={draft.captureFiles} onChange={(v) => patch({ captureFiles: v })} />
            <Toggle label="富文本（HTML / RTF）" checked={draft.captureRich} onChange={(v) => patch({ captureRich: v })} />
            <Toggle
              label="其它格式（原样存字节）"
              hint="Excel 区间、颜色、音频等私有格式"
              checked={draft.captureCustom}
              onChange={(v) => patch({ captureCustom: v })}
            />
          </div>
          <label className="field">
            <span className="field-label">
              单条文本长度上限
              <em>超过则跳过，0 表示不限制</em>
            </span>
            <input
              type="number"
              min={0}
              step={1000}
              value={draft.maxTextLength}
              onChange={(e) => patch({ maxTextLength: Number(e.target.value) || 0 })}
            />
          </label>
        </div>

        <div className="card">
          <h3>保留策略</h3>
          <div className="grid-2">
            <label className="field">
              <span className="field-label">
                最多保留条数
                <em>超出后删除最旧的未收藏条目</em>
              </span>
              <input
                type="number"
                min={50}
                step={50}
                value={draft.maxItems}
                onChange={(e) => patch({ maxItems: Number(e.target.value) || 1000 })}
              />
            </label>
            <label className="field">
              <span className="field-label">
                最长保留天数
                <em>0 表示永久保留</em>
              </span>
              <input
                type="number"
                min={0}
                step={1}
                value={draft.maxAgeDays}
                onChange={(e) => patch({ maxAgeDays: Number(e.target.value) || 0 })}
              />
            </label>
          </div>
          <p className="hint-line">收藏（星标）的条目不会被自动清理。</p>
        </div>

        <div className="card">
          <h3>忽略规则</h3>
          <label className="field">
            <span className="field-label">
              忽略这些程序
              <em>可执行文件名，逗号分隔，例如 keepass.exe, 1password.exe</em>
            </span>
            <input value={appText} placeholder="keepass.exe, bitwarden.exe" onChange={(e) => setAppText(e.target.value)} />
          </label>
          <label className="field">
            <span className="field-label">
              忽略匹配这些正则的内容
              <em>每行一条，命中文本或文件名即跳过</em>
            </span>
            <textarea
              rows={4}
              value={patternText}
              placeholder={"^sk-[A-Za-z0-9]{20,}\npassword\\s*="}
              onChange={(e) => setPatternText(e.target.value)}
            />
          </label>
        </div>

        <div className="card">
          <h3>行为</h3>
          <label className="field">
            <span className="field-label">
              快速粘贴快捷键
              <em>在光标处弹出列表；留空表示不注册</em>
            </span>
            <input value={draft.hotkey} placeholder="Ctrl+Shift+V" onChange={(e) => patch({ hotkey: e.target.value })} />
          </label>
          <Toggle
            label="选中后自动粘贴"
            hint="关闭则只写入剪贴板，需自己按 Ctrl+V"
            checked={draft.pasteOnPick}
            onChange={(v) => patch({ pasteOnPick: v })}
          />
          <Toggle label="开机自动启动" hint="写入当前用户的开机启动项" checked={draft.autoStart} onChange={(v) => patch({ autoStart: v })} />
          <Toggle
            label="启动时打开管理窗口"
            hint="关闭则仅驻留托盘"
            checked={draft.showOnLaunch}
            onChange={(v) => patch({ showOnLaunch: v })}
          />
          <p className="hint-line">关闭窗口不会退出程序；需要彻底退出请用托盘菜单的「退出 ClipNest」。</p>
        </div>

        <div className="card">
          <h3>数据与关于</h3>
          <dl className="meta-grid">
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
              <dd className="path">{appInfo?.dataDir ?? "-"}</dd>
            </div>
          </dl>
          <div className="row-actions">
            <button className="ghost-btn" onClick={onOpenDataDir}>
              <IconFolder size={15} />
              打开数据目录
            </button>
            <button className="danger-btn" onClick={onClearHistory}>
              <IconTrash size={15} />
              清空未收藏的历史
            </button>
          </div>
        </div>

        <div className="card danger-card">
          <h3>退出</h3>
          <p className="hint-line">退出后不再监听剪贴板，历史记录仍保存在数据目录。</p>
          <button className="danger-btn" onClick={onQuit}>
            退出 ClipNest
          </button>
        </div>
      </div>
    </section>
  );
}
