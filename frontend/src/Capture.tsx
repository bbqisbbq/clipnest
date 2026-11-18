import { useCallback, useEffect, useRef, useState } from "react";
import MarkdownEditor, { type MarkdownEditorHandle } from "./components/MarkdownEditor";
import { ClipService } from "./lib/api";

// 快速输入窗：全局快捷键在光标处弹出来，写一句话按 Ctrl+Enter 就存成备忘。
// 和主界面的输入卡片保持同一套外观：卡片边框、底部操作行、Markdown 实时着色。

type Tool = {
  key: string;
  label: string;
  title: string;
  wrap?: [string, string];
  insert?: string;
};

// 常用工具条：都是"在光标处插入"这类轻量操作，不引入额外依赖
const TOOLS: Tool[] = [
  { key: "tag", label: "#", title: "插入标签", insert: "#" },
  { key: "bold", label: "B", title: "加粗", wrap: ["**", "**"] },
  { key: "italic", label: "I", title: "斜体", wrap: ["*", "*"] },
  { key: "code", label: "</>", title: "行内代码", wrap: ["`", "`"] },
  { key: "list", label: "•", title: "列表项", insert: "- " },
  { key: "todo", label: "☐", title: "待办", insert: "- [ ] " },
  { key: "quote", label: "❝", title: "引用", insert: "> " },
  { key: "link", label: "🔗", title: "链接", wrap: ["[", "](url)"] },
];

export default function Capture() {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const editorRef = useRef<MarkdownEditorHandle | null>(null);
  const textRef = useRef(text);
  textRef.current = text;

  useEffect(() => {
    // 标签实时预览：从正文里抽 #xxx（和保存后的派生规则一致）
    const found = new Set<string>();
    for (const m of text.matchAll(/#([\p{L}\p{N}_/-]+)/gu)) found.add(m[1]);
    setTags([...found]);
  }, [text]);

  const save = useCallback(async () => {
    const content = textRef.current.trim();
    if (!content) {
      await ClipService.CloseCapture();
      return;
    }
    setBusy(true);
    setError("");
    try {
      await ClipService.SaveEntry({ content, source: "manual", kind: "text" } as never);
      setText("");
      await ClipService.CloseCapture();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  const close = useCallback(() => {
    void ClipService.CloseCapture();
  }, []);

  // 工具条：全部走 CodeMirror 事务，格式与撤销栈都保留
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
        <span className="capture-hint">Ctrl+Enter 保存 · Esc 关闭</span>
        <button className="capture-close" onClick={close} title="关闭（Esc）">
          ×
        </button>
      </div>

      <div className="composer-box capture-box">
        <MarkdownEditor
          ref={editorRef}
          value={text}
          onChange={setText}
          onSave={() => void save()}
          autoFocus
          placeholder="随手记点什么… 正文里写 #标签 就能归类"
          onImagePaste={(dataUrl) => {
            void (async () => {
              try {
                const token = await ClipService.SaveImage(dataUrl);
                const sep = textRef.current && !textRef.current.endsWith("\n") ? "\n" : "";
                setText(textRef.current + sep + "![](" + token + ")\n");
              } catch (err) {
                setError(String(err));
              }
            })();
          }}
        />

        <div className="composer-actions capture-actions">
          <div className="capture-tools">
            {TOOLS.map((tool) => (
              <button
                key={tool.key}
                className={`capture-tool ${tool.key === "bold" ? "is-strong" : ""} ${tool.key === "italic" ? "is-em" : ""}`}
                title={tool.title}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => applyTool(tool)}
              >
                {tool.label}
              </button>
            ))}
          </div>
          <div className="composer-btns">
            {tags.length > 0 ? (
              <span className="composer-tags">
                {tags.slice(0, 3).map((t) => (
                  <span key={t} className="composer-chip">
                    #{t}
                  </span>
                ))}
              </span>
            ) : null}
            <button className="btn btn-primary btn-sm" onClick={save} disabled={busy}>
              {busy ? "保存中…" : "保存"}
            </button>
          </div>
        </div>
      </div>

      {error ? <p className="snip-error capture-error">{error}</p> : null}
    </div>
  );
}
