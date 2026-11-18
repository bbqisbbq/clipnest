import { useMemo, useRef, useState } from "react";
import { marked } from "marked";
import MarkdownEditor from "./MarkdownEditor";
import { resolveImages } from "../lib/images";

// 底部输入卡片：只负责"记一条新的"。
// 编辑已有内容走 EntryEditor（全屏），两者不要混在一起——
// 一个要窄、要连续输入；一个要宽、要看全文。

type Props = {
  value: string;
  onChange: (next: string) => void;
  onSave: () => void;
  onImagePaste: (dataUrl: string) => void;
  busy?: boolean;
  error?: string;
  imageCache: Record<string, string>;
};

const MIN_H = 120;
const DEFAULT_H = 184;
const STORAGE_KEY = "clipnest.composerHeight";

function maxHeight(): number {
  return Math.max(MIN_H + 40, Math.round(window.innerHeight * 0.5));
}

export default function Composer(props: Props) {
  const [preview, setPreview] = useState(false);
  const [height, setHeight] = useState(() => {
    const saved = Number(localStorage.getItem(STORAGE_KEY));
    return Number.isFinite(saved) && saved >= MIN_H ? saved : DEFAULT_H;
  });
  const hostRef = useRef<HTMLDivElement>(null);

  const previewHtml = useMemo(() => {
    const raw = marked.parse(props.value, { async: false }) as string;
    return raw
      .replace(/<script[\s\S]*?<\/script>/gi, "")
      .replace(/<iframe[\s\S]*?<\/iframe>/gi, "")
      .replace(/ on[a-z]+="[^"]*"/gi, "")
      .replace(/javascript:/gi, "");
  }, [props.value]);

  const startResize = (ev: React.PointerEvent<HTMLDivElement>) => {
    ev.preventDefault();
    const startY = ev.clientY;
    const startH = height;
    const onMove = (e: PointerEvent) => {
      const next = Math.min(maxHeight(), Math.max(MIN_H, startH - (e.clientY - startY)));
      setHeight(next);
      localStorage.setItem(STORAGE_KEY, String(next));
    };
    const onUp = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      document.body.classList.remove("is-row-resizing");
    };
    document.body.classList.add("is-row-resizing");
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  };

  return (
    <div className="memo-composer" style={{ height: `${height}px` }} ref={hostRef}>
      <div
        className="memo-composer-resizer"
        role="separator"
        aria-orientation="horizontal"
        aria-label="拖动调整输入区高度"
        onPointerDown={startResize}
        onDoubleClick={() => {
          setHeight(DEFAULT_H);
          localStorage.setItem(STORAGE_KEY, String(DEFAULT_H));
        }}
        title="拖动调整高度，双击复位"
      />

      <div className="composer-box">
        {preview ? (
          <div
            className="snip-preview composer-preview"
            dangerouslySetInnerHTML={{ __html: resolveImages(previewHtml, props.imageCache) }}
          />
        ) : (
          <MarkdownEditor
            value={props.value}
            onChange={props.onChange}
            onSave={props.onSave}
            onImagePaste={props.onImagePaste}
            placeholder="随手记点什么…（Ctrl+Enter 保存）"
          />
        )}

        <div className="composer-actions">
          <span className="composer-tags">
            <span className="composer-hint">写 #标签 就能归类 · Ctrl+Enter 保存</span>
          </span>
          <div className="composer-btns">
            <button className="link-btn" onClick={() => setPreview((v) => !v)}>
              {preview ? "编辑" : "预览"}
            </button>
            <button className="link-btn" onClick={() => props.onChange("")}>
              清空
            </button>
            <button className="btn btn-primary btn-sm" onClick={props.onSave} disabled={props.busy}>
              {props.busy ? "保存中…" : "保存"}
            </button>
          </div>
        </div>
      </div>

      {props.error ? <p className="snip-error composer-error">{props.error}</p> : null}
    </div>
  );
}
