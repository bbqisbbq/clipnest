import * as ClipService from "../../bindings/github.com/bbqisbbq/clipnest/internal/clip/clipservice";
import * as Models from "../../bindings/github.com/bbqisbbq/clipnest/internal/clip/models";
import * as EventNames from "../../bindings/events";

export { ClipService, Models, EventNames };

export type Item = Models.Item;
export type FileRef = Models.FileRef;
export type Settings = Models.Settings;
export type Stats = Models.Stats;
export type AppInfo = Models.AppInfo;
export type Filter = Models.Filter;
export type SyncStatus = Models.SyncStatus;
export type UpdateInfo = Models.UpdateInfo;
export type SyncPanel = Models.SyncPanel;
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

// newFilter builds the Filter payload the Go side expects.
export function newFilter(query = "", kind = "", pinned = false, limit = 400): Filter {
  return { query, kind, pinned, limit, offset: 0 };
}

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
    ignorePatterns: [],
    ignoreApps: [],
    hotkey: "Ctrl+Shift+V",
    pasteOnPick: true,
    showOnLaunch: false,
    closeToTray: true,
    autoStart: false,
    theme: "system",
    syncEnabled: false,
    syncUrl: "",
    syncUser: "",
    syncPass: "",
    syncFile: "clipnest-sync.json",
    syncIntervalMin: 15,
    snippetsInPicker: true,
    expandEnabled: false,
    expandSuffix: '-',
    syncSettings: true,
    syncDeviceId: '',
    updateCheck: true,
    syncUpdatedAt: '',
  };
}
