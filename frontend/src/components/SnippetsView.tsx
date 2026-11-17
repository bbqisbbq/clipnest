import { useCallback, useMemo, useState } from "react";
import { ClipService, type Snippet } from "../lib/api";
import { formatRelative } from "../lib/format";
import { IconTrash } from "./Icons";

const KINDS: Array<{ value: string; label: string; hint: string }> = [
  { value: "text", label: "文本", hint: "复制到剪贴板；在弹窗里回车直接粘贴" },
  { value: "password", label: "密码", hint: "同文本，但在列表里掩码显示；仍会同步" },
  { value: "image", label: "图片", hint: "粘贴 PNG（从剪贴板抓一张）" },
  { value: "file", label: "文件", hint: "一行一个路径；单个目录 = 打开它，文件 = 复制到剪贴板" },
  { value: "url", label: "网址", hint: "回车用默认浏览器打开，Ctrl+回车只复制" },
  { value: "app", label: "程序", hint: "启动程序，可带参数，例如 code E:\\proj" },
  { value: "command", label: "命令", hint: "用 cmd /c 执行，返回首行输出" },
];

type Props = {
  snippets: Snippet[];
  groups: string[];
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
};

export default function SnippetsView(props: Props) {
  const [draft, setDraft] = useState<Snippet>(EMPTY);
  const [imageData, setImageData] = useState("");
  const [query, setQuery] = useState("");
  const [group, setGroup] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return props.snippets.filter((sp) => {
      if (group && sp.group !== group) return false;
      if (!q) return true;
      return (
        sp.title.toLowerCase().includes(q) ||
        (sp.trigger ?? "").toLowerCase().includes(q) ||
        sp.body.toLowerCase().includes(q) ||
        (sp.group ?? "").toLowerCase().includes(q)
      );
    });
  }, [props.snippets, query, group]);

  const pick = useCallback((sp: Snippet) => {
    setDraft({ ...sp });
    setImageData("");
    setError("");
  }, []);

  const save = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      const saved = await ClipService.SaveSnippet({ ...draft, body: draft.body ?? "" }, imageData);
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

        <div className="snip-groups">
          <button className={`chip ${group === "" ? "is-on" : ""}`} onClick={() => setGroup("")}>
            全部 {props.snippets.length}
          </button>
          {props.groups.map((g) => (
            <button key={g} className={`chip ${group === g ? "is-on" : ""}`} onClick={() => setGroup(g)}>
              {g}
            </button>
          ))}
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
                placeholder="例如 addr（结尾加 - 触发）"
                value={draft.trigger}
                onChange={(e) => setDraft({ ...draft, trigger: e.target.value })}
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
              <span className="field-label">内容</span>
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
            </div>
          )}

          {error ? <p className="snip-error">{error}</p> : null}
        </div>
      </div>
    </div>
  );
}
