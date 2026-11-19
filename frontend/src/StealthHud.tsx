import { useEffect, useState } from "react";
import { ClipService, type StealthState } from "./lib/api";

// 隐写模式的角落提示（?mode=stealthhud）：一个不抢焦点的小条，告诉你
// 「现在写给谁、已经攒了多少字」。窗口是透明的、点上去会穿透，所以它只是
// 一个状态灯，不承担任何交互。
//
// 数据是轮询拿的：跨窗口的事件管道比一次 200ms 的读取更麻烦，而这里要的
// 只是"字数变了"。

export default function StealthHud() {
  const [state, setState] = useState<StealthState | null>(null);

  useEffect(() => {
    let alive = true;
    const tick = async () => {
      try {
        const next = await ClipService.StealthState();
        if (alive) setState(next);
      } catch {
        /* 后端没起来时安静地什么都不显示 */
      }
    };
    void tick();
    const id = window.setInterval(() => void tick(), 200);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, []);

  const target = (state?.target ?? "").trim();
  const chars = state?.chars ?? 0;
  const message = (state?.message ?? "").trim();

  let label = "隐写中";
  if (chars > 0) {
    label = `${target ? `@${target}@` : "新条目"} · ${chars}`;
  } else if (message) {
    label = message;
  }

  return (
    <div className="stealth-hud" title="隐写模式：Ctrl+Enter 提交，Esc 退出">
      <span className={`stealth-dot ${chars > 0 ? "is-live" : ""}`} />
      <span className="stealth-hud-label">{label}</span>
    </div>
  );
}
