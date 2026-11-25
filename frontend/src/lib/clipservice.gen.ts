// 由 scripts/gen-mygo-clipservice.ps1 从 scripts/mygo-bindings/clipservice.signatures.txt 生成，别手改。
// 方法名保持 Go 的 PascalCase，Wails 版组件因此一行都不用改。
import { call } from "mygo-runtime";
import type * as Models from "./models";

export const AutoStartTarget = (...args: any[]): Promise<string> => call<string>("ClipService.AutoStartTarget", ...args);
export const CancelShot = (...args: any[]): Promise<void> => call<void>("ClipService.CancelShot", ...args);
export const CaptureSize = async (..._args: any[]): Promise<[number, number]> => {
  const r: any = await call("ClipService.CaptureSize", ..._args);
  // 运行时按 Go 结构体的字段名取值，形状再断言回元组（类型由上面的 fields 保证）
  return [r?.W, r?.H] as [number, number];
};
export const CheckUpdate = (...args: any[]): Promise<Models.UpdateInfo> => call<Models.UpdateInfo>("ClipService.CheckUpdate", ...args);
export const CloseCapture = (...args: any[]): Promise<void> => call<void>("ClipService.CloseCapture", ...args);
export const CommandOutput = (...args: any[]): Promise<string> => call<string>("ClipService.CommandOutput", ...args);
export const CopyEntry = (...args: any[]): Promise<void> => call<void>("ClipService.CopyEntry", ...args);
export const CopyPlainText = (...args: any[]): Promise<void> => call<void>("ClipService.CopyPlainText", ...args);
export const CountEntriesWith = (...args: any[]): Promise<number> => call<number>("ClipService.CountEntriesWith", ...args);
export const CurrentShotPNG = (...args: any[]): Promise<string | null> => call<string | null>("ClipService.CurrentShotPNG", ...args);
export const DeleteAll = (...args: any[]): Promise<number> => call<number>("ClipService.DeleteAll", ...args);
export const DeleteEntry = (...args: any[]): Promise<void> => call<void>("ClipService.DeleteEntry", ...args);
export const DeleteFilter = (...args: any[]): Promise<void> => call<void>("ClipService.DeleteFilter", ...args);
export const DemoteFromSnippet = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.DemoteFromSnippet", ...args);
export const DownloadOCRAssets = (...args: any[]): Promise<string> => call<string>("ClipService.DownloadOCRAssets", ...args);
export const DownloadUpdate = (...args: any[]): Promise<string> => call<string>("ClipService.DownloadUpdate", ...args);
export const EnsureCaptureHotkey = (...args: any[]): Promise<void> => call<void>("ClipService.EnsureCaptureHotkey", ...args);
export const EnsureShotDefaults = (...args: any[]): Promise<void> => call<void>("ClipService.EnsureShotDefaults", ...args);
export const FallbackPaste = (...args: any[]): Promise<void> => call<void>("ClipService.FallbackPaste", ...args);
export const FlushComposerDraft = (...args: any[]): Promise<void> => call<void>("ClipService.FlushComposerDraft", ...args);
export const ForegroundProcess = (...args: any[]): Promise<string> => call<string>("ClipService.ForegroundProcess", ...args);
export const GetAppInfo = (...args: any[]): Promise<Models.AppInfo> => call<Models.AppInfo>("ClipService.GetAppInfo", ...args);
export const GetComposerDraft = (...args: any[]): Promise<string> => call<string>("ClipService.GetComposerDraft", ...args);
export const GetEntries = (...args: any[]): Promise<Models.Entry[] | null> => call<Models.Entry[] | null>("ClipService.GetEntries", ...args);
export const GetEntry = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.GetEntry", ...args);
export const GetEntryCalendar = (...args: any[]): Promise<Models.DayCount[] | null> => call<Models.DayCount[] | null>("ClipService.GetEntryCalendar", ...args);
export const GetEntryStats = (...args: any[]): Promise<Models.EntryStats> => call<Models.EntryStats>("ClipService.GetEntryStats", ...args);
export const GetEntryTags = (...args: any[]): Promise<string[] | null> => call<string[] | null>("ClipService.GetEntryTags", ...args);
export const GetFilterFields = (...args: any[]): Promise<Models.FilterFieldSpec[] | null> => call<Models.FilterFieldSpec[] | null>("ClipService.GetFilterFields", ...args);
export const GetFilters = (...args: any[]): Promise<Models.EntryFilter[] | null> => call<Models.EntryFilter[] | null>("ClipService.GetFilters", ...args);
export const GetImage = (...args: any[]): Promise<string> => call<string>("ClipService.GetImage", ...args);
export const GetImageData = (...args: any[]): Promise<string> => call<string>("ClipService.GetImageData", ...args);
export const GetSettings = (...args: any[]): Promise<Models.Settings> => call<Models.Settings>("ClipService.GetSettings", ...args);
export const GetShotInfo = (...args: any[]): Promise<Models.ShotInfo> => call<Models.ShotInfo>("ClipService.GetShotInfo", ...args);
export const GetStats = (...args: any[]): Promise<Models.Stats> => call<Models.Stats>("ClipService.GetStats", ...args);
export const GetSyncPanel = (...args: any[]): Promise<Models.SyncPanel> => call<Models.SyncPanel>("ClipService.GetSyncPanel", ...args);
export const GetSyncStatus = (...args: any[]): Promise<Models.SyncStatus> => call<Models.SyncStatus>("ClipService.GetSyncStatus", ...args);
export const GetTodos = (...args: any[]): Promise<Models.TodoRef[] | null> => call<Models.TodoRef[] | null>("ClipService.GetTodos", ...args);
export const HidePicker = (...args: any[]): Promise<void> => call<void>("ClipService.HidePicker", ...args);
export const HideWindow = (...args: any[]): Promise<void> => call<void>("ClipService.HideWindow", ...args);
export const InstallUpdate = (...args: any[]): Promise<void> => call<void>("ClipService.InstallUpdate", ...args);
export const IsAutoStartEnabled = (...args: any[]): Promise<boolean> => call<boolean>("ClipService.IsAutoStartEnabled", ...args);
export const IsOwnWindow = (...args: any[]): Promise<boolean> => call<boolean>("ClipService.IsOwnWindow", ...args);
export const LaunchApp = (...args: any[]): Promise<void> => call<void>("ClipService.LaunchApp", ...args);
export const LogClient = (...args: any[]): Promise<void> => call<void>("ClipService.LogClient", ...args);
export const Match = async (..._args: any[]): Promise<[any, number, boolean]> => {
  const r: any = await call("ClipService.Match", ..._args);
  // 运行时按 Go 结构体的字段名取值，形状再断言回元组（类型由上面的 fields 保证）
  return [r?.Trigger, r?.ConsumedLen, r?.Ok] as [any, number, boolean];
};
export const OcrAssetsDir = (...args: any[]): Promise<string> => call<string>("ClipService.OcrAssetsDir", ...args);
export const OcrEngineStatus = async (..._args: any[]): Promise<[string, boolean, string]> => {
  const r: any = await call("ClipService.OcrEngineStatus", ..._args);
  // 运行时按 Go 结构体的字段名取值，形状再断言回元组（类型由上面的 fields 保证）
  return [r?.Chosen, r?.PPOCRReady, r?.Reason] as [string, boolean, string];
};
export const OcrEntry = (...args: any[]): Promise<string> => call<string>("ClipService.OcrEntry", ...args);
export const OpenDataDir = (...args: any[]): Promise<void> => call<void>("ClipService.OpenDataDir", ...args);
export const OpenEntry = (...args: any[]): Promise<void> => call<void>("ClipService.OpenEntry", ...args);
export const OpenOcrAssetsDir = (...args: any[]): Promise<void> => call<void>("ClipService.OpenOcrAssetsDir", ...args);
export const OpenReleasePage = (...args: any[]): Promise<void> => call<void>("ClipService.OpenReleasePage", ...args);
export const PasteImage = (...args: any[]): Promise<void> => call<void>("ClipService.PasteImage", ...args);
export const PickEntry = (...args: any[]): Promise<string> => call<string>("ClipService.PickEntry", ...args);
export const PickSnippet = (...args: any[]): Promise<string> => call<string>("ClipService.PickSnippet", ...args);
export const PickerSnippets = (...args: any[]): Promise<Models.Snippet[] | null> => call<Models.Snippet[] | null>("ClipService.PickerSnippets", ...args);
export const PromoteToSnippet = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.PromoteToSnippet", ...args);
export const Quit = (...args: any[]): Promise<void> => call<void>("ClipService.Quit", ...args);
export const ReconcileAutoStart = (...args: any[]): Promise<void> => call<void>("ClipService.ReconcileAutoStart", ...args);
export const Render = async (..._args: any[]): Promise<[string, number]> => {
  const r: any = await call("ClipService.Render", ..._args);
  // 运行时按 Go 结构体的字段名取值，形状再断言回元组（类型由上面的 fields 保证）
  return [r?.Text, r?.Caret] as [string, number];
};
export const RestoreFilters = (...args: any[]): Promise<Models.EntryFilter[] | null> => call<Models.EntryFilter[] | null>("ClipService.RestoreFilters", ...args);
export const SaveCaptureSize = (...args: any[]): Promise<void> => call<void>("ClipService.SaveCaptureSize", ...args);
export const SaveEntry = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.SaveEntry", ...args);
export const SaveFilter = (...args: any[]): Promise<Models.EntryFilter> => call<Models.EntryFilter>("ClipService.SaveFilter", ...args);
export const SaveImage = (...args: any[]): Promise<string> => call<string>("ClipService.SaveImage", ...args);
export const SaveSettings = (...args: any[]): Promise<Models.Settings> => call<Models.Settings>("ClipService.SaveSettings", ...args);
export const SaveShot = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.SaveShot", ...args);
export const SaveSnippet = (...args: any[]): Promise<Models.Snippet> => call<Models.Snippet>("ClipService.SaveSnippet", ...args);
export const SetAutoStart = (...args: any[]): Promise<Models.Settings> => call<Models.Settings>("ClipService.SetAutoStart", ...args);
export const SetCapture = (...args: any[]): Promise<Models.Settings> => call<Models.Settings>("ClipService.SetCapture", ...args);
export const SetCaptureBackdrop = (...args: any[]): Promise<boolean> => call<boolean>("ClipService.SetCaptureBackdrop", ...args);
export const SetComposerDraft = (...args: any[]): Promise<void> => call<void>("ClipService.SetComposerDraft", ...args);
export const SetEntryArchived = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.SetEntryArchived", ...args);
export const SetEntryPinned = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.SetEntryPinned", ...args);
export const SetOwnWindowCheck = (...args: any[]): Promise<void> => call<void>("ClipService.SetOwnWindowCheck", ...args);
export const SetSnippetFields = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.SetSnippetFields", ...args);
export const SetTodoDone = (...args: any[]): Promise<Models.Entry> => call<Models.Entry>("ClipService.SetTodoDone", ...args);
export const SetWindowChrome = (...args: any[]): Promise<void> => call<void>("ClipService.SetWindowChrome", ...args);
export const ShotBounds = async (..._args: any[]): Promise<[number, number, number, number]> => {
  const r: any = await call("ClipService.ShotBounds", ..._args);
  // 运行时按 Go 结构体的字段名取值，形状再断言回元组（类型由上面的 fields 保证）
  return [r?.Left, r?.Top, r?.Width, r?.Height] as [number, number, number, number];
};
export const ShowCapture = (...args: any[]): Promise<void> => call<void>("ClipService.ShowCapture", ...args);
export const ShowPicker = (...args: any[]): Promise<void> => call<void>("ClipService.ShowPicker", ...args);
export const ShowWindow = (...args: any[]): Promise<void> => call<void>("ClipService.ShowWindow", ...args);
export const Shutdown = (...args: any[]): Promise<void> => call<void>("ClipService.Shutdown", ...args);
export const Start = (...args: any[]): Promise<void> => call<void>("ClipService.Start", ...args);
export const StartShot = (...args: any[]): Promise<void> => call<void>("ClipService.StartShot", ...args);
export const SyncNow = (...args: any[]): Promise<Models.SyncStatus> => call<Models.SyncStatus>("ClipService.SyncNow", ...args);
export const TestSync = (...args: any[]): Promise<string> => call<string>("ClipService.TestSync", ...args);

