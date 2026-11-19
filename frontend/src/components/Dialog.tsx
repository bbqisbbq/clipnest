import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

// 全应用统一的确认 / 选择 / 输入弹窗。
//
// 以前同一件事有四份实现：App.tsx 的 confirm 状态、EntryDetailView 自己一套
// （leave/delete，而且"离开"是三分支：继续编辑 / 保存并返回 / 放弃修改）、
// FilterDialog 第三套（.filter-confirm），再加上 EntriesView 里的 window.prompt
// —— 那个连主题都不跟，和"主题化模态"的注释自相矛盾。
//
// 现在只有一个 provider，底座是 choose()（任意个按钮），confirm()/prompt() 都是
// 它的薄封装，所以三向选择不需要另开一套 UI。

export type DialogChoice = {
  key: string;
  label: string;
  /** ghost=次要、primary=推荐、danger=不可撤销 */
  kind?: "primary" | "ghost" | "danger";
};

export type ChooseOptions = {
  title?: string;
  message: ReactNode;
  choices: DialogChoice[];
};

export type ConfirmOptions = {
  title?: string;
  message: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** 红色危险按钮（删除、清空这类不可撤销的操作） */
  danger?: boolean;
};

type PromptOptions = {
  title?: string;
  message?: ReactNode;
  placeholder?: string;
  initialValue?: string;
  confirmLabel?: string;
};

type DialogApi = {
  choose: (o: ChooseOptions) => Promise<string | null>;
  confirm: (o: ConfirmOptions) => Promise<boolean>;
  prompt: (o: PromptOptions) => Promise<string | null>;
};

const DialogContext = createContext<DialogApi | null>(null);

type Pending =
  | { kind: "choose"; opts: ChooseOptions; resolve: (v: string | null) => void }
  | { kind: "prompt"; opts: PromptOptions; resolve: (v: string | null) => void };

export function DialogProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const [value, setValue] = useState("");
  // 同时只显示一个：后来的排队，否则两层 backdrop 会叠在一起。
  const queue = useRef<Pending[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);

  const enqueue = useCallback((p: Pending) => {
    setPending((cur) => {
      if (cur) {
        queue.current.push(p);
        return cur;
      }
      return p;
    });
  }, []);

  const choose = useCallback(
    (opts: ChooseOptions) =>
      new Promise<string | null>((resolve) => enqueue({ kind: "choose", opts, resolve })),
    [enqueue],
  );

  const api: DialogApi = {
    choose,
    confirm: async (o) => {
      const key = await choose({
        title: o.title,
        message: o.message,
        choices: [
          { key: "cancel", label: o.cancelLabel ?? "取消", kind: "ghost" },
          { key: "ok", label: o.confirmLabel ?? "确认", kind: o.danger ? "danger" : "primary" },
        ],
      });
      return key === "ok";
    },
    prompt: (opts) => {
      setValue(opts.initialValue ?? "");
      return new Promise<string | null>((resolve) => enqueue({ kind: "prompt", opts, resolve }));
    },
  };

  const close = useCallback(
    (result: string | null) => {
      if (!pending) return;
      pending.resolve(result);
      const next = queue.current.shift() ?? null;
      setPending(next);
      if (next?.kind === "prompt") setValue(next.opts.initialValue ?? "");
    },
    [pending],
  );

  // 输入框自动聚焦；Esc 一律当"取消"
  useEffect(() => {
    if (pending?.kind === "prompt") inputRef.current?.focus();
  }, [pending]);
  useEffect(() => {
    if (!pending) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close(null);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pending, close]);

  const title = pending?.opts.title;

  return (
    <DialogContext.Provider value={api}>
      {children}
      {pending ? (
        <div
          className="modal-backdrop"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) close(null);
          }}
        >
          <div className="modal" role="dialog" aria-modal="true">
            {title ? <h3 className="modal-title">{title}</h3> : null}
            {pending.opts.message ? <p>{pending.opts.message}</p> : null}
            {pending.kind === "prompt" ? (
              <input
                ref={inputRef}
                className="input modal-input"
                value={value}
                placeholder={pending.opts.placeholder}
                onChange={(e) => setValue(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") close(value);
                }}
              />
            ) : null}
            <div className="modal-actions">
              {pending.kind === "prompt" ? (
                <button className="btn btn-ghost" onClick={() => close(null)}>
                  取消
                </button>
              ) : null}
              {pending.kind === "choose"
                ? pending.opts.choices.map((c) => (
                    <button
                      key={c.key}
                      className={`btn ${
                        c.kind === "danger"
                          ? "btn-danger"
                          : c.kind === "primary"
                            ? "btn-primary"
                            : "btn-ghost"
                      }`}
                      onClick={() => close(c.key)}
                    >
                      {c.label}
                    </button>
                  ))
                : null}
              {pending.kind === "prompt" ? (
                <button className="btn btn-primary" onClick={() => close(value)}>
                  {pending.opts.confirmLabel ?? "确认"}
                </button>
              ) : null}
            </div>
          </div>
        </div>
      ) : null}
    </DialogContext.Provider>
  );
}

function useDialog(): DialogApi {
  const ctx = useContext(DialogContext);
  if (!ctx) throw new Error("Dialog hooks 必须在 DialogProvider 里使用");
  return ctx;
}

export function useConfirm() {
  return useDialog().confirm;
}

export function useChoose() {
  return useDialog().choose;
}

export function usePrompt() {
  return useDialog().prompt;
}
