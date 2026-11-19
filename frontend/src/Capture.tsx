import { useCallback, useEffect, useRef, useState } from "react";
import MarkdownSurface, { type MarkdownSurfaceHandle, warmUpMarkdown } from "./components/MarkdownSurface";
import { ClipService } from "./lib/api";

// 快速输入窗（Ctrl+Shift+N）：光标处弹出来，写完 Enter 保存、Esc 关闭。
//
// 几个曾经踩过的点，这里都显式处理：
//   · Esc —— 编辑器会吞按键，必须走它的 esc 回调（onEscape）而不是外层 onKeyDown
//   · 自动聚焦 —— 窗口刚 Show 时还没拿到焦点，要等 window focus 再 focus 编辑器
//   · 预热 —— 这个窗口随应用一起创建，挂载后立刻把 lute 烧热，否则第一次唤起
//     要等 250ms 的引擎初始化（实测数据见 docs/design 3.2.1）
//   · 界面 —— 和主界面输入卡片同一套 token，顶部一条细把手，底部工具条

type Tool = {
  key: string;
  label: string;
  title: string;
  wrap?: [string, string];
  insert?: string;
};

const TOOLS: Tool[] = [
  { key: "tag", label: "#", title: "插入标签", insert: "#" },
  { key: "bold", label: "B", title: "加粗", wrap: ["**", "**"] },
  { key: "italic", label: "I", title: "斜体", wrap: ["*", "*"] },
  { key: "code", label: "</>", title: "行内代码", wrap: ["`", "`"] },
  { key: "list", label: "•", title: "列表项", insert: "- " },
  { key: "todo", label: "☐", title: "待办", insert: "- [ ] " },
  { key: "quote", label: "❝", title: "引用", insert: "> " },
];

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

  const applyTool = useCallback((tool: Tool) => {
    const api = editorRef.current;
    if (!api) return;
    if (tool.wrap) api.wrap(tool.wrap[0], tool.wrap[1]);
    else if (tool.insert) api.insert(tool.insert);
    api.focus();
  }, []);

  return (
    <div className="capture">
      <div className="capture-head">
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
        <button className="capture-close" onClick={close} title="关闭（Esc）">
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
        <div className="capture-tools">
          {TOOLS.map((tool) => (
            <button
              key={tool.key}
              className={`capture-tool ${tool.key === "bold" ? "is-strong" : ""} ${
                tool.key === "italic" ? "is-em" : ""
              }`}
              title={tool.title}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => applyTool(tool)}
            >
              {tool.label}
            </button>
          ))}
        </div>
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
