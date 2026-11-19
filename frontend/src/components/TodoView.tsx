import { useCallback, useEffect, useMemo, useState } from "react";
import type { Entry, TodoRef } from "../lib/api";
import { ClipService } from "../lib/api";

// 待办收集页：把「所有条目正文里的 - [ ] 任务」摊平成一页。
//
// 三个约定（都在设计文档 1.7 里）：
//   · 勾选改的是条目正文里的那一个字符（后端 SetTodoDone），这里只做乐观更新；
//   · 点行文字跳到它所在的条目（右侧详情视图），不是弹窗；
//   · 已完成默认折叠 —— 待办页的目的是"还剩什么没做"。

type Props = {
  onOpenEntry: (entry: Entry) => void;
  onNotify: (msg: string) => void;
  onChanged: () => void;
  onGoEntries: () => void;
};

function timeLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  const today = new Date();
  const sameDay =
    d.getFullYear() === today.getFullYear() &&
    d.getMonth() === today.getMonth() &&
    d.getDate() === today.getDate();
  return sameDay
    ? `${pad(d.getHours())}:${pad(d.getMinutes())}`
    : `${d.getMonth() + 1}/${d.getDate()}`;
}

function rowKey(r: TodoRef): string {
  return `${r.entryId}:${r.item.key}`;
}

/** 行尾的出处：有标题用标题，没有就用正文首句 —— 让人知道这条待办在哪 */
function sourceLabel(r: TodoRef): string {
  const name = (r.entryTitle || r.entryPreview || "").replace(/\s+/g, " ").trim();
  const short = name.length > 28 ? `${name.slice(0, 28)}…` : name;
  return `${short} · ${timeLabel(r.entryCreatedAt)}`;
}

export default function TodoView({ onOpenEntry, onNotify, onChanged, onGoEntries }: Props) {
  const [rows, setRows] = useState<TodoRef[]>([]);
  const [loading, setLoading] = useState(true);
  const [showDone, setShowDone] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const list = (await ClipService.GetTodos({} as never)) ?? [];
      setRows(list);
      setError("");
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const open = useMemo(() => rows.filter((r) => !r.item.done), [rows]);
  const done = useMemo(() => rows.filter((r) => r.item.done), [rows]);

  const toggle = useCallback(
    async (row: TodoRef) => {
      const key = rowKey(row);
      const nextDone = !row.item.done;
      const patch = (flag: boolean) =>
        setRows((prev) =>
          prev.map((r) => (rowKey(r) === key ? { ...r, item: { ...r.item, done: flag } } : r)),
        );

      patch(nextDone); // 乐观：先动界面，失败再回滚
      try {
        await ClipService.SetTodoDone(row.entryId, row.item.key, nextDone);
        onChanged();
        if (nextDone) onNotify(`已完成：${row.item.text.slice(0, 24)}`);
      } catch (err) {
        patch(!nextDone);
        onNotify(`切换失败：${String(err)}`);
        void load();
      }
    },
    [load, onChanged, onNotify],
  );

  const jump = useCallback(
    async (row: TodoRef) => {
      try {
        const entry = await ClipService.GetEntry(row.entryId);
        onOpenEntry(entry);
      } catch (err) {
        onNotify(`打开条目失败：${String(err)}`);
        void load();
      }
    },
    [load, onNotify, onOpenEntry],
  );

  return (
    <div className="todo">
      <header className="todo-head">
        <h2>待办</h2>
        <span className="todo-hint">
          在任意条目里写 <code>- [ ] 一件事</code>，就会出现在这里；勾选改的是那行正文。
        </span>
        <span className="todo-count">
          {open.length} 未完成{done.length ? ` · ${done.length} 已完成` : ""}
        </span>
      </header>

      <div className="todo-body">
        {loading ? <p className="todo-empty">正在读…</p> : null}

        {!loading && rows.length === 0 ? (
          <div className="todo-empty">
            <p>还没有任何待办。</p>
            <p className="todo-empty-sub">
              在底部「记一条」里写一行 <code>- [ ] 买牛奶</code>，它就会出现在这里。
            </p>
            <button className="btn btn-primary btn-sm" onClick={onGoEntries}>
              去写一条
            </button>
          </div>
        ) : null}

        {open.length > 0 ? (
          <section className="todo-group">
            {open.map((row) => (
              <div className="todo-row" key={rowKey(row)}>
                <input
                  className="todo-check"
                  type="checkbox"
                  checked={false}
                  onChange={() => void toggle(row)}
                  title="标记完成"
                />
                <button
                  className="todo-text"
                  style={{ paddingLeft: `${row.item.indent * 14}px` }}
                  onClick={() => void jump(row)}
                  title="打开它所在的条目"
                >
                  {row.item.text}
                </button>
                <span className="todo-meta" title={row.entryPreview}>
                  {sourceLabel(row)}
                </span>
              </div>
            ))}
          </section>
        ) : null}

        {done.length > 0 ? (
          <section className="todo-group todo-group--done">
            <button className="todo-group-head" onClick={() => setShowDone((v) => !v)}>
              {showDone ? "收起" : "展开"}已完成（{done.length}）
            </button>
            {showDone
              ? done.map((row) => (
                  <div className="todo-row is-done" key={rowKey(row)}>
                    <input
                      className="todo-check"
                      type="checkbox"
                      checked
                      onChange={() => void toggle(row)}
                      title="取消完成"
                    />
                    <button
                      className="todo-text"
                      style={{ paddingLeft: `${row.item.indent * 14}px` }}
                      onClick={() => void jump(row)}
                      title="打开它所在的条目"
                    >
                      {row.item.text}
                    </button>
                    <span className="todo-meta" title={row.entryPreview}>
                      {sourceLabel(row)}
                    </span>
                  </div>
                ))
              : null}
          </section>
        ) : null}

        {error ? <p className="snip-error">{error}</p> : null}
      </div>
    </div>
  );
}
