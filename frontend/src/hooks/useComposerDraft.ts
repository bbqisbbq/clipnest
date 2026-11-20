import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService } from "../lib/api";

// 快捷输入框的草稿：输入框在哪个视图都要保持同一份内容，关掉应用再打开也要还在。
//
// 状态放在这里（而不是 EntriesView 里）有两个理由：
//   1. 输入框是全局常驻组件，视图切换不该卸载它、更不该清空它；
//   2. 草稿要落到 clipnest.db（后端 meta），页面刷新/重启都读得回来。
//
// 落盘分工（2.8.2 起）：
//   · 这里**不防抖**，每次输入都把正文交给后端（一次 Wails 调用，很便宜），
//     后端在内存里立刻更新，并把 SQLite 写按 400ms 静默期合并（internal/clip/draft.go）。
//     以前防抖在浏览器里，那 400ms 窗口里进程被强杀就丢字；现在窗口只剩"一次按键"。
//   · 窗口收起（托盘）、页面隐藏、退出之前再显式 Flush 一次，不赌静默期还剩多少。

export type ComposerDraft = {
  /** 当前草稿（Markdown；图片是 memoimg: 令牌） */
  text: string;
  /** 用户每敲一个字都调这里：更新内存 + 通知后端 */
  setText: (next: string) => void;
  /** 存成条目之后清空（内存与库里一起清，重启不会被塞回来） */
  reset: () => void;
  /** 上次的草稿是否已经从库里读回来了（读完之前别急着渲染空白编辑器） */
  restored: boolean;
};

export function useComposerDraft(onError: (message: string) => void): ComposerDraft {
  const [text, setText] = useState("");
  const [restored, setRestored] = useState(false);
  // 用户已经动过手就不再拿库里的旧草稿覆盖（读回来是异步的，可能晚于第一次输入）
  const touched = useRef(false);

  /** 把一行草稿交给后端（后端自己防抖写库）。 */
  const push = useCallback(
    (value: string) => {
      void ClipService.SetComposerDraft(value).catch((err) => {
        // 落盘失败不该拦住输入：下一次输入会再写一遍
        onError(`草稿保存失败：${String(err)}`);
      });
    },
    [onError],
  );

  /** 让后端立刻把内存里的草稿写进库（收起窗口 / 退出前）。 */
  const flush = useCallback(() => {
    void ClipService.FlushComposerDraft().catch(() => undefined);
  }, []);

  // 启动时把上次没存完的草稿读回来。用户已经先动手打字就以用户为准。
  useEffect(() => {
    let alive = true;
    void ClipService.GetComposerDraft()
      .then((saved) => {
        if (!alive || touched.current) return;
        setText(saved ?? "");
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
      touched.current = true;
      setText(next);
      push(next);
    },
    [push],
  );

  const reset = useCallback(() => {
    touched.current = true;
    setText("");
    // 清空要立刻生效：先写空值再 flush，别让静默期把"已经存成条目"的内容又塞回来
    void ClipService.SetComposerDraft("")
      .then(() => ClipService.FlushComposerDraft())
      .catch((err) => onError(`清空草稿失败：${String(err)}`));
  }, [onError]);

  // 收起窗口（点 × 进托盘）、切走、关窗前各补一次
  useEffect(() => {
    const onHide = () => flush();
    window.addEventListener("blur", onHide);
    window.addEventListener("beforeunload", onHide);
    document.addEventListener("visibilitychange", onHide);
    // 主窗口 Hide 是 Wails 原生行为，浏览器这边不一定收到 visibilitychange，
    // 所以后端在 WindowHide 钩子里补发一个 app:hidden（见 wire.go）。
    const offHidden = Events.On("app:hidden", onHide);
    return () => {
      window.removeEventListener("blur", onHide);
      window.removeEventListener("beforeunload", onHide);
      document.removeEventListener("visibilitychange", onHide);
      offHidden();
      flush();
    };
  }, [flush]);

  return { text, setText: setTextValue, reset, restored };
}
