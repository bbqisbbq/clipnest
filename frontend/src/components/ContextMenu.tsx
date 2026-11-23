import { useCallback, useEffect, useRef, useState } from "react";
import type { MenuItem } from "../lib/entryMenu";

// 通用右键菜单。
//
// 为什么自己写而不是用系统菜单：这个应用是无边框自绘 UI（连标题栏都是画的），
// 弹一个 Win32 菜单会露出方角、浅色底和系统字体，和整体完全不是一个东西。
//
// 行为约定（照 Windows 用户的手感来）：
//   · 点外面、按 Esc、滚动整个列表 —— 都关掉；
//   · 贴到窗口右边/下边会自动翻转，不被裁掉；
//   · ↑↓ 移动高亮，Enter 执行，鼠标悬停跟键盘高亮联动；
//   · 危险项（删除）用红色，且放在最后并单独分隔。
//
// 菜单**条目**的规则不在这里：哪些操作对哪条内容有意义，全在 lib/entryMenu.ts
// （纯函数，能单独跑断言）。这里只管怎么画、怎么关。

export type { MenuItem };

export type ContextMenuProps = {
  /** 视口坐标（event.clientX / clientY） */
  x: number;
  y: number;
  items: MenuItem[];
  onClose: () => void;
};

/** 与 .ctx-menu 的宽度保持一致，用来算贴边翻转 */
const MENU_WIDTH = 208;
const EDGE = 8;

export default function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState({ left: x, top: y });
  const [active, setActive] = useState(-1);

  const selectable = items.filter((it) => it.kind !== "separator").map((it) => it);

  // 打开后量一次真实高度：靠近底边就往上翻，否则菜单会被窗口底部裁掉一半。
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const h = el.offsetHeight;
    const left = Math.max(EDGE, Math.min(x, window.innerWidth - MENU_WIDTH - EDGE));
    const top = y + h + EDGE > window.innerHeight ? Math.max(EDGE, y - h) : y;
    setPos({ left, top });
    el.focus();
  }, [x, y]);

  // 点外面 / Esc / 列表滚动都关
  useEffect(() => {
    const onDown = (ev: MouseEvent) => {
      if (!ref.current?.contains(ev.target as Node)) onClose();
    };
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.stopPropagation();
        onClose();
      }
    };
    const onScroll = () => onClose();
    document.addEventListener("mousedown", onDown, true);
    document.addEventListener("keydown", onKey, true);
    window.addEventListener("resize", onScroll);
    window.addEventListener("blur", onScroll);
    return () => {
      document.removeEventListener("mousedown", onDown, true);
      document.removeEventListener("keydown", onKey, true);
      window.removeEventListener("resize", onScroll);
      window.removeEventListener("blur", onScroll);
    };
  }, [onClose]);

  const run = useCallback(
    (it: MenuItem) => {
      if (it.kind === "separator" || it.disabled) return;
      onClose();
      it.onClick();
    },
    [onClose],
  );

  const move = useCallback(
    (step: number) => {
      if (selectable.length === 0) return;
      setActive((cur) => {
        let next = cur;
        for (let i = 0; i < selectable.length; i++) {
          next = (next + step + selectable.length) % selectable.length;
          if (!selectable[next].disabled) break;
        }
        return next;
      });
    },
    [selectable],
  );

  const onKeyDown = (ev: React.KeyboardEvent) => {
    if (ev.key === "ArrowDown") {
      ev.preventDefault();
      move(1);
    } else if (ev.key === "ArrowUp") {
      ev.preventDefault();
      move(-1);
    } else if (ev.key === "Enter") {
      ev.preventDefault();
      if (active >= 0) run(selectable[active]);
    }
  };

  return (
    <div
      ref={ref}
      className="ctx-menu"
      role="menu"
      tabIndex={-1}
      style={{ left: pos.left, top: pos.top }}
      onKeyDown={onKeyDown}
      // 菜单自己不该再弹卡片菜单
      onContextMenu={(ev) => ev.preventDefault()}
    >
      {items.map((it, i) => {
        if (it.kind === "separator") return <div key={it.id} className="ctx-sep" role="separator" />;
        const index = selectable.indexOf(it);
        return (
          <button
            key={it.id}
            type="button"
            role="menuitem"
            className={`ctx-item${it.danger ? " is-danger" : ""}${
              index === active ? " is-active" : ""
            }`}
            disabled={it.disabled}
            onMouseEnter={() => setActive(index)}
            onClick={() => run(it)}
          >
            <span className="ctx-label">{it.label}</span>
            {it.hint ? <span className="ctx-hint">{it.hint}</span> : null}
          </button>
        );
      })}
    </div>
  );
}
