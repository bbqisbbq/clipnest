import { useCallback, useEffect, useRef, useState } from "react";
import MarkdownSurface, { type MarkdownSurfaceHandle, warmUpMarkdown } from "./components/MarkdownSurface";
import { ClipService } from "./lib/api";
import { drag, noDrag } from "./lib/window";

// 快速输入窗（Ctrl+Shift+N）：光标处弹出来，写完 Enter 保存、Esc 关闭。
//
// 几个曾经踩过的点，这里都显式处理：
//   · Esc —— 编辑器会吞按键，必须走它的 esc 回调（onEscape）而不是外层 onKeyDown
//   · 自动聚焦 —— 窗口刚 Show 时还没拿到焦点，要等 window focus 再 focus 编辑器
//   · 预热 —— 这个窗口随应用一起创建，挂载后立刻把 lute 烧热，否则第一次唤起
//     要等 250ms 的引擎初始化（实测数据见 docs/design 3.2.1）
//   · 界面 —— 无边框整块浮层，只有一个「保存」；格式靠 Markdown 语法（- [ ] 就是待办）
//   · 拖动 —— 抬头那条（`.capture-head`）是拖动区（`--wails-draggable: drag`），
//     里面的 × 必须显式 no-drag，否则点它会被当成想拖窗口。改大小不用前端做任何事：
//     窗口去掉 DisableResize 后，Wails 运行时自己把四周边缘变成拉伸热区。

export default function Capture() {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const editorRef = useRef<MarkdownSurfaceHandle | null>(null);
  const textRef = useRef(text);
  textRef.current = text;

  // 挂载即预热：这个窗口在热键按下之前就已经存在了，预热不占用户可感知的时间。
  useEffect(() => {
    void warmUpMarkdown();
  }, []);

  const tags = (() => {
    const found = new Set<string>();
    for (const m of text.matchAll(/#([\p{L}\p{N}_/-]+)/gu)) found.add(m[1]);
    return [...found];
  })();

  const close = useCallback(() => {
    void ClipService.CloseCapture();
  }, []);

  const save = useCallback(async () => {
    // 以编辑器里的真实内容为准（state 可能落后一个 keystroke）
    const content = (editorRef.current?.getValue() ?? textRef.current).trim();
    if (!content) {
      close();
      return;
    }
    setBusy(true);
    setError("");
    try {
      await ClipService.SaveEntry({ content, source: "manual", kind: "text" } as never);
      setText("");
      close();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [close]);

  // 窗口显示时编辑器可能还没拿到焦点：补一次，并在窗口重新获得焦点时也补。
  useEffect(() => {
    const focus = () => editorRef.current?.focus();
    focus();
    const t = window.setTimeout(focus, 60);
    window.addEventListener("focus", focus);
    return () => {
      window.clearTimeout(t);
      window.removeEventListener("focus", focus);
    };
  }, []);

  return (
    <div className="capture">
      <div className="capture-head" style={drag}>
        <span className="capture-dot" />
        <span className="capture-title">记一笔</span>
        {tags.length > 0 ? (
          <span className="capture-tags">
            {tags.slice(0, 3).map((t) => (
              <span key={t} className="composer-chip">
                #{t}
              </span>
            ))}
          </span>
        ) : (
          <span className="capture-hint">写 #标签 就能归类</span>
        )}
        <span className="capture-keys">Enter 保存 · Esc 关闭</span>
        <button className="capture-close" style={noDrag} onClick={close} title="关闭（Esc）">
          ×
        </button>
      </div>

      <div className="capture-box">
        <MarkdownSurface
          ref={editorRef}
          preset="capture"
          value={text}
          onChange={setText}
          onSave={() => void save()}
          onEscape={close}
          autoFocus
          placeholder="随手记点什么…（- [ ] 就是待办）"
        />
      </div>

      <div className="capture-foot">
        <div className="capture-foot-right">
          {error ? <span className="snip-error">{error}</span> : null}
          <button className="btn btn-primary btn-sm" onClick={() => void save()} disabled={busy}>
            {busy ? "保存中…" : "保存"}
          </button>
        </div>
      </div>
    </div>
  );
}
