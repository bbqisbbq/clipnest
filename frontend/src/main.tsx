import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import Picker from "./Picker";
import { ClipService } from "./lib/api";

// Bundled fonts (SIL OFL / Apache-2.0). Shipping them avoids relying on
// "Microsoft YaHei", which renders blurry at fractional Windows scaling.
import "@fontsource-variable/inter";
import "@fontsource-variable/noto-sans-sc";
import "@fontsource-variable/jetbrains-mono";
import "./styles.css";
import "./theme-light.css";
import { applyTheme } from "./lib/themes";

// The same bundle serves two Wails windows: the manager (default) and the
// hotkey popup, which main.go loads with ?mode=picker.
const isPicker = new URLSearchParams(window.location.search).get("mode") === "picker";
// Apply the theme as early as possible so the first paint is not the wrong colour.
applyTheme(
  (localStorage.getItem("clipnest.theme") as "system" | "dark" | "light") ?? "system",
  localStorage.getItem("clipnest.preset") ?? undefined,
);
document.body.classList.toggle("is-picker", isPicker);

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
    <React.StrictMode>{isPicker ? <Picker /> : <App />}</React.StrictMode>,
  );
} catch (err) {
  report("render", err);
}
