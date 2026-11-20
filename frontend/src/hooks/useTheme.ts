import { useEffect } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService, EventNames } from "../lib/api";
import { DEFAULT_PRESET_ID, THEME_PRESETS, applyCustomCSS, applyTheme, rememberTheme, type ThemeScheme } from "../lib/themes";

// 主题落地的唯一入口：配色预设 + 深浅方案 + 自定义 CSS，外加原生窗口边框。
// 任何需要"应用主题"的地方都调这里，而不是各自拼一遍。

type ThemeSettings = { theme?: string; themePreset?: string; themeCustomCss?: string } | null;

export function applySettingsTheme(settings: ThemeSettings) {
  const scheme = (settings?.theme ?? "system") as ThemeScheme;
  const preset = settings?.themePreset ?? DEFAULT_PRESET_ID;

  applyTheme(scheme, preset);
  applyCustomCSS(settings?.themeCustomCss ?? "");
  // 记住选择，下一次启动在 React 挂载前就能先画对颜色（见 main.tsx）
  rememberTheme(scheme, preset);

  // 原生窗口边框/标题区也要跟着换，否则浅色主题下会残留系统深色边。
  // 这条对弹窗一样重要：DWM 的 Acrylic 底色（弹窗半透明要用的那层）跟着
  // immersive-dark 标志走，标志不更新的话浅色主题下会糊出一层深色。
  const def = THEME_PRESETS.find((p) => p.id === preset) ?? THEME_PRESETS[0];
  void ClipService.SetWindowChrome(def.scheme === "dark", def.tokens["--bg"] ?? "").catch(() => {});
}

// useTheme 给主窗口用：设置已经在手边（App 的 useAppData 里），变了就重画。
export function useTheme(settings: ThemeSettings) {
  const scheme = (settings?.theme ?? "system") as ThemeScheme;
  const preset = settings?.themePreset ?? DEFAULT_PRESET_ID;
  const css = settings?.themeCustomCss ?? "";

  useEffect(() => {
    applySettingsTheme(settings);
    const mq = window.matchMedia?.(`(prefers-color-scheme: light)`);
    const onChange = () => {
      if (scheme === "system") applyTheme(scheme, preset);
    };
    mq?.addEventListener("change", onChange);
    return () => mq?.removeEventListener("change", onChange);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scheme, preset, css]);
}

// useSettingsTheme 给**弹窗**用（快速粘贴 / 记一笔）：它们没有 useAppData，
// 只靠这条订阅跟着设置走。
//
// 以前两个弹窗只在 main.tsx 里跑一次 `applyTheme(storedTheme())`——也就是"上次启动时
// 记在 localStorage 里的那套"。于是应用跑着的时候在设置页换主题，主窗口当场变色，
// 弹窗却还是老配色，得重启才跟上（issue: 记一笔不跟随主题）。
export function useSettingsTheme() {
  useEffect(() => {
    let alive = true;
    const apply = (st: ThemeSettings) => {
      if (alive) applySettingsTheme(st);
    };
    void ClipService.GetSettings()
      .then((st) => apply(st as ThemeSettings))
      .catch(() => undefined);
    const off = Events.On(EventNames.SettingsChanged, (ev) => {
      apply(ev.data as ThemeSettings);
    });
    return () => {
      alive = false;
      off();
    };
  }, []);
}

