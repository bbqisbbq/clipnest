import { useCallback, useMemo, useState } from "react";
import { marked } from "marked";
import MarkdownEditor from "./MarkdownEditor";
import SnippetFields from "./SnippetFields";
import { ClipService, type Entry } from "../lib/api";
import { resolveImages } from "../lib/images";

// 条目的完整编辑器：全屏覆盖，不再借用底部输入框。
//
// 为什么单独做：编辑一条已有内容和"记一条新的"是两件事——前者需要看到全文、
// 需要片段字段、需要改能力开关；后者只要一个能连续输入的窄框。

type Props = {
  entry: Entry;
  imageCache: Record<string, string>;
  onClose: () => void;
  onSaved: () => void;
  onNotify: (msg: string) => void;
};

export default function EntryEditor({ entry, imageCache, onClose, onSaved, onNotify }: Props) {
  const [draft, setDraft] = useState(entry.content);
  const [preview, setPreview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [actions, setActions] = useState<string[]>(entry.actions ?? []);
  const [trigger, setTrigger] = useState(entry.triggerKey ?? "");

  const dirty =
    draft !== entry.content ||
    trigger !== (entry.triggerKey ?? "") ||
    JSON.stringify([...actions].sort()) !== JSON.stringify([...(entry.actions ?? [])].sort());

  const previewHtml = useMemo(() => {
    const raw = marked.parse(draft, { async: false }) as string;
    return raw
      .replace(/<script[\s\S]*?<\/script>/gi, "")
      .replace(/<iframe[\s\S]*?<\/iframe>/gi, "")
      .replace(/ on[a-z]+="[^"]*"/gi, "")
      .replace(/javascript:/gi, "");
  }, [draft]);

  const toggleAction = (name: string) => {
    setActions((prev) => (prev.includes(name) ? prev.filter((a) => a !== name) : [...prev, name]));
  };

  const save = useCallback(async () => {
    if (!draft.trim()) {
      setError("内容不能为空");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await ClipService.SaveEntry({
        ...entry,
        content: draft,
        actions,
        triggerKey: trigger.trim(),
      } as never);
      onNotify("已保存");
      onSaved();
      onClose();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [draft, actions, trigger, entry, onNotify, onSaved, onClose]);

  const remove = useCallback(async () => {
    if (!window.confirm("删除这条内容？")) return;
    try {
      await ClipService.DeleteEntry(entry.id);
      onNotify("已删除");
      onSaved();
      onClose();
    } catch (err) {
      setError(String(err));
    }
  }, [entry.id, onNotify, onSaved, onClose]);

  return (
    <div className="editor-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="editor-panel">
        <header className="editor-head">
          <h3>编辑内容</h3>
          <div className="editor-head-actions">
            <button className="link-btn" onClick={() => setPreview((v) => !v)}>
              {preview ? "编辑" : "预览"}
            </button>
            <button className="link-btn" onClick={onClose}>
              关闭
            </button>
            <button className="btn btn-primary btn-sm" onClick={save} disabled={busy || !dirty}>
              {busy ? "保存中…" : dirty ? "保存" : "已保存"}
            </button>
          </div>
        </header>

        <div className="editor-body">
          {preview ? (
            <div
              className="editor-preview"
              dangerouslySetInnerHTML={{ __html: resolveImages(previewHtml, imageCache) }}
            />
          ) : (
            <MarkdownEditor
              value={draft}
              onChange={setDraft}
              onSave={() => void save()}
              autoFocus
              placeholder="内容支持 Markdown，正文里写 #标签 就能归类"
            />
          )}
        </div>

        <div className="editor-actions">
          <div className="editor-action-row">
            <span className="sf-label">能力</span>
            <label className="sf-check">
              <input
                type="checkbox"
                checked={actions.includes("clipboard")}
                onChange={() => toggleAction("clipboard")}
              />
              剪贴板（出现在快速粘贴列表）
            </label>
            <label className="sf-check">
              <input
                type="checkbox"
                checked={actions.includes("snippet") || trigger !== ""}
                onChange={() => {
                  toggleAction("snippet");
                  if (actions.includes("snippet")) setTrigger("");
                }}
              />
              片段（可用缩写触发）
            </label>
            {(actions.includes("snippet") || trigger !== "") && (
              <>
                <span className="sf-label">缩写</span>
                <input
                  className="input is-mono sf-trigger"
                  placeholder="例如 addr-"
                  value={trigger}
                  onChange={(e) => setTrigger(e.target.value)}
                />
              </>
            )}
            <button className="link-btn is-danger editor-delete" onClick={remove}>
              删除这条
            </button>
          </div>

          {entry.source === "clipboard" ? (
            <p className="composer-hint">
              采集自 {entry.sourceApp || "剪贴板"} · 复制 {entry.copyCount || 1} 次
            </p>
          ) : null}

          {/* 片段的其他设置（区分大小写 / 仅在 / 排除 / 分组）单独保存 */}
          {entry.triggerKey || trigger ? (
            <SnippetFields
              entry={{ ...entry, triggerKey: trigger }}
              onSaved={onSaved}
              onNotify={onNotify}
            />
          ) : null}
        </div>

        {error ? <p className="snip-error editor-error">{error}</p> : null}
      </div>
    </div>
  );
}
