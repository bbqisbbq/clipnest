import { useEffect } from "react";
import { ClipService } from "../lib/api";
import { DEFAULT_PRESET_ID, THEME_PRESETS, applyCustomCSS, applyTheme, rememberTheme, type ThemeScheme } from "../lib/themes";

// 主题落地的唯一入口：配色预设 + 深浅方案 + 自定义 CSS，外加原生窗口边框。
// 放在 hook 里，任何需要"应用主题"的地方都调它，而不是各自拼一遍。

export function useTheme(settings: { theme?: string; themePreset?: string; themeCustomCss?: string } | null) {
  const scheme = (settings?.theme ?? "system") as ThemeScheme;
  const preset = settings?.themePreset ?? DEFAULT_PRESET_ID;
  const css = settings?.themeCustomCss ?? "";

  useEffect(() => {
    applyTheme(scheme, preset);
    applyCustomCSS(css);
    // 记住选择，下一次启动在 React 挂载前就能先画对颜色（见 main.tsx）
    rememberTheme(scheme, preset);

    // 原生窗口边框/标题区也要跟着换，否则浅色主题下会残留系统深色边
    const def = THEME_PRESETS.find((p) => p.id === preset) ?? THEME_PRESETS[0];
    void ClipService.SetWindowChrome(def.scheme === "dark", def.tokens["--bg"] ?? "").catch(() => {});

    const mq = window.matchMedia?.("(prefers-color-scheme: light)");
    const onChange = () => {
      if (scheme === "system") applyTheme(scheme, preset);
    };
    mq?.addEventListener("change", onChange);
    return () => mq?.removeEventListener("change", onChange);
  }, [scheme, preset, css]);
}
