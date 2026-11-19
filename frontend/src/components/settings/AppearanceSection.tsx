import { useMemo } from "react";
import type { Settings } from "../../lib/api";
import { THEME_PRESETS, checkContrast, presetByID } from "../../lib/themes";
import { Row, Section, type SettingsPatch } from "./ui";

// 「外观」分区：深浅方案、配色预设、自定义 CSS，外加一个对比度自检。
// 对比度（WCAG AA 4.5:1）按当前草稿选中的预设现算，相当于把原来写在单测里的
// 检查变成界面提示；切预设立刻能看到结果，保存后与已存设置一致。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
};

export default function AppearanceSection({ draft, patch }: Props) {
  const contrast = useMemo(() => checkContrast(presetByID(draft.themePreset).tokens), [draft.themePreset]);

  return (
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
  );
}