export const ClipService = {
  AutoStartTarget,
  CancelShot,
  CaptureSize,
  CheckUpdate,
  CloseCapture,
  CommandOutput,
  CopyEntry,
  CopyPlainText,
  CountEntriesWith,
  CurrentShotPNG,
  DeleteAll,
  DeleteEntry,
  DeleteFilter,
  DemoteFromSnippet,
  DownloadOCRAssets,
  DownloadUpdate,
  EnsureCaptureHotkey,
  EnsureShotDefaults,
  FallbackPaste,
  FlushComposerDraft,
  ForegroundProcess,
  GetAppInfo,
  GetComposerDraft,
  GetEntries,
  GetEntry,
  GetEntryCalendar,
  GetEntryStats,
  GetEntryTags,
  GetFilterFields,
  GetFilters,
  GetImage,
  GetImageData,
  GetSettings,
  GetShotInfo,
  GetStats,
  GetSyncPanel,
  GetSyncStatus,
  GetTodos,
  HidePicker,
  HideWindow,
  InstallUpdate,
  IsAutoStartEnabled,
  IsOwnWindow,
  LaunchApp,
  LogClient,
  Match,
  OcrAssetsDir,
  OcrEngineStatus,
  OcrEntry,
  OpenDataDir,
  OpenEntry,
  OpenOcrAssetsDir,
  OpenReleasePage,
  PasteImage,
  PickEntry,
  PickSnippet,
  PickerSnippets,
  PromoteToSnippet,
  Quit,
  ReconcileAutoStart,
  Render,
  RestoreFilters,
  SaveCaptureSize,
  SaveEntry,
  SaveFilter,
  SaveImage,
  SaveSettings,
  SaveShot,
  SaveSnippet,
  SetAutoStart,
  SetCapture,
  SetCaptureBackdrop,
  SetComposerDraft,
  SetEntryArchived,
  SetEntryPinned,
  SetOwnWindowCheck,
  SetSnippetFields,
  SetTodoDone,
  SetWindowChrome,
  ShotBounds,
  ShowCapture,
  ShowPicker,
  ShowWindow,
  Shutdown,
  Start,
  StartShot,
  SyncNow,
  TestSync,
};
