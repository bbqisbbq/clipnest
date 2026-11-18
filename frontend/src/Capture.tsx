import { useCallback, useEffect, useRef, useState } from "react";
import { ClipService } from "./lib/api";

// 快速输入窗：全局快捷键在光标处弹出来，写一句话按 Enter 就存成备忘。
// Enter 保存、Shift+Enter 换行、Esc 关闭，不做任何其他事。

export default function Capture() {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const boxRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const off = [
      // 每次唤出都清空并聚焦
      ...([] as Array<() => void>),
    ];
    boxRef.current?.focus();
    return () => off.forEach((fn) => fn());
  }, []);

  const save = useCallback(async () => {
    const content = text.trim();
    if (!content) {
      await ClipService.CloseCapture();
      return;
    }
    setBusy(true);
    setError("");
    try {
      await ClipService.QuickMemo(content);
      setText("");
      await ClipService.CloseCapture();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [text]);

  const close = useCallback(() => {
    void ClipService.CloseCapture();
  }, []);

  return (
    <div className="capture">
      <div className="capture-head">
        <span className="capture-dot" />
        <span>记一笔</span>
        <button className="capture-close" onClick={close} title="关闭（Esc）">
          ×
        </button>
      </div>
      <textarea
        ref={boxRef}
        className="capture-input"
        placeholder="写点什么，Enter 保存 · Shift+Enter 换行 · #标签 自动归类"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            close();
            return;
          }
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            void save();
          }
        }}
      />
      <div className="capture-foot">
        {error ? <span className="snip-error">{error}</span> : <span>Enter 保存</span>}
        <span>{busy ? "保存中…" : `${text.length} 字`}</span>
      </div>
    </div>
  );
}
