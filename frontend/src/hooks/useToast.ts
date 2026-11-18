import { useCallback, useRef, useState } from "react";

// 轻量 toast：一处状态，一个定时器，谁都能调。
// 拆出来是为了让任何组件都能发通知，而不用把 setToast 一层层往下传。

export function useToast() {
  const [message, setMessage] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const notify = useCallback((text: string) => {
    setMessage(text);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setMessage(""), 2600);
  }, []);

  return { toast: message, notify };
}
