import { useSyncExternalStore } from "react";
import { Events } from "./runtime";
import { ClipService, EventNames } from "./api";
import { repaintPrivacy, setPrivacyEnabled } from "./privacyMask";

// 隐私模式的开关状态。三个写字的地方（详情 / 记一条 / 热键快速输入窗）共用一份，
// 存在 Settings.PrivacyInput 里（机器本地字段，不进同步包）——存 localStorage 不行：
// 每个 Wails 窗口是独立的 WebView，弹窗里改了主窗口看不见。

let on = false;
const listeners = new Set<() => void>();
let loading: Promise<void> | null = null;
let wired = false;

function publish(): void {
  for (const fn of listeners) fn();
}

function apply(next: boolean): void {
  if (next === on) return;
  on = next;
  setPrivacyEnabled(next);
  publish();
}

/** 当前值（useSyncExternalStore 的快照函数，必须是稳定引用）。 */
export function privacyModeOn(): boolean {
  return on;
}

function loadOnce(): Promise<void> {
  if (!loading) {
    loading = ClipService.GetSettings()
      .then((st) => apply(!!st.privacyInput))
      .catch(() => undefined);
  }
  return loading;
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  void loadOnce();
  return () => {
    listeners.delete(fn);
  };
}

/** 组件里用：`const privacy = usePrivacyMode()`，配合 <PrivacyToggle />。 */
export function usePrivacyMode(): boolean {
  return useSyncExternalStore(subscribe, privacyModeOn, privacyModeOn);
}

/** 切换开关：先让界面立刻生效，再把设置落盘（失败只记日志，不回滚视觉）。 */
export async function togglePrivacyMode(): Promise<void> {
  const next = !on;
  apply(next);
  repaintPrivacy();
  try {
    const st = await ClipService.GetSettings();
    st.privacyInput = next;
    await ClipService.SaveSettings(st);
  } catch (err) {
    void ClipService.LogClient(`privacy: save failed: ${String(err)}`);
  }
}

/**
 * 订阅"别处改了它"：另一个窗口点开关、或设置页改了值，都跟着变。
 * 每个窗口调一次即可（main.tsx 里统一调）。
 */
export function watchPrivacyMode(): void {
  if (wired) return;
  wired = true;
  void loadOnce();
  try {
    // 不保存返回值：订阅活到这个窗口销毁，没有需要解绑的时机。
    Events.On(EventNames.SettingsChanged, (ev) => {
      const st = ev.data as { privacyInput?: boolean } | null | undefined;
      if (st && typeof st.privacyInput === "boolean") apply(st.privacyInput);
    });
  } catch {
    /* 事件桥没起来也不影响开关本身 */
  }
}

