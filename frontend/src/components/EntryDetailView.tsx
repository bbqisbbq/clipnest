import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import MarkdownSurface, { type MarkdownSurfaceHandle } from "./MarkdownSurface";
import SnippetFields from "./SnippetFields";
import { ClipService, type Entry } from "../lib/api";

// 条目详情：占右侧整块区域，不是一个浮层。
//
// 为什么不再用弹窗：编辑一条长内容时，用户需要同时看到"我改的是哪一条、属于哪个
// 筛选器、什么时候收的"，还要能随时点一下左边的列表换一条。浮层把上下文全遮住，
// 而且 Esc/返回的行为跟应用的导航不是一个模型。
//
// 分工：列表与滚动位置留在 EntriesView（它不卸载），这里只管一条内容——
// 草稿、脏标记、保存、能力开关、删除。

type Props = {
  entry: Entry;
  /** 面包屑上的来源，用当前筛选器名 */
  backLabel: string;
  onBack: () => void;
  onSaved: (next: Entry) => void;
  onDeleted: () => void;
  onNotify: (msg: string) => void;
  /** 把「有未保存修改」这件事告诉 App：它要管侧栏切筛选器时的拦截 */
  onDirtyChange?: (dirty: boolean) => void;
};

function timeLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function sourceLabel(e: Entry): string {
  if (e.source === "clipboard") return e.sourceApp ? `采集自 ${e.sourceApp}` : "采集自剪贴板";
  if (e.source === "import") return "导入";
  return "手写";
}

