import { useCallback, useEffect, useMemo, useState } from "react";
import { marked } from "marked";
import { ClipService, type Snippet } from "../lib/api";
import { formatRelative } from "../lib/format";
import { IconTrash } from "./Icons";
import { NO_GROUP } from "./Sidebar";

const KINDS: Array<{ value: string; label: string; hint: string }> = [
  { value: "text", label: "文本", hint: "复制到剪贴板；在弹窗里回车直接粘贴；缩写触发时直接打字出来" },
  { value: "password", label: "密码", hint: "同文本，但在列表里掩码显示；仍会同步；缩写触发时打字出来" },
  { value: "html", label: "富文本", hint: "粘贴为富文本（HTML，含格式）；Word / 邮件里保留格式" },
  { value: "image", label: "图片", hint: "粘贴 PNG（从剪贴板抓一张）；缩写触发时直接粘贴图片" },
  { value: "file", label: "文件", hint: "一行一个路径；单个目录 = 打开它，文件 = 复制到剪贴板；缩写触发时把路径打出来" },
  { value: "url", label: "网址", hint: "回车用默认浏览器打开，Ctrl+回车只复制；缩写触发时打网址出来" },
  { value: "app", label: "程序", hint: "启动程序，可带参数，例如 code E:\\proj；缩写触发时直接启动" },
  { value: "command", label: "命令", hint: "用 cmd /c 执行，返回首行输出；缩写触发时直接执行（记事本里也会执行，留意）" },
];

type Props = {
  snippets: Snippet[];
  groups: string[];
  // 侧栏二级筛选（"" = 全部，NO_GROUP = 未分组）
  group: string;
  // 从剪贴板历史「编辑为片段」时带过来的草稿
  initial?: { snippet: Snippet; imageData: string; seq: number } | null;
  onReload: () => Promise<void>;
  onNotify: (message: string) => void;
};

const EMPTY: Snippet = {
  id: "",
  kind: "text" as Snippet["kind"],
  title: "",
  trigger: "",
  group: "",
  body: "",
  hash: "",
  size: 0,
  createdAt: "",
  updatedAt: "",
  useCount: 0,
  pinned: false,
  caseSensitive: false,
  keepTrigger: false,
  apps: [],
  excludeApps: [],
};

// splitCsv parses the process-list inputs ("code.exe, winword.exe").
function splitCsv(value: string): string[] {
  return value
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean);
}

