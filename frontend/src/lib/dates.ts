// 日期键：全应用只有这一种写法 —— 本地时间、不带时区、补零。
//
// 日历格子、侧栏「今天」、EntryQuery.day（后端按 day 前缀匹配 created_at 的本地值）
// 必须用同一个口径，否则"点今天点不出东西"。以前 Calendar.tsx 和 App.tsx 各写了
// 一份一模一样的 `${y}-${mm}-${dd}`，现在只留这里。

/** "2026-11-19" —— 传日期取那天，不传取今天。 */
export function dayKey(date: Date = new Date()): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(
    date.getDate(),
  ).padStart(2, "0")}`;
}

/** "2026-11" —— 日历按月拉热度点用。 */
export function monthKey(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}`;
}

/** 今天（0 点归一，比较用）。 */
export function today(): string {
  return dayKey();
}
