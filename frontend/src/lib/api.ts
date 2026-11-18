import * as ClipService from "../../bindings/github.com/bbqisbbq/clipnest/internal/clip/clipservice";
import * as Models from "../../bindings/github.com/bbqisbbq/clipnest/internal/clip/models";
import * as EventNames from "../../bindings/events";

export { ClipService, Models, EventNames };

export type FileRef = Models.FileRef;
export type Settings = Models.Settings;
export type Stats = Models.Stats;
export type AppInfo = Models.AppInfo;
export type SyncStatus = Models.SyncStatus;
export type UpdateInfo = Models.UpdateInfo;
export type SyncPanel = Models.SyncPanel;
export type Entry = Models.Entry;
export type EntryQuery = Models.EntryQuery;
export type EntryFilter = Models.EntryFilter;
export type EntryStats = Models.EntryStats;
export type DayCount = Models.DayCount;
export type Snippet = Models.Snippet;
export type SnippetKind = Models.SnippetKind;
export type SyncDeviceInfo = Models.SyncDeviceInfo;
export type SyncDetail = Models.SyncDetail;

export const KIND_LABEL: Record<string, string> = {
  text: "文本",
  link: "链接",
  image: "图片",
  files: "文件",
  rich: "富文本",
  custom: "其它格式",
};

export function emptySettings(): Settings {
  return {
    capture: true,
    captureText: true,
    captureLink: true,
    captureImage: true,
    captureFiles: true,
    captureRich: true,
    captureCustom: true,
    maxTextLength: 100000,
    maxItems: 1000,
    maxAgeDays: 30,
    hotkey: "Ctrl+Shift+V",
    captureHotkey: "Ctrl+Shift+N",
    pasteOnPick: true,
    showOnLaunch: false,
    closeToTray: true,
    autoStart: false,
    theme: "system",
    themePreset: "default-dark",
    navOrder: ["history", "snippets", "memos"],
    navLabels: {},
    navIcons: {},
    themeCustomCss: "",
    syncEnabled: false,
    syncUrl: "",
    syncUser: "",
    syncPass: "",
    syncFile: "clipnest-sync.json",
    syncIntervalMin: 15,
    snippetsInPicker: true,
    expandEnabled: false,
    expandSuffix: '-',
    expandEndChars: ' \\t,.;:!?)]}',
    syncSettings: true,
    syncDeviceId: '',
    updateCheck: true,
    syncUpdatedAt: '',
  };
}
