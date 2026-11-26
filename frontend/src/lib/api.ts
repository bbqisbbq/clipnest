import { ClipService as ClipServiceNS } from "./clipservice.gen";
import type * as Models from "./models";
import * as EventNames from "./eventnames.gen";

// 换壳（Wails → MyGo）后，这一层是唯一需要动的地方：
//   · ClipService 从"Wails 生成的 $Call.ByID(...)"换成 call("ClipService.X")；
//   · 方法名保持 Go 的 PascalCase，所以 40 多个组件一行都不用改；
//   · 模型类型在 ./models.ts（纯 TS 类型，不含运行时）。
export const ClipService = ClipServiceNS;
export { Models, EventNames };

export type FileRef = Models.FileRef;
export type Settings = Models.Settings;
export type Stats = Models.Stats;
export type AppInfo = Models.AppInfo;
export type SyncStatus = Models.SyncStatus;
export type UpdateInfo = Models.UpdateInfo;
export type OCRProgress = Models.OCRProgress;
export type EmbedStatus = Models.EmbedStatus;
export type EmbedProgress = Models.EmbedProgress;
export type SyncPanel = Models.SyncPanel;
export type Entry = Models.Entry;
export type EntryQuery = Models.EntryQuery;
export type EntryFilter = Models.EntryFilter;
export type FilterGroup = Models.FilterGroup;
export type FilterCondition = Models.FilterCondition;
export type FilterFieldSpec = Models.FilterFieldSpec;
export type EntryStats = Models.EntryStats;
export type DayCount = Models.DayCount;
export type TodoRef = Models.TodoRef;
export type TodoItem = Models.TodoItem;
export type Snippet = Models.Snippet;
export type SnippetKind = Models.SnippetKind;
export type SyncDeviceInfo = Models.SyncDeviceInfo;
export type SyncDetail = Models.SyncDetail;

