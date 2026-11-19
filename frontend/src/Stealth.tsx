import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService } from "./lib/api";

// 隐写输入窗（?mode=stealth）：窗口是全透明的，这里的输入框也看不见——文字、
// 光标都设成透明色——但它是一个真正的 textarea，所以输入法照常工作：
// 拼音候选框会出现在光标旁边，合成好的汉字落进这个看不见的缓冲里。
//
//   @名称@       这次（以及之后的）内容写进标题为「名称」的条目
//   @-@          忘掉名称，之后每次提交都新建一条
//   Ctrl+Enter   提交并清空缓冲，模式继续（可以一条接一条地写）
//   Esc          退出；缓冲非空时先自动保存一次
//
// 失焦（点了别的窗口）等同 Esc：先存再退，绝不丢字。

const BLUR_GRACE_MS = 220;

/**
 * 隐写窗的诊断日志。
 *
 * 为什么这个模式要专门打日志：它按设计"什么都看不见"（透明窗口 + 透明文字），
 * 一旦焦点没抢到，用户只会觉得"打了字什么都没发生"，而我们在外面完全看不出
 * 是哪一步断的（窗口没显示 / 拿到了焦点但按键没进来 / 提交失败）。
 * 每次激活只写 3 行左右，落在 clipnest.log 里，出问题能直接看出断点。
 */
function stealthLog(what: string, extra: string): void {
  void ClipService.LogClient(`stealth ui: ${what} ${extra}`).catch(() => {
    /* 日志失败不该影响输入 */
  });
}

export default function Stealth() {
  const [text, setText] = useState("");
  const areaRef = useRef<HTMLTextAreaElement | null>(null);
  const textRef = useRef(text);
  textRef.current = text;
  const openedAt = useRef(0);
  const closingRef = useRef(false);

  const focusArea = useCallback(() => {
    const el = areaRef.current;
    if (!el) return;
    el.focus();
    // 光标放到末尾：模式是"继续写"，不是"改开头"
    const n = el.value.length;
    el.setSelectionRange(n, n);
  }, []);

  // 进度只喂给角落 HUD（200ms 轮询一次），所以按 80ms 合并一下：
  // 每敲一个字都往返一次 Go 没有意义。
  const progressTimer = useRef<number | null>(null);
  const progress = useCallback((next: string) => {
    if (progressTimer.current) window.clearTimeout(progressTimer.current);
    progressTimer.current = window.setTimeout(() => {
      progressTimer.current = null;
      void ClipService.StealthProgress(next).catch(() => {
        /* HUD 拿不到进度不影响提交 */
      });
    }, 80);
  }, []);

  useEffect(() => () => {
    if (progressTimer.current) window.clearTimeout(progressTimer.current);
  }, []);

  // 提交：写入目标条目，然后清空缓冲、保持模式。
  const commit = useCallback(async () => {
    const body = textRef.current;
    if (!body.trim()) return;
    setText("");
    textRef.current = "";
    progress("");
    stealthLog("commit", `len=${body.length}`);
    try {
      await ClipService.StealthCommit(body);
    } catch {
      /* 失败时把内容退回缓冲，别让人白打 */
      setText(body);
      textRef.current = body;
      progress(body);
    }
    focusArea();
  }, [focusArea, progress]);

  // 结束：缓冲非空先保存一次，然后由后端隐藏窗口并归还焦点。
  const close = useCallback(async () => {
    if (closingRef.current) return;
    closingRef.current = true;
    try {
      await ClipService.CloseStealth(textRef.current);
    } catch {
      /* 关闭路径不再弹错，日志里有 */
    } finally {
      closingRef.current = false;
      setText("");
      textRef.current = "";
    }
  }, []);

  // 热键再次按下 = 退出（后端发事件，因为热键在 Go 侧注册）
  useEffect(() => {
    const off = Events.On("stealth:close-request", () => void close());
    return () => off();
  }, [close]);

  // 每次重新激活：清空缓冲 + 抢焦点（窗口刚 Show 时焦点还没到位，补两次）
  useEffect(() => {
    const onOpened = () => {
      openedAt.current = Date.now();
      setText("");
      textRef.current = "";
      focusArea();
      window.setTimeout(focusArea, 40);
      window.setTimeout(focusArea, 120);
      stealthLog("opened", "");
      window.setTimeout(() => {
        const active = document.activeElement as HTMLElement | null;
        stealthLog(
          "focus-check",
          `hasFocus=${document.hasFocus()} active=${active?.tagName ?? "none"}/${active?.className ?? ""}`,
        );
      }, 160);
    };
    const off = Events.On("stealth:opened", onOpened);
    const onFocus = () => focusArea();
    window.addEventListener("focus", onFocus);
    onOpened();
    return () => {
      off();
      window.removeEventListener("focus", onFocus);
    };
  }, [focusArea]);

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Ctrl+Enter 提交；Shift+Enter / Enter 仍然是换行（正文可以是多行）
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      void commit();
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      void close();
    }
  };

  const onBlur = () => {
    // 刚打开时的焦点抖动不算离开
    if (Date.now() - openedAt.current < BLUR_GRACE_MS) {
      stealthLog("blur-ignored", `afterMs=${Date.now() - openedAt.current}`);
      return;
    }
    stealthLog("blur-close", `afterMs=${Date.now() - openedAt.current} len=${textRef.current.length}`);
    void close();
  };

  return (
    <div className="stealth">
      <textarea
        ref={areaRef}
        className="stealth-input"
        value={text}
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => {
          if (textRef.current === "") stealthLog("first-input", `len=${e.target.value.length}`);
          setText(e.target.value);
          progress(e.target.value);
        }}
        onKeyDown={onKeyDown}
        onBlur={onBlur}
      />
    </div>
  );
}
