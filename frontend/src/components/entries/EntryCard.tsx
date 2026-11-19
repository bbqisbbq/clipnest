import { useEffect, useRef, useState } from "react";
import { marked } from "marked";
import type { Entry } from "../../lib/api";
import { resolveImages } from "../../lib/images";

// 时间线里的一条内容卡片：正文（Markdown 渲染 + 图片换真 src）、元信息、操作按钮。
// 折叠遮罩的测量也在这里，因为"有没有溢出"是卡片自己的布局事实。

const ACTION_LABEL: Record<string, string> = { clipboard: "剪贴板", snippet: "片段" };

function timeLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function sourceLabel(e: Entry): string {
  if (e.source === "clipboard") return e.sourceApp ? `来自 ${e.sourceApp}` : "来自剪贴板";
  if (e.source === "import") return "导入";
  return "";
}

export type EntryCardProps = {
  entry: Entry;
  expanded: boolean;
  imgCache: Record<string, string>;
  onToggleExpand: () => void;
  onEdit: () => void;
  onImage: (src: string) => void;
  onTogglePin: () => void;
  onToggleArchive: () => void;
  onToggleClipboard: () => void;
  onSnippet: () => void;
  onDelete: () => void;
};

export default function EntryCard(props: EntryCardProps) {
  const { entry: e, expanded, imgCache } = props;
  const collected = e.source === "clipboard";
  const inClipboard = (e.actions ?? []).includes("clipboard");
  const html = resolveImages(marked.parse(e.content, { async: false }) as string, imgCache);

  // 单击 = 展开/收起，双击 = 打开编辑器。单击要延迟 180ms 再执行，否则双击的
  // 第一次点击已经把卡片折起来了（一开一合，看着像闪了一下）。
  const clickTimer = useRef<number | null>(null);
  useEffect(() => () => {
    if (clickTimer.current) window.clearTimeout(clickTimer.current);
  }, []);

  // 折叠遮罩只在内容真的被裁掉时才显示：短文本、小图不该被底部渐变压暗。
  // 图片是异步换进来的（data URL 高度后到），所以除了 ResizeObserver 还听
  // img 的 load 和窗口 resize。只在折叠态测量（展开后 scrollHeight 就等于
  // clientHeight，会把"有溢出"这件事忘掉，收起来就点不动了）。
  const bodyRef = useRef<HTMLDivElement>(null);
  const [overflows, setOverflows] = useState(false);
  useEffect(() => {
    const el = bodyRef.current;
    if (!el || expanded) return;
    // 阈值不能太小：只溢出 1~2px 时遮罩会白亮一道，看着像渲染坏了。
    const measure = () => setOverflows(el.scrollHeight - el.clientHeight > 8);
    measure();
    const imgs = Array.from(el.querySelectorAll("img"));
    imgs.forEach((img) => img.addEventListener("load", measure));
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    window.addEventListener("resize", measure);
    return () => {
      imgs.forEach((img) => img.removeEventListener("load", measure));
      ro.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [expanded, html]);

  const folded = !expanded && overflows;

  const handleClick = () => {
    if (clickTimer.current) return;
    clickTimer.current = window.setTimeout(() => {
      clickTimer.current = null;
      // 本来就没折起来的内容，点它不该做无反馈的空动作
      if (expanded || overflows) props.onToggleExpand();
    }, 180);
  };

  const handleDoubleClick = () => {
    if (clickTimer.current) {
      window.clearTimeout(clickTimer.current);
      clickTimer.current = null;
    }
    props.onEdit();
  };

  return (
    <article
      className={`memo-card entry-card ${collected ? "is-collected" : ""} ${
        e.pinned ? "is-pinned" : ""
      } ${overflows ? "is-collapsible" : ""} ${expanded ? "is-expanded" : ""}`}
      onClick={handleClick}
      onDoubleClick={handleDoubleClick}
      title={expanded ? "点击收起 · 双击编辑" : overflows ? "点击展开全文 · 双击编辑" : undefined}
    >
      {e.title ? <h3 className="memo-card-title">{e.title}</h3> : null}
      <div
        ref={bodyRef}
        className={`${e.kind === "image" ? "entry-image" : "memo-body"} clampable${
          folded ? " is-clipped" : ""
        }`}
        dangerouslySetInnerHTML={{ __html: html }}
        onClick={(ev) => {
          const t = ev.target as HTMLElement;
          if (t.tagName === "IMG") {
            ev.stopPropagation();
            // 图片卡片整张就是图，点图等于点卡片：折叠时先展开看全图，
            // 已展开（或本来就没折）再点才进大图。
            if (e.kind === "image" && folded) {
              props.onToggleExpand();
              return;
            }
            props.onImage((t as HTMLImageElement).src);
          }
        }}
      />

      {/* 折叠与否的显式提示：只靠底部渐变，用户看不出来这张卡片还能点开。
          只在真的被裁掉时出现，短卡片不凭空多一行。 */}
      {overflows ? (
        <button
          className={`entry-toggle ${expanded ? "is-open" : ""}`}
          onClick={(ev) => {
            ev.stopPropagation();
            props.onToggleExpand();
          }}
        >
          {expanded ? "收起 ⌃" : "展开全文 ⌄"}
        </button>
      ) : null}

      <footer className="memo-meta">
        <span>{timeLabel(e.createdAt)}</span>
        {e.todoOpen ? (
          <span className="entry-todo" title="还有未完成的任务行">
            {e.todoTotal ? `${(e.todoTotal ?? 0) - (e.todoOpen ?? 0)}/${e.todoTotal}` : ""} 待办
          </span>
        ) : null}
        {(e.actions ?? []).map((a) => (
          <span key={a} className="entry-autotag">
            {ACTION_LABEL[a] ?? a}
          </span>
        ))}
        {(e.tags ?? []).map((t) => (
          <span key={t} className="memo-tag">
            #{t}
          </span>
        ))}
        {e.triggerKey ? <span className="entry-trigger">{e.triggerKey}</span> : null}
        {collected ? <span className="memo-src">{sourceLabel(e)}</span> : null}
        <span className="memo-actions">
          <button
            className="link-btn"
            onClick={(ev) => {
              ev.stopPropagation();
              props.onEdit();
            }}
          >
            编辑
          </button>
          <button
            className={e.pinned ? "link-btn is-on" : "link-btn"}
            onClick={(ev) => {
              ev.stopPropagation();
              props.onTogglePin();
            }}
          >
            {e.pinned ? "取消置顶" : "置顶"}
          </button>
          <button
            className="link-btn"
            onClick={(ev) => {
              ev.stopPropagation();
              props.onSnippet();
            }}
          >
            {e.triggerKey ? "取消片段" : "设为片段"}
          </button>
          <button
            className={inClipboard ? "link-btn is-on" : "link-btn"}
            onClick={(ev) => {
              ev.stopPropagation();
              props.onToggleClipboard();
            }}
            title="有这项能力才会出现在快速粘贴列表里"
          >
            {inClipboard ? "移出粘贴列表" : "加入粘贴列表"}
          </button>
          <button
            className="link-btn"
            onClick={(ev) => {
              ev.stopPropagation();
              props.onToggleArchive();
            }}
          >
            {e.archived ? "恢复" : "归档"}
          </button>
          <button
            className="link-btn is-danger"
            onClick={(ev) => {
              ev.stopPropagation();
              props.onDelete();
            }}
          >
            删除
          </button>
        </span>
      </footer>
    </article>
  );
}
