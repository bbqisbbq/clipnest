import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "../lib/runtime";
import { ClipService } from "../lib/api";

// 未保存草稿：主窗口底部的「记一条」和快速输入窗「记一笔」各一份。
//
// 状态放在 hook 里（而不是某个视图里）有两个理由：
//   1. 输入框是常驻组件，视图切换不该卸载它、更不该清空它；
//   2. 草稿要落到 clipnest.db（后端 meta），页面刷新/重启/窗口被销毁都读得回来。
//
// 落盘分工：这里**不防抖**，每次输入都把正文交给后端（一次 IPC，很便宜），后端在
// 内存里立刻更新、把 SQLite 写按 400ms 静默期合并（internal/clip/draft.go）。
// 以前防抖在浏览器里，那 400ms 窗口里进程被强杀就丢字；现在窗口只剩"一次按键"。
// 窗口收起（托盘）、页面隐藏、退出之前再显式 Flush 一次。
//
// **为什么「记一笔」必须用这套（2.12.6）**：那个窗口闲置会被销毁（idle.go），
// 页面连同它的 React state 一起没了。以前它的正文只活在 Capture.tsx 的 useState
// 里，于是"打了一段字没保存 → 关窗 → 再打开就是空的"。草稿落库是它唯一活下来的
// 可能，不是锦上添花。

/** 草稿的两份归属：主窗口输入框 / 「记一笔」浮层。两把键，互不覆盖。 */
export type DraftKind = "composer" | "capture";

export type Draft = {
  /** 当前草稿（Markdown；图片是 memoimg: 令牌） */
  text: string;
  /** 用户每敲一个字都调这里：更新内存 + 通知后端 */
  setText: (next: string) => void;
  /** 存成条目之后清空（内存与库里一起清，重启不会被塞回来） */
  reset: () => void;
  /** 上次的草稿是否已经从库里读回来了（读完之前别急着渲染空白编辑器） */
  restored: boolean;
};

/** 旧名字：调用方一直用的是它，含义就是 composer 那一份。 */
export type ComposerDraft = Draft;

type DraftAPI = {
  get: () => Promise<string>;
  set: (text: string) => Promise<void>;
  flush: () => Promise<void>;
};

// 三个方法按 kind 分派。写成表而不是 if：漏一种 kind 会直接编译失败。
const APIs: Record<DraftKind, DraftAPI> = {
  composer: {
    get: () => ClipService.GetComposerDraft(),
    set: (text) => ClipService.SetComposerDraft(text),
    flush: () => ClipService.FlushComposerDraft(),
  },
  capture: {
    get: () => ClipService.GetCaptureDraft(),
    set: (text) => ClipService.SetCaptureDraft(text),
    flush: () => ClipService.FlushCaptureDraft(),
  },
};

export function useDraft(kind: DraftKind, onError: (message: string) => void): Draft {
  const api = APIs[kind];
  const [text, setText] = useState("");
  const [restored, setRestored] = useState(false);
  // 用户已经动过手就不再拿库里的旧草稿覆盖（读回来是异步的，可能晚于第一次输入）
  const touched = useRef(false);

  /** 把一行草稿交给后端（后端自己防抖写库）。 */
  const push = useCallback(
    (value: string) => {
      void api.set(value).catch((err) => {
        // 落盘失败不该拦住输入：下一次输入会再写一遍
        onError(`草稿保存失败：${String(err)}`);
      });
    },
    [api, onError],
  );

  /** 让后端立刻把内存里的草稿写进库（收起窗口 / 退出前）。 */
  const flush = useCallback(() => {
    void api.flush().catch(() => undefined);
  }, [api]);

  // 启动时把上次没存完的草稿读回来。用户已经先动手打字就以用户为准。
  useEffect(() => {
    let alive = true;
    void api
      .get()
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
  }, [api]);

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
    void api
      .set("")
      .then(() => api.flush())
      .catch((err) => onError(`清空草稿失败：${String(err)}`));
  }, [api, onError]);

  // 收起窗口（点 × 进托盘）、切走、关窗前各补一次
  useEffect(() => {
    const onHide = () => flush();
    window.addEventListener("blur", onHide);
    window.addEventListener("beforeunload", onHide);
    document.addEventListener("visibilitychange", onHide);
    // 主窗口 Hide 是后端的原生行为，浏览器这边不一定收到 visibilitychange，
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

export function useComposerDraft(onError: (message: string) => void): Draft {
  return useDraft("composer", onError);
}

export function useCaptureDraft(onError: (message: string) => void): Draft {
  return useDraft("capture", onError);
}
