import { useEffect, useState } from "react";
import { ClipService } from "../lib/api";
import { drag, isMaximised, minimise, noDrag, toggleMaximise } from "../lib/window";

type Props = {
  capturing: boolean;
  enabled: boolean;
  onToggleCapture: () => void;
};

/**
 * TitleBar replaces the OS caption bar. The whole strip is a drag region; every
 * interactive child opts out with `--wails-draggable: none`, otherwise a click
 * on a button would start moving the window instead of pressing it.
 */
export default function TitleBar({ capturing, enabled, onToggleCapture }: Props) {
  const [max, setMax] = useState(false);

  useEffect(() => {
    void isMaximised().then(setMax);
  }, []);

  return (
    <header
      className="tb"
      style={drag}
      onDoubleClick={(e) => {
        if ((e.target as HTMLElement).closest("button")) return;
        void toggleMaximise().then(setMax);
      }}
    >
      <div className="tb-brand">
        <span className="tb-mark" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="6" y="4" width="12" height="17" rx="2.6" />
            <path d="M9.5 4.2V3.4A1.4 1.4 0 0 1 10.9 2h2.2a1.4 1.4 0 0 1 1.4 1.4v.8" />
            <path d="M9.5 10h5M9.5 13.5h5M9.5 17h3" />
          </svg>
        </span>
        <strong>ClipNest</strong>
      </div>

      <button
        className={`tb-pill ${capturing ? "is-live" : ""}`}
        style={noDrag}
        onClick={onToggleCapture}
        disabled={!enabled}
        title={capturing ? "点击暂停收集" : "点击恢复收集"}
      >
        <span className="tb-dot" />
        {capturing ? "记录中" : "已暂停"}
      </button>

      <div className="tb-spacer" />

      <div className="tb-controls" style={noDrag}>
        <button className="win-btn" title="最小化" onClick={minimise}>
          <svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">
            <path d="M2 5h6" stroke="currentColor" strokeWidth="1.1" strokeLinecap="round" />
          </svg>
        </button>
        <button
          className="win-btn"
          title={max ? "还原" : "最大化"}
          onClick={() => void toggleMaximise().then(setMax)}
        >
          {max ? (
            <svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">
              <rect x="2.4" y="3.4" width="5.2" height="5.2" rx="0.6" fill="none" stroke="currentColor" strokeWidth="1.1" />
              <path d="M4 3.4V2.4a.6.6 0 0 1 .6-.6h3a.6.6 0 0 1 .6.6v3a.6.6 0 0 1-.6.6h-1" fill="none" stroke="currentColor" strokeWidth="1.1" />
            </svg>
          ) : (
            <svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">
              <rect x="2.2" y="2.2" width="5.6" height="5.6" rx="0.6" fill="none" stroke="currentColor" strokeWidth="1.1" />
            </svg>
          )}
        </button>
        <button
          className="win-btn is-close"
          title="收起到托盘（程序继续在后台记录）"
          onClick={() => void ClipService.HideWindow()}
        >
          <svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">
            <path d="m2.6 2.6 4.8 4.8M7.4 2.6 2.6 7.4" stroke="currentColor" strokeWidth="1.15" strokeLinecap="round" />
          </svg>
        </button>
      </div>
    </header>
  );
}