export default function SnippetsView(props: Props) {
  const [draft, setDraft] = useState<Snippet>(EMPTY);
  const [imageData, setImageData] = useState("");
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState(false);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const group = props.group;
    return props.snippets.filter((sp) => {
      if (group === NO_GROUP ? !!sp.group : group ? sp.group !== group : false) return false;
      if (!q) return true;
      return (
        sp.title.toLowerCase().includes(q) ||
        (sp.trigger ?? "").toLowerCase().includes(q) ||
        sp.body.toLowerCase().includes(q) ||
        (sp.group ?? "").toLowerCase().includes(q)
      );
    });
  }, [props.snippets, query, props.group]);

  // 纯本地内容，做最小化清理即可：去掉脚本/iframe/内联事件与 javascript: 链接
  const previewHtml = useMemo(() => {
    const raw = marked.parse(draft.body ?? "", { async: false }) as string;
    return raw
      .replace(/<script[\s\S]*?<\/script>/gi, "")
      .replace(/<iframe[\s\S]*?<\/iframe>/gi, "")
      .replace(/ on[a-z]+="[^"]*"/gi, "")
      .replace(/javascript:/gi, "");
  }, [draft.body]);

  const insertVariable = useCallback((token: string) => {
    setDraft((cur) => ({ ...cur, body: `${cur.body ?? ""}${token}` }));
  }, []);

  // 「编辑为片段」：侧栏之外的入口把历史内容塞进编辑器
  useEffect(() => {
    if (!props.initial) return;
    setDraft({ ...props.initial.snippet });
    setImageData(props.initial.imageData ?? "");
    setError("");
    setPreview(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.initial?.seq]);

  const pick = useCallback((sp: Snippet) => {
    setDraft({ ...sp });
    setImageData("");
    setError("");
  }, []);

  const save = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      const now = new Date().toISOString();
      const saved = await ClipService.SaveSnippet(
        {
          ...draft,
          body: draft.body ?? "",
          createdAt: draft.createdAt || now,
          updatedAt: draft.updatedAt || now,
        },
        imageData,
      );
      setDraft({ ...saved });
      setImageData("");
      await props.onReload();
      props.onNotify("片段已保存");
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [draft, imageData, props]);

  const remove = useCallback(async () => {
    if (!draft.id) return;
    setBusy(true);
    try {
      await ClipService.DeleteSnippet(draft.id);
      setDraft(EMPTY);
      await props.onReload();
      props.onNotify("片段已删除");
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [draft.id, props]);

  const run = useCallback(async () => {
    if (!draft.id) return;
    try {
      const msg = await ClipService.TestSnippet(draft.id);
      props.onNotify(msg || "已执行");
    } catch (err) {
      props.onNotify(`执行失败：${String(err)}`);
    }
  }, [draft.id, props]);

  // 图片片段：用「从剪贴板抓一张」最省事
  const grabImage = useCallback(async () => {
    try {
      const png = await ClipService.GrabClipboardImage();
      if (!png) {
        props.onNotify("剪贴板里没有图片");
        return;
      }
      setImageData(png);
      props.onNotify("已读取剪贴板图片，保存后生效");
    } catch (err) {
      props.onNotify(`读取失败：${String(err)}`);
    }
  }, [props]);

  const isImage = draft.kind === "image";
  const kindHint = KINDS.find((k) => k.value === draft.kind)?.hint ?? "";

  return (
    <div className="snippets">
      <div className="snip-list">
        <div className="snip-search">
          <input
            className="input"
            placeholder="搜索片段 / 缩写…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <button className="btn btn-primary btn-sm" onClick={() => { setDraft(EMPTY); setImageData(""); }}>
            新建
          </button>
        </div>


        <div className="snip-rows">
          {filtered.length === 0 ? (
            <p className="snip-empty">
              还没有片段。把常用的回复、地址、提示词、命令存成片段，配一个缩写（例如 <code>addr-</code>）
              就能随时调用。
            </p>
          ) : (
            filtered.map((sp) => (
              <button
                key={sp.id}
                className={`snip-row ${sp.id === draft.id ? "is-active" : ""}`}
                onClick={() => pick(sp)}
              >
                <span className="snip-row-main">
                  <span className="snip-title">{sp.title}</span>
                  <span className="snip-meta">
                    {sp.trigger ? <code className="snip-trigger">{sp.trigger}</code> : null}
                    {sp.group ? <span>{sp.group}</span> : null}
                    <span>{formatRelative(sp.updatedAt)}</span>
                    {sp.useCount ? <span>用 {sp.useCount} 次</span> : null}
                  </span>
                </span>
                <span className="snip-kind">{KINDS.find((k) => k.value === sp.kind)?.label ?? sp.kind}</span>
              </button>
            ))
          )}
        </div>
      </div>

      <div className="snip-editor">
        <div className="snip-editor-head">
          <h3>{draft.id ? "编辑片段" : "新建片段"}</h3>
          <div className="snip-editor-actions">
            {draft.id ? (
              <button className="btn btn-ghost btn-sm" onClick={run}>
                执行一次
              </button>
            ) : null}
            <button className="btn btn-primary btn-sm" onClick={save} disabled={busy}>
              {busy ? "保存中…" : "保存"}
            </button>
            {draft.id ? (
              <button className="btn btn-danger-ghost btn-sm" onClick={remove} disabled={busy}>
                <IconTrash size={13} />
                删除
              </button>
            ) : null}
          </div>
        </div>

        <div className="snip-fields">
          <label className="field">
            <span className="field-label">类型</span>
            <span className="field-control">
              <select
                className="input"
                value={draft.kind}
                onChange={(e) => setDraft({ ...draft, kind: e.target.value as Snippet["kind"] })}
              >
                {KINDS.map((k) => (
                  <option key={k.value} value={k.value}>
                    {k.label}
                  </option>
                ))}
              </select>
            </span>
          </label>
          <p className="snip-hint">{kindHint}</p>

          <label className="field">
            <span className="field-label">名称</span>
            <span className="field-control">
              <input
                className="input"
                placeholder="例如：办公地址"
                value={draft.title}
                onChange={(e) => setDraft({ ...draft, title: e.target.value })}
              />
            </span>
          </label>

          <label className="field">
            <span className="field-label">缩写</span>
            <span className="field-control">
              <input
                className="input is-trigger"
                placeholder="例如 addr"
                value={draft.trigger}
                onChange={(e) => setDraft({ ...draft, trigger: e.target.value })}
              />
            </span>
          </label>
          <p className="snip-hint">
            打 <code>{draft.trigger || "addr"}</code> 后按空格 / 回车 / 标点即展开（终止符可在设置里改）
          </p>

          <div className="field is-stacked">
            <span className="field-label">触发选项</span>
            <div className="field-control snip-opts">
              <label className="snip-check">
                <input
                  type="checkbox"
                  checked={draft.caseSensitive ?? false}
                  onChange={(e) => setDraft({ ...draft, caseSensitive: e.target.checked })}
                />
                区分大小写
              </label>
              <label className="snip-check">
                <input
                  type="checkbox"
                  checked={draft.keepTrigger ?? false}
                  onChange={(e) => setDraft({ ...draft, keepTrigger: e.target.checked })}
                />
                保留缩写（在后面追加）
              </label>
            </div>
          </div>

          <label className="field">
            <span className="field-label">仅在此</span>
            <span className="field-control">
              <input
                className="input"
                placeholder="只在列出的程序里展开，逗号分隔，如 code.exe,winword.exe（留空 = 所有程序）"
                value={(draft.apps ?? []).join(", ")}
                onChange={(e) => setDraft({ ...draft, apps: splitCsv(e.target.value) })}
              />
            </span>
          </label>

          <label className="field">
            <span className="field-label">排除</span>
            <span className="field-control">
              <input
                className="input"
                placeholder="在这些程序里不展开，如 windowsterminal.exe,mstsc.exe"
                value={(draft.excludeApps ?? []).join(", ")}
                onChange={(e) => setDraft({ ...draft, excludeApps: splitCsv(e.target.value) })}
              />
            </span>
          </label>

          <label className="field">
            <span className="field-label">分组</span>
            <span className="field-control">
              <input
                className="input"
                placeholder="例如：客户 / 写作"
                value={draft.group}
                onChange={(e) => setDraft({ ...draft, group: e.target.value })}
              />
            </span>
          </label>

          {isImage ? (
            <div className="field is-stacked">
              <span className="field-label">图片</span>
              <div className="field-control">
                <button className="btn btn-ghost btn-sm" onClick={grabImage}>
                  从剪贴板抓取图片
                </button>
                {imageData ? <span className="snip-ok">已就绪</span> : null}
              </div>
            </div>
          ) : (
            <div className="field is-stacked">
              <span className="field-label">
                内容
                <button className="link-btn" onClick={() => setPreview((v) => !v)}>
                  {preview ? "编辑" : "预览"}
                </button>
              </span>
              <div className="var-chips">
                {["{{date}}", "{{time}}", "{{datetime}}", "{{clipboard}}", "{{cursor}}"].map((token) => (
                  <button key={token} className="chip" onClick={() => insertVariable(token)} title="插入到内容末尾">
                    {token}
                  </button>
                ))}
              </div>
              {preview ? (
                <div className="snip-preview" dangerouslySetInnerHTML={{ __html: previewHtml }} />
              ) : (
              <textarea
                className="input is-area"
                rows={draft.kind === "text" || draft.kind === "password" ? 10 : 4}
                value={draft.body}
                placeholder={
                  draft.kind === "url"
                    ? "https://…"
                    : draft.kind === "file"
                      ? "D:\\工作\\项目\n（一行一个路径）"
                      : draft.kind === "app"
                        ? "code E:\\default\\clipnest"
                        : draft.kind === "command"
                          ? "git status"
                          : "支持 Markdown"
                }
                onChange={(e) => setDraft({ ...draft, body: e.target.value })}
              />
              )}
            </div>
          )}

          {error ? <p className="snip-error">{error}</p> : null}
        </div>
      </div>
    </div>
  );
}
