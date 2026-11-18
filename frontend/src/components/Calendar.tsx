import { useCallback, useEffect, useMemo, useState } from "react";
import { ClipService } from "../lib/api";

// 日历：月视图，有内容的日子显示热度点，点某天 = 只看那天。
// 这是统一时间线的"导航器"——不需要滚一条无限长的列表。

type Props = {
  selectedDay: string; // "" = 不按天筛
  onPickDay: (day: string) => void;
};

const WEEKDAYS = ["一", "二", "三", "四", "五", "六", "日"];

function monthKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

function dayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export default function Calendar({ selectedDay, onPickDay }: Props) {
  const [cursor, setCursor] = useState(() => new Date());
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [loading, setLoading] = useState(false);

  const month = monthKey(cursor);

  const load = useCallback(async (m: string) => {
    setLoading(true);
    try {
      const list = (await ClipService.GetEntryCalendar(m)) ?? [];
      const map: Record<string, number> = {};
      for (const d of list) map[d.day] = d.count;
      setCounts(map);
    } catch {
      setCounts({});
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(month);
  }, [month, load]);

  // 网格：周一开头，补齐前后空位
  const cells = useMemo(() => {
    const first = new Date(cursor.getFullYear(), cursor.getMonth(), 1);
    // JS 里周日=0，转成周一=0
    const lead = (first.getDay() + 6) % 7;
    const daysInMonth = new Date(cursor.getFullYear(), cursor.getMonth() + 1, 0).getDate();
    const out: Array<{ key: string; day: number } | null> = [];
    for (let i = 0; i < lead; i++) out.push(null);
    for (let d = 1; d <= daysInMonth; d++) {
      const date = new Date(cursor.getFullYear(), cursor.getMonth(), d);
      out.push({ key: dayKey(date), day: d });
    }
    return out;
  }, [cursor]);

  const today = dayKey(new Date());
  const maxCount = useMemo(
    () => Math.max(1, ...Object.values(counts)),
    [counts],
  );

  const shiftMonth = (delta: number) => {
    setCursor((c) => new Date(c.getFullYear(), c.getMonth() + delta, 1));
  };

  return (
    <div className="cal">
      <div className="cal-head">
        <button className="cal-nav" onClick={() => shiftMonth(-1)} title="上个月">
          ‹
        </button>
        <span className="cal-title">
          {cursor.getFullYear()} 年 {cursor.getMonth() + 1} 月
          {loading ? " …" : ""}
        </span>
        <button className="cal-nav" onClick={() => shiftMonth(1)} title="下个月">
          ›
        </button>
      </div>

      <div className="cal-grid cal-weekdays">
        {WEEKDAYS.map((w) => (
          <span key={w} className="cal-weekday">
            {w}
          </span>
        ))}
      </div>

      <div className="cal-grid">
        {cells.map((cell, i) => {
          if (!cell) return <span key={`pad-${i}`} className="cal-cell is-empty" />;
          const n = counts[cell.key] ?? 0;
          const isToday = cell.key === today;
          const isActive = cell.key === selectedDay;
          // 热度：1/3/5/7 档
          const level = n === 0 ? 0 : n <= 2 ? 1 : n <= 5 ? 2 : n <= 12 ? 3 : 4;
          return (
            <button
              key={cell.key}
              className={`cal-cell ${isToday ? "is-today" : ""} ${isActive ? "is-active" : ""} ${
                n > 0 ? `has-${level}` : ""
              }`}
              title={`${cell.key}：${n} 条`}
              onClick={() => onPickDay(isActive ? "" : cell.key)}
            >
              <span className="cal-day">{cell.day}</span>
              {n > 0 ? (
                <span
                  className="cal-dot"
                  style={{ opacity: 0.35 + 0.65 * (n / maxCount) }}
                />
              ) : null}
            </button>
          );
        })}
      </div>

      {selectedDay ? (
        <button className="cal-clear" onClick={() => onPickDay("")}>
          清除日期筛选（{selectedDay}）
        </button>
      ) : null}
    </div>
  );
}
