import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import FilterRuleEditor from "./FilterRuleEditor";
import {
  IconAll,
  IconFiles,
  IconFolder,
  IconImage,
  IconLink,
  IconMemo,
  IconPin,
  IconSnippet,
  IconStar,
  IconText,
  IconTodo,
} from "./Icons";
import { ClipService, type EntryFilter, type EntryQuery, type FilterGroup } from "../lib/api";
import { useConfirm } from "./Dialog";

// 筛选器编辑弹窗（主题化模态，替代原来的 window.prompt）。
//
// 三件事：改名/换图标、编辑 and/or/not 规则、实时看"命中几条"。
// 内置筛选器的规则不给改 —— 它们的 query 是代码里的扁平字段，
// 想改语义就"复制为新筛选器"（侧栏 ⋯ 菜单里）。

const ICONS: { name: string; render: (size: number) => JSX.Element }[] = [
  { name: "all", render: (s) => <IconAll size={s} /> },
  { name: "memo", render: (s) => <IconMemo size={s} /> },
  { name: "pin", render: (s) => <IconPin size={s} /> },
  { name: "snippet", render: (s) => <IconSnippet size={s} /> },
  { name: "todo", render: (s) => <IconTodo size={s} /> },
  { name: "star", render: (s) => <IconStar size={s} /> },
  { name: "folder", render: (s) => <IconFolder size={s} /> },
  { name: "files", render: (s) => <IconFiles size={s} /> },
  { name: "image", render: (s) => <IconImage size={s} /> },
  { name: "text", render: (s) => <IconText size={s} /> },
  { name: "link", render: (s) => <IconLink size={s} /> },
];

type Props = {
  filter: EntryFilter;
  isNew: boolean;
  onClose: () => void;
  onSaved: (f: EntryFilter) => void;
  onDeleted: (id: string) => void;
  onNotify: (msg: string) => void;
};

function emptyRule(): FilterGroup {
  return { op: "and", conditions: [{ field: "tag", op: "is", value: "" }] };
}

export default function FilterDialog({ filter, isNew, onClose, onSaved, onDeleted, onNotify }: Props) {
  const [name, setName] = useState(filter.name);
  const [icon, setIcon] = useState(filter.icon || "memo");
  const [rule, setRule] = useState<FilterGroup>(filter.query?.rule ?? emptyRule());
  const [fields, setFields] = useState<Awaited<ReturnType<typeof ClipService.GetFilterFields>>>([]);
  const [hits, setHits] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const confirmDialog = useConfirm();

  const ruleDisabled = filter.builtin && !isNew;
  const timer = useRef<number | null>(null);

  useEffect(() => {
    void ClipService.GetFilterFields()
      .then((f) => setFields(f ?? []))
      .catch((err) => setError(String(err)));
  }, []);

  const previewQuery = useMemo<EntryQuery>(
    () => ({ ...(filter.query ?? {}), rule: ruleDisabled ? filter.query?.rule ?? null : rule }) as EntryQuery,
    [filter.query, rule, ruleDisabled],
  );

  // 命中数：输入停下 300ms 再查，别每敲一个字查一次库
  useEffect(() => {
    if (timer.current) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      void ClipService.CountEntriesWith(previewQuery)
        .then((n) => setHits(Number(n) || 0))
        .catch(() => setHits(null));
    }, 300);
    return () => {
      if (timer.current) window.clearTimeout(timer.current);
    };
  }, [previewQuery]);

  const save = useCallback(async () => {
    if (!name.trim()) {
      setError("名称不能为空");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const saved = await ClipService.SaveFilter({
        ...filter,
        id: isNew ? "" : filter.id,
        name: name.trim(),
        icon,
        query: previewQuery,
      } as never);
      onSaved(saved);
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [filter, icon, isNew, name, onSaved, previewQuery]);

  const remove = useCallback(async () => {
    try {
      await ClipService.DeleteFilter(filter.id);
      onNotify(`已删除筛选器「${filter.name}」`);
      onDeleted(filter.id);
    } catch (err) {
      setError(String(err));
    }
  }, [filter.id, filter.name, onDeleted, onNotify]);

  // 关窗前确认：手滑点到遮罩/按 Esc 不该把刚拼好的规则整份丢掉。
  const dirty =
    name.trim() !== (filter.name ?? "").trim() ||
    icon !== (filter.icon || "memo") ||
    (!!isNew && (rule.conditions?.length ?? 0) > 0);
  const requestClose = useCallback(async () => {
    if (!dirty) {
      onClose();
      return;
    }
    if (
      await confirmDialog({
        message: "这份筛选器还没保存，确定放弃修改？",
        danger: true,
        confirmLabel: "放弃修改",
      })
    ) {
      onClose();
    }
  }, [dirty, confirmDialog, onClose]);

  // Esc 关闭（和上面的遮罩点击走同一套判断）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      e.stopImmediatePropagation();
      void requestClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [requestClose]);

  return (
    <div className="modal-backdrop" onClick={() => void requestClose()}>
      <div
        className="modal filter-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="filter-dialog-title"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="filter-dialog-head">
          <h3 id="filter-dialog-title">{isNew ? "新建筛选器" : "编辑筛选器"}</h3>
          {filter.builtin ? <span className="filter-tag">内置</span> : null}
          <button className="filter-close" onClick={() => void requestClose()} title="关闭">
            ×
          </button>
        </header>

        <div className="filter-dialog-body">
          <div className="filter-row">
            <label className="filter-label">名称</label>
            <input
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="例如：本周未完成的工作"
              autoFocus
            />
          </div>

          <div className="filter-row">
            <label className="filter-label">图标</label>
            <div className="filter-icons">
              {ICONS.map((it) => (
                <button
                  key={it.name}
                  className={`filter-icon ${icon === it.name ? "is-active" : ""}`}
                  onClick={() => setIcon(it.name)}
                  title={it.name}
                  type="button"
                >
                  {it.render(14)}
                </button>
              ))}
            </div>
          </div>

          <div className="filter-row">
            <label className="filter-label">规则</label>
            <div className="filter-rule-wrap">
              {ruleDisabled ? (
                <p className="filter-note">
                  内置筛选器的规则由代码固定，不能改。想按自己的条件来，用侧栏这一项的
                  <b> ⋯ → 复制为新筛选器</b>。
                </p>
              ) : null}
              <FilterRuleEditor
                group={rule}
                fields={fields ?? []}
                onChange={setRule}
                disabled={ruleDisabled}
              />
            </div>
          </div>

          <div className="filter-preview">
            命中 <b>{hits === null ? "…" : hits}</b> 条
            {hits === 0 ? <span className="filter-preview-hint">（条件可能太严，或还没这类内容）</span> : null}
          </div>

          {error ? <p className="snip-error">{error}</p> : null}
        </div>

        <footer className="filter-dialog-foot">
          {!isNew && filter.deletable ? (
            <button
              className="link-btn is-danger"
              onClick={() =>
                void (async () => {
                  if (
                    await confirmDialog({
                      message: `删除筛选器「${filter.name}」？条目本身不受影响。`,
                      danger: true,
                      confirmLabel: "确认删除",
                    })
                  ) {
                    await remove();
                  }
                })()
              }
            >
              删除这个筛选器
            </button>
          ) : null}
          <span className="filter-foot-spacer" />
          <button className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button className="btn btn-primary" onClick={() => void save()} disabled={busy}>
            {busy ? "保存中…" : "保存"}
          </button>
        </footer>
      </div>
    </div>
  );
}
