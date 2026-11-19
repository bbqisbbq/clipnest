import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import Picker from "./Picker";
import Capture from "./Capture";
import Stealth from "./Stealth";
import StealthHud from "./StealthHud";
import { ClipService } from "./lib/api";

// 自带字体（SIL OFL / Google Fonts）：
//   Inter + Noto Sans SC —— 界面正文（比例宽度，长中文段落才好读）
//   Maple Mono NF CN     —— 等宽语境（styles/fonts.css，只给代码块/编辑器）
import "@fontsource-variable/inter";
import "@fontsource-variable/noto-sans-sc";
// 样式按域拆成多个文件：import 顺序就是层叠顺序，必须与原 styles.css 的
// 段落顺序一致（同优先级的选择器靠后覆盖）。theme-light.css 仍是最后的补丁。
import "./styles/fonts.css";
import "./styles/base.css";
import "./styles/layout.css";
import "./styles/entries.css";
import "./styles/controls.css";
import "./styles/settings.css";
import "./styles/picker.css";
import "./styles/snippets.css";
import "./styles/timeline.css";
import "./styles/editor.css";
import "./styles/stealth.css";
import "./theme-light.css";
import { applyTheme } from "./lib/themes";

// The same bundle serves every Wails window: the manager (default), the hotkey
// popup (?mode=picker), quick capture (?mode=capture), and the two 隐写 windows
// (?mode=stealth = invisible input, ?mode=stealthhud = corner indicator).
const mode = new URLSearchParams(window.location.search).get("mode");
const isPicker = mode === "picker";
const isCapture = mode === "capture";
// 隐写模式两个窗口：stealth = 全透明输入窗，stealthhud = 角落状态灯。
const isStealth = mode === "stealth";
const isStealthHud = mode === "stealthhud";
// Apply the theme as early as possible so the first paint is not the wrong colour.
applyTheme(
  (localStorage.getItem("clipnest.theme") as "system" | "dark" | "light") ?? "system",
  localStorage.getItem("clipnest.preset") ?? undefined,
);
document.body.classList.toggle("is-picker", isPicker);
document.body.classList.toggle("is-capture", isCapture);
document.body.classList.toggle("is-stealth", isStealth);
document.body.classList.toggle("is-stealthhud", isStealthHud);

// Forward frontend failures to the Go log. Without this a render error just
// leaves an empty window and there is nothing to debug from outside.
function report(source: string, detail: unknown) {
  const text = detail instanceof Error ? `${detail.message}\n${detail.stack ?? ""}` : String(detail);
  console.error(source, detail);
  // Visible to the outside (window title is readable via Win32) in case the
  // binding path itself is what's broken.
  document.title = `ClipNest ⚠ ${source}: ${text}`.slice(0, 120);
  try {
    void ClipService.LogClient(`${source}: ${text}`);
  } catch {
    /* the binding itself may be what failed */
  }
}

window.addEventListener("error", (e) => report("error", e.error ?? e.message));
window.addEventListener("unhandledrejection", (e) => report("unhandledrejection", e.reason));

try {
  ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
    <React.StrictMode>
      {isPicker ? (
        <Picker />
      ) : isCapture ? (
        <Capture />
      ) : isStealth ? (
        <Stealth />
      ) : isStealthHud ? (
        <StealthHud />
      ) : (
        <App />
      )}
    </React.StrictMode>,
  );
} catch (err) {
  report("render", err);
}
