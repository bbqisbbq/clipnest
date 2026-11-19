import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import Picker from "./Picker";
import Capture from "./Capture";
import { ClipService } from "./lib/api";

// Bundled fonts (SIL OFL / Apache-2.0). Shipping them avoids relying on
// "Microsoft YaHei", which renders blurry at fractional Windows scaling.
import "@fontsource-variable/inter";
import "@fontsource-variable/noto-sans-sc";
import "@fontsource-variable/jetbrains-mono";
// 样式按域拆成多个文件：import 顺序就是层叠顺序，必须与原 styles.css 的
// 段落顺序一致（同优先级的选择器靠后覆盖）。theme-light.css 仍是最后的补丁。
import "./styles/base.css";
import "./styles/layout.css";
import "./styles/entries.css";
import "./styles/controls.css";
import "./styles/settings.css";
import "./styles/picker.css";
import "./styles/snippets.css";
import "./styles/timeline.css";
import "./styles/editor.css";
import "./theme-light.css";
import { applyTheme } from "./lib/themes";

// The same bundle serves two Wails windows: the manager (default) and the
// hotkey popup, which main.go loads with ?mode=picker.
const mode = new URLSearchParams(window.location.search).get("mode");
const isPicker = mode === "picker";
const isCapture = mode === "capture";
// Apply the theme as early as possible so the first paint is not the wrong colour.
applyTheme(
  (localStorage.getItem("clipnest.theme") as "system" | "dark" | "light") ?? "system",
  localStorage.getItem("clipnest.preset") ?? undefined,
);
document.body.classList.toggle("is-picker", isPicker);
document.body.classList.toggle("is-capture", isCapture);

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
    <React.StrictMode>{isPicker ? <Picker /> : isCapture ? <Capture /> : <App />}</React.StrictMode>,
  );
} catch (err) {
  report("render", err);
}
