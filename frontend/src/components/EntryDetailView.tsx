import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import MarkdownSurface, { type MarkdownSurfaceHandle } from "./MarkdownSurface";
import SnippetFields from "./SnippetFields";
import PrivacyToggle from "./PrivacyToggle";
import { entryClipboardPayload } from "../lib/entryClipboard";
import { ClipService, type Entry } from "../lib/api";
import { isDialogOpen, useChoose, useConfirm } from "./Dialog";
import { SNIPPET_VARS } from "../lib/snippetTemplate";

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
  // 标题：后台从正文的 `# 一级标题` 自动派生；这里只负责"手写覆盖"或"清空退回自动"
  const [title, setTitle] = useState(entry.title ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [savedAt, setSavedAt] = useState<string>("");
  const choose = useChoose();
  const confirmDialog = useConfirm();

  // 切换条目时重置草稿
  useEffect(() => {
    setDraft(entry.content);
    setActions(entry.actions ?? []);
    setTrigger(entry.triggerKey ?? "");
    setTitle(entry.title ?? "");
    setError("");
    setSavedAt("");
  }, [entry.id, entry.content, entry.actions, entry.triggerKey, entry.title]);

  const dirty = useMemo(() => {
    const sameActions =
      JSON.stringify([...actions].sort()) === JSON.stringify([...(entry.actions ?? [])].sort());
    return (
      draft !== entry.content ||
      trigger !== (entry.triggerKey ?? "") ||
      title !== (entry.title ?? "") ||
      !sameActions
    );
  }, [draft, actions, trigger, title, entry]);

  // 脏标记回调放进 ref：props 是每次父组件渲染新建的对象，直接进依赖会每帧都调一次
  const dirtyCb = useRef(props.onDirtyChange);
  dirtyCb.current = props.onDirtyChange;
  useEffect(() => {
    dirtyCb.current?.(dirty);
  }, [dirty]);

  const save = useCallback(async () => {
    const content = (editorRef.current?.getValue() ?? draft).trim();
    if (!content) {
      setError("内容不能为空");
      return;
    }
    setBusy(true);
    setError("");
    try {
      // 先取一份库里的最新值再覆盖：这份 entry 是打开详情时的快照，
      // 期间可能有同步把对端的修改拉了进来，整行回写会把它们抹掉。
      const fresh = await ClipService.GetEntry(entry.id);
      // 标题：改过就是手写（清空 = 退回自动）；没动过就原样传回去，
      // 让后端继续按正文的 `# 一级标题` 维护它（titleSource 见 entry_title.go）。
      const titleChanged = title !== (entry.title ?? "");
      const nextTitle = title.trim();
      const saved = await ClipService.SaveEntry({
        ...fresh,
        content,
        actions,
        triggerKey: trigger.trim(),
        title: nextTitle,
        titleSource: titleChanged ? (nextTitle ? "user" : "") : (fresh.titleSource ?? ""),
      } as never);
      setDraft(saved.content);
      setActions(saved.actions ?? []);
      setTrigger(saved.triggerKey ?? "");
      setTitle(saved.title ?? "");
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
  }, [actions, draft, entry, props, title, trigger]);


  // 片段设置保存后重新读一条：以前回调里传的是编辑前的 entry，
  // 会把用户刚配好的字段又写回旧值。
  const reloadEntry = useCallback(async () => {
    try {
      props.onSaved(await ClipService.GetEntry(entry.id));
    } catch (err) {
      props.onNotify(`刷新失败：${String(err)}`);
    }
  }, [entry.id, props]);

  // OCR：复制识别文字 / 手动跑一次（图片条目才有这块）
  const copyOcr = useCallback(
    async (text: string) => {
      try {
        await ClipService.CopyPlainText(text);
        props.onNotify("识别文字已复制");
      } catch (err) {
        props.onNotify(`复制失败：${String(err)}`);
      }
    },
    [props],
  );

  const runOcr = useCallback(
    async (id: string) => {
      setBusy(true);
      try {
        const text = await ClipService.OcrEntry(id, true);
        props.onNotify(text ? `识别完成：${text.length} 个字` : "这张图里没识别出文字");
        await reloadEntry();
      } catch (err) {
        props.onNotify(`识别失败：${String(err)}`);
      } finally {
        setBusy(false);
      }
    },
    [props, reloadEntry],
  );
  const remove = useCallback(async () => {
    try {
      await ClipService.DeleteEntry(entry.id);
      props.onNotify("已删除");
      props.onDeleted();
    } catch (err) {
      setError(String(err));
    }
  }, [entry.id, props]);

  // 离开时若未保存：三向选择（继续编辑 / 保存并返回 / 放弃修改）
  const requestBack = useCallback(async () => {
    if (!dirty) {
      props.onBack();
      return;
    }
    const choice = await choose({
      message: "这条内容有未保存的修改，怎么处理？",
      choices: [
        { key: "cancel", label: "继续编辑", kind: "ghost" },
        { key: "save", label: "保存并返回", kind: "primary" },
        { key: "discard", label: "放弃修改", kind: "danger" },
      ],
    });
    if (choice === "save") {
      await save();
      props.onBack();
    } else if (choice === "discard") {
      props.onBack();
    }
  }, [dirty, props, choose, save]);

  // 删除是不可撤销操作：单独问一句
  const requestDelete = useCallback(async () => {
    if (!(await confirmDialog({ message: "删除这条内容？这条操作不可撤销。", danger: true, confirmLabel: "确认删除" }))) {
      return;
    }
    await remove();
  }, [confirmDialog, remove]);

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
      if (e.key === "Escape") {
        // 有弹窗挡在前面时（三向选择"继续编辑/保存/放弃"）不接管：
        // 以前两边都处理 Esc，一次按键会"关掉弹窗 + 又弹一个"。
        if (isDialogOpen()) return;
        // Vditor 在 IR 下用 Esc 退出"待选择"状态，这里只在没有浮层时接管
        e.preventDefault();
        void requestBack();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [save, requestBack]);

  return (
    <section className="detail">
      <header className="detail-head">
        <button className="detail-back" onClick={() => void requestBack()} title="返回列表（Esc）">
          <span aria-hidden>←</span> {props.backLabel}
        </button>
        <span className="detail-sub">
          {timeLabel(entry.createdAt)} · {sourceLabel(entry)}
          {entry.copyCount ? ` · 复制 ${entry.copyCount} 次` : ""}
        </span>
        <div className="detail-head-right">
          {savedAt ? <span className="detail-saved">已保存 {savedAt}</span> : null}
          {dirty ? <span className="detail-dirty">未保存</span> : null}
          <PrivacyToggle />
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
          {/* 片段模板变量：展开成文本时才替换，普通条目用不上，所以只在片段时出现 */}
          {actions.includes("snippet") || trigger !== "" ? (
            <div className="var-chips">
              <span className="var-chips-label">模板变量</span>
              {SNIPPET_VARS.map((v) => (
                <button
                  key={v.token}
                  className="var-chip"
                  title={v.hint}
                  onClick={() => editorRef.current?.insert(v.token)}
                >
                  {v.label}
                </button>
              ))}
            </div>
          ) : null}
          {error ? <p className="snip-error detail-error">{error}</p> : null}
        </div>

        <aside className="detail-side">
          <section className="detail-block">
            <h4>标题</h4>
            <input
              className="input"
              value={title}
              maxLength={60}
              placeholder="留空则自动取正文的第一个 # 标题"
              onChange={(e) => setTitle(e.target.value)}
            />
            <p className="detail-hint">
              {entry.titleSource === "auto"
                ? "自动：来自正文的第一个一级标题（正文改了会跟着变）"
                : title.trim()
                  ? "手写：正文再改也不会覆盖"
                  : "当前没有标题"}
            </p>
          </section>

          {entry.kind === "image" ? (
            <section className="detail-block">
              <h4>图里的文字（OCR）</h4>
              {entry.ocrState === "done" ? (
                entry.ocrText ? (
                  <>
                    <p className="detail-ocr">{entry.ocrText}</p>
                    <button className="link-btn" onClick={() => void copyOcr(entry.ocrText ?? "")}>
                      复制识别文字
                    </button>
                  </>
                ) : (
                  <p className="detail-hint">这张图里没识别出文字。</p>
                )
              ) : entry.ocrState === "pending" ? (
                <p className="detail-hint">正在识别…</p>
              ) : entry.ocrState === "unsupported" ? (
                <p className="detail-hint">
                  这台机器没有可用的 OCR 引擎（缺语言包，或 PowerShell 被执行策略禁用）。
                </p>
              ) : (
                <button className="link-btn" onClick={() => void runOcr(entry.id)}>
                  {entry.ocrState === "failed" ? "上次识别失败，再试一次" : "识别这张图里的文字"}
                </button>
              )}
            </section>
          ) : null}

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
              <SnippetFields entry={{ ...entry, triggerKey: trigger }} onSaved={() => void reloadEntry()} onNotify={props.onNotify} />
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
              <button
                className="link-btn"
                onClick={() =>
                  void (async () => {
                    try {
                      // 传渲染好的 HTML：图文混排的条目要靠它图文一起粘贴出去
                      const payload = entryClipboardPayload(entry.content);
                      await ClipService.CopyEntry(entry.id, payload.html, payload.plain);
                      props.onNotify("已复制到剪贴板");
                    } catch (err) {
                      props.onNotify(`复制失败：${String(err)}`);
                    }
                  })()
                }
              >
                复制内容
              </button>
              <button
                className="link-btn"
                onClick={() =>
                  void (async () => {
                    try {
                      await ClipService.OpenEntry(entry.id);
                    } catch (err) {
                      props.onNotify(`打开失败：${String(err)}`);
                    }
                  })()
                }
              >
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
              <button className="link-btn is-danger" onClick={() => void requestDelete()}>
                删除这条
              </button>
            </div>
          </section>
        </aside>
      </div>

    </section>
  );
}
