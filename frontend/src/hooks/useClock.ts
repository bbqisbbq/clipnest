import { useEffect, useState } from "react";

// 每分钟变一次的序号。给"今天 / 昨天 / 3 分钟前"这类**相对**标签用：
// 它们是渲染时算出来的，不重渲染就不会变——应用一直开着跨过零点，"今天"会一直
// 贴在昨天那批条目上。用它当 useMemo 的依赖即可，最便宜的修法。
//
// 别把它用在"绝对时间"上（卡片上的 HH:MM 是绝对时间，不需要 tick）。

export function useMinuteTick(): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => setTick((n) => n + 1), 60_000);
    return () => window.clearInterval(id);
  }, []);
  return tick;
}
