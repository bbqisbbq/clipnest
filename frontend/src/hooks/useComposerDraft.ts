import { useCallback, useEffect, useRef, useState } from "react";
import { ClipService } from "../lib/api";

// 快捷输入框的草稿：输入框在哪个视图都要保持同一份内容，关掉应用再打开也要还在。
//
// 状态放在这里（而不是 EntriesView 里）有两个理由：
//   1. 输入框是全局常驻组件，视图切换不该卸载它、更不该清空它；
//   2. 草稿要落到 clipnest.db（后端 meta），页面刷新/重启都读得回来。
// 落盘走防抖：敲字时只更新内存，停手 400ms 才写一次库；窗口失焦/隐藏/卸载时补一次。

/** 停手多久落盘。太短会把 SQLite 写成热点，太长则崩溃时丢字多。 */
const SAVE_DELAY_MS = 400;

export type ComposerDraft = {
  /** 当前草稿（Markdown；图片是 memoimg: 令牌） */
  text: string;
  /** 用户每敲一个字都调这里：更新内存 + 排队落盘 */
  setText: (next: string) => void;
  /** 存成条目之后清空（内存与库里一起清，重启不会被塞回来） */
  reset: () => void;
  /** 上次的草稿是否已经从库里读回来了（读完之前别急着渲染空白编辑器） */
  restored: boolean;
};

export function useComposerDraft(onError: (message: string) => void): ComposerDraft {
  const [text, setText] = useState("");
  const [restored, setRestored] = useState(false);
  // latest 是"用户看到的内容"，dirty 表示它还没落盘
  const latest = useRef("");
  const dirty = useRef(false);
  const timer = useRef<number | null>(null);

  const persist = useCallback(
    (value: string) => {
      dirty.current = false;
      void ClipService.SetComposerDraft(value).catch((err) => {
        // 落盘失败不该拦住输入：下一次输入会再写一遍
        onError(`草稿保存失败：${String(err)}`);
      });
    },
    [onError],
  );

  const flush = useCallback(() => {
    if (timer.current) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
    if (dirty.current) persist(latest.current);
  }, [persist]);

  // 启动时把上次没存完的草稿读回来。用户已经先动手打字就以用户为准。
  useEffect(() => {
    let alive = true;
    void ClipService.GetComposerDraft()
      .then((saved) => {
        if (!alive || dirty.current || latest.current) return;
        latest.current = saved ?? "";
        setText(latest.current);
      })
      .catch(() => undefined)
      .finally(() => {
        if (alive) setRestored(true);
      });
    return () => {
      alive = false;
    };
  }, []);

  const setTextValue = useCallback(
    (next: string) => {
      latest.current = next;
      dirty.current = true;
      setText(next);
      if (timer.current) window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => {
        timer.current = null;
        if (dirty.current) persist(latest.current);
      }, SAVE_DELAY_MS);
    },
    [persist],
  );

  const reset = useCallback(() => {
    if (timer.current) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
    latest.current = "";
    setText("");
    persist("");
  }, [persist]);

  // 关窗、切走、组件卸载之前把防抖窗口里的内容补上
  useEffect(() => {
    const onHide = () => flush();
    window.addEventListener("blur", onHide);
    window.addEventListener("beforeunload", onHide);
    document.addEventListener("visibilitychange", onHide);
    return () => {
      window.removeEventListener("blur", onHide);
      window.removeEventListener("beforeunload", onHide);
      document.removeEventListener("visibilitychange", onHide);
      flush();
    };
  }, [flush]);

  return { text, setText: setTextValue, reset, restored };
}