export default function EntryDetailView(props: Props) {
  const { entry } = props;
  const editorRef = useRef<MarkdownSurfaceHandle | null>(null);

  const [draft, setDraft] = useState(entry.content);
  const [actions, setActions] = useState<string[]>(entry.actions ?? []);
  const [trigger, setTrigger] = useState(entry.triggerKey ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [savedAt, setSavedAt] = useState<string>("");
  const [confirm, setConfirm] = useState<null | "leave" | "delete">(null);

  // 切换条目时重置草稿
  useEffect(() => {
    setDraft(entry.content);
    setActions(entry.actions ?? []);
    setTrigger(entry.triggerKey ?? "");
    setError("");
    setSavedAt("");
  }, [entry.id, entry.content, entry.actions, entry.triggerKey]);

  const dirty = useMemo(() => {
    const sameActions =
      JSON.stringify([...actions].sort()) === JSON.stringify([...(entry.actions ?? [])].sort());
    return draft !== entry.content || trigger !== (entry.triggerKey ?? "") || !sameActions;
  }, [draft, actions, trigger, entry]);

  useEffect(() => {
    props.onDirtyChange?.(dirty);
  }, [dirty, props]);

  const save = useCallback(async () => {
    const content = (editorRef.current?.getValue() ?? draft).trim();
    if (!content) {
      setError("内容不能为空");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const saved = await ClipService.SaveEntry({
        ...entry,
        content,
        actions,
        triggerKey: trigger.trim(),
      } as never);
      setDraft(saved.content);
      setActions(saved.actions ?? []);
      setTrigger(saved.triggerKey ?? "");
      setSavedAt(timeLabel(saved.updatedAt));
      props.onSaved(saved);
      // IR 模式会把正文按 Markdown 规范重排一遍（设计文档 3.2.2）。第一次保存时
      // 说一句，免得用户以为内容是别人改的。
      props.onNotify(content !== saved.content ? "已保存（格式已按 Markdown 规范整理）" : "已保存");
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [actions, draft, entry, props, trigger]);

  const remove = useCallback(async () => {
    try {
      await ClipService.DeleteEntry(entry.id);
      props.onNotify("已删除");
      props.onDeleted();
    } catch (err) {
      setError(String(err));
    }
  }, [entry.id, props]);

  const requestBack = useCallback(() => {
    if (dirty) {
      setConfirm("leave");
      return;
    }
    props.onBack();
  }, [dirty, props]);

  const toggleAction = (name: string) => {
    setActions((prev) => (prev.includes(name) ? prev.filter((a) => a !== name) : [...prev, name]));
  };

  const pasteImage = useCallback(async (dataUrl: string) => {
    try {
      return await ClipService.SaveImage(dataUrl);
    } catch (err) {
      setError(String(err));
      return null;
    }
  }, []);

  // ↵ 保存 / Esc 返回：编辑区自己会吞键盘，所以监听放在 window 上并判断来源
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        void save();
        return;
      }
      if (e.key === "Escape" && !confirm) {
        // Vditor 在 IR 下用 Esc 退出"待选择"状态，这里只在没有浮层时接管
        e.preventDefault();
        requestBack();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [save, requestBack, confirm]);

  return (
    <section className="detail">
      <header className="detail-head">
        <button className="detail-back" onClick={requestBack} title="返回列表（Esc）">
          <span aria-hidden>←</span> {props.backLabel}
        </button>
        <span className="detail-sub">
          {timeLabel(entry.createdAt)} · {sourceLabel(entry)}
          {entry.copyCount ? ` · 复制 ${entry.copyCount} 次` : ""}
        </span>
        <div className="detail-head-right">
          {savedAt ? <span className="detail-saved">已保存 {savedAt}</span> : null}
          {dirty ? <span className="detail-dirty">未保存</span> : null}
          <button className="btn btn-primary btn-sm" onClick={() => void save()} disabled={busy || !dirty}>
            {busy ? "保存中…" : dirty ? "保存" : "已保存"}
          </button>
        </div>
      </header>

      <div className="detail-body">
        <div className="detail-editor">
          <MarkdownSurface
            ref={editorRef}
            preset="detail"
            value={draft}
            onChange={setDraft}
            onSave={() => void save()}
            onEscape={requestBack}
            onImageFile={pasteImage}
            autoFocus
            placeholder="内容支持 Markdown；写 #标签 归类，写 - [ ] 一件事 就会进待办"
          />
          {error ? <p className="snip-error detail-error">{error}</p> : null}
        </div>

        <aside className="detail-side">
          <section className="detail-block">
            <h4>能力</h4>
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
            {actions.includes("snippet") || trigger !== "" ? (
              <label className="detail-field">
                <span>缩写</span>
                <input
                  className="input is-mono"
                  placeholder="例如 addr-"
                  value={trigger}
                  onChange={(e) => setTrigger(e.target.value)}
                />
              </label>
            ) : null}
          </section>

          {entry.triggerKey || trigger ? (
            <section className="detail-block">
              <h4>片段设置</h4>
              <SnippetFields entry={{ ...entry, triggerKey: trigger }} onSaved={() => props.onSaved(entry)} onNotify={props.onNotify} />
            </section>
          ) : null}

          <section className="detail-block">
            <h4>元信息</h4>
            <dl className="detail-meta">
              <dt>创建</dt>
              <dd>{timeLabel(entry.createdAt)}</dd>
              <dt>更新</dt>
              <dd>{timeLabel(entry.updatedAt)}</dd>
              <dt>类型</dt>
              <dd>{entry.kind}</dd>
              {entry.title ? (
                <>
                  <dt>标题</dt>
                  <dd>{entry.title}</dd>
                </>
              ) : null}
              {entry.hash ? (
                <>
                  <dt>哈希</dt>
                  <dd className="is-mono">{entry.hash}</dd>
                </>
              ) : null}
            </dl>
          </section>

          <section className="detail-block detail-block--danger">
            <h4>操作</h4>
            <div className="detail-ops">
              <button className="link-btn" onClick={() => void ClipService.CopyEntry(entry.id)}>
                复制内容
              </button>
              <button className="link-btn" onClick={() => void ClipService.OpenEntry(entry.id)}>
                用系统默认程序打开
              </button>
              <button
                className="link-btn"
                onClick={async () => {
                  const next = await ClipService.SetEntryArchived(entry.id, !entry.archived);
                  props.onSaved(next);
                  props.onNotify(next.archived ? "已归档" : "已恢复");
                }}
              >
                {entry.archived ? "取消归档" : "归档"}
              </button>
              <button className="link-btn is-danger" onClick={() => setConfirm("delete")}>
                删除这条
              </button>
            </div>
          </section>
        </aside>
      </div>

      {confirm ? (
        <div className="modal-backdrop" onClick={() => setConfirm(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <p>
              {confirm === "delete"
                ? "删除这条内容？这条操作不可撤销。"
                : "这条内容有未保存的修改，怎么处理？"}
            </p>
            <div className="modal-actions">
              <button className="btn btn-ghost" onClick={() => setConfirm(null)}>
                继续编辑
              </button>
              {confirm === "leave" ? (
                <button
                  className="btn btn-primary"
                  onClick={() => {
                    setConfirm(null);
                    void save().then(() => props.onBack());
                  }}
                >
                  保存并返回
                </button>
              ) : null}
              <button
                className={`btn ${confirm === "delete" ? "btn-danger" : "btn-ghost"}`}
                onClick={() => {
                  const kind = confirm;
                  setConfirm(null);
                  if (kind === "delete") void remove();
                  else props.onBack();
                }}
              >
                {confirm === "delete" ? "确认删除" : "放弃修改"}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}
