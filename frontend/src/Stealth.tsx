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

const BLUR_GRACE_MS = 400;

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
  /**
   * 当前是否处于一次"激活会话"中。
   *
   * 这个窗口在应用启动时就创建好（隐藏），挂载时如果去抢焦点，窗口一隐藏就会
   * 产生一次 blur；等用户按热键把窗口显示出来，那个**陈旧的 blur** 会让
   * "失焦=存档退出"立刻生效 —— 表现就是"窗口一闪而过"。
   * 所以：没收到 stealth:opened 之前，任何 blur 都不算数。
   */
  const activeRef = useRef(false);

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
    activeRef.current = false;
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
      activeRef.current = true;
      openedAt.current = Date.now();
      setText("");
      textRef.current = "";
      focusArea();
      window.setTimeout(focusArea, 40);
      window.setTimeout(focusArea, 120);
      stealthLog("opened", "");
      // 200ms 后确认焦点：Windows 的前台锁可能让 Show() 显示了窗口却没给它键盘，
      // 这时打出来的字会落进用户原来的程序里（等于把内容打进了别人的窗口）。
      // 没焦点就让后端再抢一次，而不是默默让用户打丢。
      const verifyFocus = (attempt: number) => {
        window.setTimeout(() => {
          if (!activeRef.current) return;
          const hasFocus = document.hasFocus();
          const active = document.activeElement as HTMLElement | null;
          stealthLog(
            `focus-check${attempt > 0 ? "-retry" + attempt : ""}`,
            `hasFocus=${hasFocus} active=${active?.tagName ?? "none"}/${active?.className ?? ""}`,
          );
          if (hasFocus) {
            focusArea();
            return;
          }
          if (attempt >= 2) return; // 抢两次还不行就不再折腾，用户按 Ctrl+Enter 也能存
          void ClipService.RefocusStealth().catch(() => false);
          verifyFocus(attempt + 1);
        }, attempt === 0 ? 200 : 260);
      };
      verifyFocus(0);
    };
    const off = Events.On("stealth:opened", onOpened);
    const onFocus = () => {
      // 窗口被系统重新激活（比如用户点了别处又点回来）：把光标还给输入框
      if (activeRef.current) focusArea();
    };
    window.addEventListener("focus", onFocus);
    // 刻意不在挂载时调用 onOpened()：这时窗口还是隐藏的，抢焦点只会制造脏 blur。
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
    // 不在会话里（窗口隐藏着 / 还没被热键激活）：这个 blur 是陈旧的，直接忽略
    if (!activeRef.current) {
      stealthLog("blur-inactive", "");
      return;
    }
    const elapsed = Date.now() - openedAt.current;
    // 刚打开时的焦点抖动不算离开
    if (elapsed < BLUR_GRACE_MS) {
      stealthLog("blur-ignored", `afterMs=${elapsed}`);
      return;
    }
    // 延后确认：可能只是内部元素换了焦点，或别的 ClipNest 窗口短暂抢了一下。
    // 真丢了焦点才按"失焦=存档退出"走；还在焦点上就把光标放回输入框。
    window.setTimeout(() => {
      if (!activeRef.current) return;
      if (document.hasFocus()) {
        stealthLog("blur-retained", `hasFocus=true`);
        focusArea();
        return;
      }
      stealthLog("blur-close", `afterMs=${Date.now() - openedAt.current} len=${textRef.current.length}`);
      void close();
    }, 260);
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
