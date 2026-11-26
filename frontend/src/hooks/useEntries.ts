import { useCallback, useEffect, useMemo, useRef, useState, type UIEvent } from "react";
import { ClipService, type Entry, type EntryQuery } from "../lib/api";
import { useMinuteTick } from "./useClock";

// 条目列表的数据层：加载最新一页、上滑加载更早、按天分组、以及"是否贴住底部"的滚动状态。
// 视图只管渲染；这里不碰搜索框、写新条目和卡片操作——
// 那几件事属于 EntriesView，因为它们要跟输入状态、弹窗状态联动。
//
// 两条不写在文档里就会踩的坑：
//   1. **请求要有序号**。快速切筛选器/连打搜索时，先发的查询可能后回来，
//      老响应会把新结果顶掉（页面显示的是上一个筛选器的内容）。
//   2. **后台刷新不能整页替换**。用户可能正翻着更早的几页，替换成"最新 40 条"
//      等于把手里那摞纸抽走 —— 后台来的新条目只能并进来。

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

/** 按 created_at 升序合并两页：同一 id 以新来的为准，其余保持原有顺序。 */
function mergePages(prev: Entry[], page: Entry[]): Entry[] {
  const byId = new Map<string, Entry>();
  for (const e of prev) byId.set(e.id, e);
  for (const e of page) byId.set(e.id, e);
  return [...byId.values()].sort((a, b) => (a.createdAt < b.createdAt ? -1 : a.createdAt > b.createdAt ? 1 : 0));
}

export function useEntries(baseQuery: EntryQuery, onError?: (msg: string) => void) {
  // 相对日期标签（今天/昨天）要能自己改口，见下面 grouped 的说明
  const clockTick = useMinuteTick();
  const [entries, setEntries] = useState<Entry[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const timelineRef = useRef<HTMLDivElement>(null);
  // 贴底标志：新内容进来时，只有"本来就在底部"才跟着滚下去
  const stickToBottom = useRef(true);
  // 请求序号：只有最后一次发出的查询有资格写 state
  const reqId = useRef(0);

  // stick=false 用于"后台采集进来的新条目"：不该把正在往上翻历史的用户拽回底部，
  // 也不该把已经加载的更早几页整段丢掉（这两件事以前是同一个 bug 的两面）。
  const loadLatest = useCallback(
    async (opts?: { stick?: boolean; mode?: "replace" | "merge" }) => {
      const seq = ++reqId.current;
      const background = opts?.stick === false;
      try {
        const list =
          (await ClipService.GetEntries({ ...baseQuery, limit: PAGE_SIZE, tail: true } as never)) ?? [];
        if (seq !== reqId.current) return; // 已经有更新的查询发出，丢弃这份老结果
        setEntries((prev) => (background || opts?.mode === "merge" ? mergePages(prev, list) : list));
        if (!background) setHasMore(list.length >= PAGE_SIZE);
        if (opts?.stick !== false) stickToBottom.current = true;
        setError("");
      } catch (err) {
        if (seq !== reqId.current) return;
        const msg = `列表加载失败：${String(err)}`;
        setError(msg);
        onError?.(msg);
      } finally {
        if (seq === reqId.current) setLoading(false);
      }
    },
    [baseQuery, onError],
  );

  const loadOlder = useCallback(async () => {
    if (loadingOlder || !hasMore || entries.length === 0) return;
    const cursor = entries[0].createdAt;
    if (!cursor) return;
    const seq = reqId.current;
    setLoadingOlder(true);
    const host = timelineRef.current;
    const prevHeight = host?.scrollHeight ?? 0;
    try {
      const older =
        (await ClipService.GetEntries({ ...baseQuery, limit: PAGE_SIZE, before: cursor } as never)) ?? [];
      if (seq !== reqId.current) return; // 期间换了筛选器：这批属于上一屏
      if (older.length === 0) {
        setHasMore(false);
      } else {
        // mergePages 按 id 去重再按时间排：后端万一把同一页重复给了（2.13.1 就发生过，
        // 列表一路涨到 560 条），这里也不会越滚越长。去重是兜底，不是主修。
        setEntries((prev) => mergePages(prev, older));
        setHasMore(older.length >= PAGE_SIZE);
        requestAnimationFrame(() => {
          if (host) host.scrollTop = host.scrollHeight - prevHeight;
        });
      }
    } catch (err) {
      const msg = `加载更早的内容失败：${String(err)}`;
      setError(msg);
      onError?.(msg);
    } finally {
      setLoadingOlder(false);
    }
  }, [loadingOlder, hasMore, entries, baseQuery, onError]);

  useEffect(() => {
    setLoading(true);
    void loadLatest();
  }, [loadLatest]);

  // 别处删掉了一条（在快速粘贴窗里删的、同步拉到对端的删除）：本地直接摘掉，
  // 不等下一次整页重载 —— 否则列表里还留着一条点进去报"条目不存在"的卡片。
  const removeLocal = useCallback((id: string) => {
    setEntries((prev) => prev.filter((e) => e.id !== id));
  }, []);

  /** 就地替换一条（置顶/归档/改名这类小改），不重载、不跳位。 */
  const patchLocal = useCallback((next: Entry) => {
    setEntries((prev) => prev.map((e) => (e.id === next.id ? next : e)));
  }, []);

  const grouped = useMemo(() => {
    const out: EntryDayGroup[] = [];
    for (const e of entries) {
      const key = dayKey(e.createdAt);
      const last = out[out.length - 1];
      if (last && last.key === key) last.items.push(e);
      else out.push({ day: dayLabel(e.createdAt), key, items: [e] });
    }
    return out;
    // clockTick 只为了让"今天/昨天"跨零点能改口：分组键（key）不变，只有标题要重算。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entries, clockTick]);

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
    loading,
    loadingOlder,
    error,
    loadLatest,
    loadOlder,
    removeLocal,
    patchLocal,
    timelineRef,
    stickToBottom,
    onTimelineScroll,
  };
}
