import { useCallback, useEffect, useMemo, useState } from "react";
import { ClipService, type EntryQuery } from "../lib/api";

// 日历：月视图，有内容的日子显示热度点，点某天 = 只看那天。
// 这是统一时间线的"导航器"——不需要滚一条无限长的列表。
//
// 圆点必须跟当前筛选器一致：以前这里只按"全库"统计，于是选了「片段 / 图片 /
// 置顶」之后日历照样画点，点进去却一条都没有，看着像日期筛选坏了。

type Props = {
  selectedDay: string; // "" = 不按天筛
  onPickDay: (day: string) => void;
  query: EntryQuery; // 当前侧栏筛选器的查询
};

const WEEKDAYS = ["一", "二", "三", "四", "五", "六", "日"];

function monthKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

function dayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export default function Calendar({ selectedDay, onPickDay, query }: Props) {
  const [cursor, setCursor] = useState(() => new Date());
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [loading, setLoading] = useState(false);

  const month = monthKey(cursor);
  // 查询对象每次渲染都是新引用，用序列化后的值做依赖才不会无限重载
  const queryKey = JSON.stringify(query ?? {});

  const load = useCallback(async (m: string) => {
    setLoading(true);
    try {
      const list = (await ClipService.GetEntryCalendar(m, JSON.parse(queryKey) as EntryQuery)) ?? [];
      const map: Record<string, number> = {};
      for (const d of list) map[d.day] = d.count;
      setCounts(map);
    } catch {
      setCounts({});
    } finally {
      setLoading(false);
    }
  }, [queryKey]);

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
        <button
          className="cal-today"
          title="回到今天"
          onClick={() => {
            setCursor(new Date());
            onPickDay(selectedDay === today ? "" : today);
          }}
        >
          今天
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
          if (!cell) return <span key={`pad-${i}`} className="cal-cell is-empty" aria-hidden="true" />;
          const n = counts[cell.key] ?? 0;
          const isToday = cell.key === today;
          const isActive = cell.key === selectedDay;
          // 热度只靠圆点的透明度表达（CSS 里从来没有 .has-1..4，
          // 以前算出来的 level 类名一个样式都没命中，属白算）
          return (
            <button
              key={cell.key}
              className={`cal-cell ${isToday ? "is-today" : ""} ${isActive ? "is-active" : ""}`}
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
