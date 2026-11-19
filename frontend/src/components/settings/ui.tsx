import type { ReactNode } from "react";
import type { Settings } from "../../lib/api";
import { Tip } from "../Tip";

// 设置页的三个共用小件：开关、字段行、分区卡片。
// 从原 SettingsView 原样搬出（类名、aria 属性、Tip 的用法都没动），
// 各 Section 组件与 SettingsView 共用这一份。

/** 设置草稿的局部更新函数：只传要改的字段，其余保持不变。 */
export type SettingsPatch = (p: Partial<Settings>) => void;

export function Switch({
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

export function Row({
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

export function Section({
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
