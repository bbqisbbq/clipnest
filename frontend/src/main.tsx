import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import Picker from "./Picker";
import { ClipService } from "./lib/api";
import "./styles.css";

// The same bundle serves two Wails windows: the manager (default) and the
// hotkey popup, which main.go loads with ?mode=picker.
const isPicker = new URLSearchParams(window.location.search).get("mode") === "picker";
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
