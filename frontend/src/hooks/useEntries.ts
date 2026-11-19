import { useCallback, useEffect, useMemo, useRef, useState, type UIEvent } from "react";
import { ClipService, type Entry, type EntryQuery } from "../lib/api";

// 条目列表的数据层：加载最新一页、上滑加载更早、按天分组、以及"是否贴住底部"的滚动状态。
// 视图只管渲染；这里不碰搜索框、写新条目和卡片操作——
// 那几件事属于 EntriesView，因为它们要跟输入状态、弹窗状态联动。

const PAGE_SIZE = 40;

/** 同一天的条目，供时间线按日分组渲染。 */
export type EntryDayGroup = { day: string; key: string; items: Entry[] };

/** 分组用的键：同年同月同日算一组（不补零，只用于比较，不用于显示）。 */
function dayKey(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

/** 分组标题：今天 / 昨天 / 月日 / 年月日。 */
function dayLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date();
  const same = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  const yesterday = new Date(today.getTime() - 86400000);
  if (same(d, today)) return "今天";
  if (same(d, yesterday)) return "昨天";
  return d.getFullYear() === today.getFullYear()
    ? `${d.getMonth() + 1} 月 ${d.getDate()} 日`
    : `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

export function useEntries(baseQuery: EntryQuery) {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);

  const timelineRef = useRef<HTMLDivElement>(null);
  // 贴底标志：新内容进来时，只有"本来就在底部"才跟着滚下去
  const stickToBottom = useRef(true);

  const loadLatest = useCallback(async () => {
    try {
      const list =
        (await ClipService.GetEntries({ ...baseQuery, limit: PAGE_SIZE, tail: true } as never)) ?? [];
      setEntries(list);
      setHasMore(list.length >= PAGE_SIZE);
      stickToBottom.current = true;
    } catch (err) {
      console.error(err);
    }
  }, [baseQuery]);

  const loadOlder = useCallback(async () => {
    if (loadingOlder || !hasMore || entries.length === 0) return;
    const cursor = entries[0].createdAt;
    if (!cursor) return;
    setLoadingOlder(true);
    const host = timelineRef.current;
    const prevHeight = host?.scrollHeight ?? 0;
    try {
      const older =
        (await ClipService.GetEntries({ ...baseQuery, limit: PAGE_SIZE, before: cursor } as never)) ?? [];
      if (older.length === 0) {
        setHasMore(false);
      } else {
        setEntries((prev) => [...older, ...prev]);
        setHasMore(older.length >= PAGE_SIZE);
        requestAnimationFrame(() => {
          if (host) host.scrollTop = host.scrollHeight - prevHeight;
        });
      }
    } catch (err) {
      console.error(err);
    } finally {
      setLoadingOlder(false);
    }
  }, [loadingOlder, hasMore, entries, baseQuery]);

  useEffect(() => {
    void loadLatest();
  }, [loadLatest]);

  const grouped = useMemo(() => {
    const out: EntryDayGroup[] = [];
    for (const e of entries) {
      const key = dayKey(e.createdAt);
      const last = out[out.length - 1];
      if (last && last.key === key) last.items.push(e);
      else out.push({ day: dayLabel(e.createdAt), key, items: [e] });
    }
    return out;
  }, [entries]);

  useEffect(() => {
    if (!stickToBottom.current) return;
    const host = timelineRef.current;
    if (host) host.scrollTop = host.scrollHeight;
  }, [grouped]);

  /** 时间线滚动：更新贴底标志，滑到顶部附近时补更早的一页。 */
  const onTimelineScroll = useCallback(
    (e: UIEvent<HTMLDivElement>) => {
      const el = e.currentTarget;
      stickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
      if (el.scrollTop < 60) void loadOlder();
    },
    [loadOlder],
  );

  return {
    entries,
    grouped,
    hasMore,
    loadingOlder,
    loadLatest,
    loadOlder,
    timelineRef,
    stickToBottom,
    onTimelineScroll,
  };
}
