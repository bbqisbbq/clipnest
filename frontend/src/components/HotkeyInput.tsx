import { useCallback, useEffect, useRef, useState } from "react";

// 快捷键录制器：点一下，然后按下组合键即可，不用手打 "Ctrl+Shift+V" 这种字符串。
//
// 产出的格式是 Wails accelerator（Ctrl / Alt / Shift / Super + 主键），和 Go 侧
// GlobalShortcut.Register 直接兼容。

type Props = {
  value: string;
  onChange: (accelerator: string) => void;
  /** 允许清空（留空 = 不注册全局快捷键）。 */
  allowEmpty?: boolean;
};

// 这些键单独按下时没有意义，只用来组合。
const MODIFIER_KEYS = new Set(["Control", "Shift", "Alt", "Meta", "OS", "AltGraph"]);

// 浏览器 KeyboardEvent.key → accelerator 里的主键名
function mainKey(e: KeyboardEvent): string {
  const k = e.key;
  if (k.length === 1) {
    // 字母/数字直接大写；符号保持原样
    return /[a-z]/i.test(k) ? k.toUpperCase() : k;
  }
  switch (k) {
    case " ":
      return "Space";
    case "Escape":
      return "Escape";
    case "Enter":
      return "Return";
    case "Tab":
      return "Tab";
    case "Backspace":
      return "Backspace";
    case "Delete":
      return "Delete";
    case "Insert":
      return "Insert";
    case "Home":
      return "Home";
    case "End":
      return "End";
    case "PageUp":
      return "PageUp";
    case "PageDown":
      return "PageDown";
    case "ArrowUp":
      return "Up";
    case "ArrowDown":
      return "Down";
    case "ArrowLeft":
      return "Left";
    case "ArrowRight":
      return "Right";
    default:
      // F1..F12 之类直接用
      return k;
  }
}

export default function HotkeyInput({ value, onChange, allowEmpty = true }: Props) {
  const [recording, setRecording] = useState(false);
  const [hint, setHint] = useState("");
  const boxRef = useRef<HTMLButtonElement>(null);

  const stop = useCallback(() => {
    setRecording(false);
    setHint("");
  }, []);

  useEffect(() => {
    if (!recording) return;

    const onKeyDown = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();

      if (e.key === "Escape") {
        stop();
        return;
      }
      // 退格/删除 = 清空（留空表示不注册）
      if ((e.key === "Backspace" || e.key === "Delete") && !e.ctrlKey && !e.altKey && !e.shiftKey) {
        if (allowEmpty) {
          onChange("");
          stop();
        }
        return;
      }
      // 只按了修饰键：先给个提示，等主键
      if (MODIFIER_KEYS.has(e.key)) {
        setHint("再按一个主键…");
        return;
      }

      const parts: string[] = [];
      if (e.ctrlKey) parts.push("Ctrl");
      if (e.altKey) parts.push("Alt");
      if (e.shiftKey) parts.push("Shift");
      if (e.metaKey) parts.push("Super");
      const key = mainKey(e);
      // 没有修饰键的单键不做全局快捷键（会抢掉正常输入）
      if (parts.length === 0) {
        setHint("请至少带一个 Ctrl / Alt / Shift");
        return;
      }
      parts.push(key);
      onChange(parts.join("+"));
      stop();
    };

    // 录制时吞掉 keyup，避免按键被其它监听器当成一次输入
    const onKeyUp = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
    };

    window.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("keyup", onKeyUp, true);
    return () => {
      window.removeEventListener("keydown", onKeyDown, true);
      window.removeEventListener("keyup", onKeyUp, true);
    };
  }, [recording, onChange, allowEmpty, stop]);

  const start = () => {
    setRecording(true);
    setHint("按下组合键…（Esc 取消，Backspace 清空）");
    boxRef.current?.focus();
  };

  return (
    <div className="hotkey-input">
      <button
        ref={boxRef}
        type="button"
        className={`input is-short hotkey-box ${recording ? "is-recording" : ""}`}
        onClick={recording ? stop : start}
        title="点击后按下组合键即可录制"
      >
        {recording ? <span className="hotkey-rec">● 录制中</span> : value || "未设置"}
      </button>
      {allowEmpty && value && !recording ? (
        <button className="link-btn hotkey-clear" onClick={() => onChange("")} title="清空">
          清空
        </button>
      ) : null}
      {recording && hint ? <span className="hotkey-hint">{hint}</span> : null}
    </div>
  );
}
