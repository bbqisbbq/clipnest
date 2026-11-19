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
  const [confirmDelete, setConfirmDelete] = useState(false);

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
      const ok = await ClipService.DeleteFilter(filter.id);
      if (!ok) {
        setError("这个筛选器不能删除");
        return;
      }
      onNotify(`已删除筛选器「${filter.name}」`);
      onDeleted(filter.id);
    } catch (err) {
      setError(String(err));
    }
  }, [filter.id, filter.name, onDeleted, onNotify]);

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal filter-dialog" onClick={(e) => e.stopPropagation()}>
        <header className="filter-dialog-head">
          <h3>{isNew ? "新建筛选器" : "编辑筛选器"}</h3>
          {filter.builtin ? <span className="filter-tag">内置</span> : null}
          <button className="filter-close" onClick={onClose} title="关闭">
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
            <button className="link-btn is-danger" onClick={() => setConfirmDelete(true)}>
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

        {confirmDelete ? (
          <div className="modal-backdrop filter-confirm" onClick={() => setConfirmDelete(false)}>
            <div className="modal" onClick={(e) => e.stopPropagation()}>
              <p>删除筛选器「{filter.name}」？条目本身不受影响。</p>
              <div className="modal-actions">
                <button className="btn btn-ghost" onClick={() => setConfirmDelete(false)}>
                  取消
                </button>
                <button className="btn btn-danger" onClick={() => void remove()}>
                  确认删除
                </button>
              </div>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}
