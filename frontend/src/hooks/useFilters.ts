import { useCallback, useState } from "react";
import { ClipService, type EntryFilter } from "../lib/api";
import type { ConfirmOptions } from "../components/Dialog";

// 筛选器的增删改：侧栏的 ⋯ 菜单和「＋ 新建筛选器」都走这里。
//
// 从 App.tsx 抽出来的原因：这套逻辑（打开哪个弹窗、删之前怎么确认、
// 删的是不是当前筛选器）和视图切换、详情脏数据拦截没有关系，
// 混在一起时 App 里同时躺着三种状态机。

/** 新建筛选器的草稿：id 为空 = 还没入库。 */
export const BLANK_FILTER = {
  id: "",
  name: "新筛选器",
  icon: "memo",
  query: {},
  sortOrder: 0,
  builtin: false,
  deletable: true,
} as EntryFilter;

/** 设置加载出来之前 / 筛选器列表为空时的兜底。 */
export const DEFAULT_FILTER = {
  id: "all",
  name: "全部内容",
  icon: "all",
  query: {},
  sortOrder: 0,
  builtin: true,
  deletable: false,
} as EntryFilter;

export type FilterDialogState = { filter: EntryFilter; isNew: boolean } | null;

type Args = {
  notify: (message: string) => void;
  confirm: (options: ConfirmOptions) => Promise<boolean>;
  refreshMeta: () => Promise<void>;
  /** 被删掉的正好是当前选中的筛选器时，通知调用方退回"全部内容" */
  onActiveFilterDeleted: (id: string) => void;
};

export function useFilters({ notify, confirm, refreshMeta, onActiveFilterDeleted }: Args) {
  const [dialog, setDialog] = useState<FilterDialogState>(null);

  const startNew = useCallback(() => setDialog({ filter: BLANK_FILTER, isNew: true }), []);
  const startEdit = useCallback((f: EntryFilter) => setDialog({ filter: f, isNew: false }), []);

  const startDuplicate = useCallback(
    (f: EntryFilter) =>
      setDialog({
        filter: {
          ...f,
          id: "",
          name: `${f.name} 副本`,
          builtin: false,
          deletable: true,
          query: { ...(f.query ?? {}) } as EntryFilter["query"],
        },
        isNew: true,
      }),
    [],
  );

  const remove = useCallback(
    async (f: EntryFilter) => {
      const ok = await confirm({
        message: `删除筛选器「${f.name}」？条目本身不受影响。`,
        danger: true,
        confirmLabel: "删除",
      });
      if (!ok) return;
      try {
        await ClipService.DeleteFilter(f.id);
        await refreshMeta();
        onActiveFilterDeleted(f.id);
        notify(`已删除筛选器「${f.name}」`);
      } catch (err) {
        notify(`删除失败：${String(err)}`);
      }
    },
    [confirm, notify, refreshMeta, onActiveFilterDeleted],
  );

  return { dialog, closeDialog: () => setDialog(null), startNew, startEdit, startDuplicate, remove };
}
