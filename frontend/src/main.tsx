import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import Picker from "./Picker";
import Capture from "./Capture";
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
// Vditor 自带样式必须先于我们的补丁层：同优先级选择器靠后覆盖。
// 补丁层负责三件事：把它的 CSS 变量接到我们的 token、覆盖它写死的浅色（深色主题
// 下那是黑底黑字）、去掉它自带的边框。
import "vditor/dist/index.css";
import "./styles/vditor-theme.css";
import "./styles/detail.css";
import "./styles/todo.css";
import "./styles/filter.css";
import "./theme-light.css";
import { applyTheme, storedTheme } from "./lib/themes";
import { warmUpMarkdown } from "./components/MarkdownSurface";
import { applyToolbarSettings } from "./lib/editorToolbar";
import { DialogProvider } from "./components/Dialog";

// The same bundle serves every Wails window: the manager (default), the hotkey
// popup (?mode=picker) and quick capture (?mode=capture).
const mode = new URLSearchParams(window.location.search).get("mode");
const isPicker = mode === "picker";
const isCapture = mode === "capture";
// Apply the theme as early as possible so the first paint is not the wrong colour.
const boot = storedTheme();
applyTheme(boot.scheme, boot.presetID);
document.body.classList.toggle("is-picker", isPicker);
document.body.classList.toggle("is-capture", isCapture);
// 工具栏配置：capture 窗不跑 useAppData，所以在这里也取一次（异步，取不到就用默认）
void ClipService.GetSettings()
  .then((st) => applyToolbarSettings(st.editorToolbar, st.composerToolbar))
  .catch(() => undefined);

// Forward frontend failures to the Go log. Without this a render error just
// leaves an empty window and there is nothing to debug from outside.
function report(source: string, detail: unknown) {
  const text = detail instanceof Error ? `${detail.message}\n${detail.stack ?? ""}` : String(detail);
  console.error(source, detail);
  // Visible to the outside (window title is readable via Win32) in case the
  // binding path itself is what's broken. 带上窗口模式，否则日志里分不清是哪个窗炸的。
  const where = mode ? `[${mode}] ` : "";
  document.title = `ClipNest ⚠ ${source}: ${text}`.slice(0, 120);
  try {
    void ClipService.LogClient(`${where}${source}: ${text}`);
  } catch {
    /* the binding itself may be what failed */
  }
}

window.addEventListener("error", (e) => report("error", e.error ?? e.message));
window.addEventListener("unhandledrejection", (e) => report("unhandledrejection", e.reason));

// 编辑器引擎预热：lute（Markdown 引擎，3.6MB）的加载与初始化只付一次钱
// （实测：首实例 250ms，后续实例 10~19ms）。热键窗本来就随应用一起创建，
// 空闲时先烧热，按下热键时就不会卡在解析上。
//
// picker 是搜索列表，不需要 Markdown 引擎，别在那儿白烧一遍。
const usesEditor = !isPicker;
const whenIdle = (cb: () => void) => {
  if (typeof window.requestIdleCallback === "function") {
    window.requestIdleCallback(cb, { timeout: 1200 });
  } else {
    window.setTimeout(cb, 300);
  }
};
if (usesEditor) whenIdle(() => void warmUpMarkdown());

try {
  ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
    <React.StrictMode>
      <DialogProvider>{isPicker ? <Picker /> : isCapture ? <Capture /> : <App />}</DialogProvider>
    </React.StrictMode>,
  );
} catch (err) {
  report("render", err);
}
