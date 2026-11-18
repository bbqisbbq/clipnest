// 主题引擎：一套设计 token × 若干预设 × 可选自定义 CSS。
//
// 参照 Memos 的做法（token 表 + data-theme + 额外样式），但我们只用 Tailwind-free
// 的语义 token 名（--surface-2 / --text-3 …），预设就是"一份 token 赋值"。
//
// 三个概念分开，别混：
//   scheme  浅色/深色（决定 color-scheme 与原生控件外观）→ settings.theme
//   preset  一整套配色（palette）→ settings.themePreset
//   css     用户自己写的覆盖样式 → settings.themeCustomCSS

export type ThemeScheme = "system" | "light" | "dark";

// ThemeTokens 是主题能改的全部变量；缺省则回落到 :root 里的值。
export type ThemeTokens = Record<string, string>;

export type ThemePreset = {
  id: string;
  name: string;
  scheme: "light" | "dark";
  note: string;
  tokens: ThemeTokens;
};

export const THEME_PRESETS: ThemePreset[] = [
  {
    id: "default-dark",
    name: "默认深色",
    scheme: "dark",
    note: "原来的深色，冷灰底 + 靛蓝强调色",
    tokens: {
      "--bg": "#0a0b0f",
      "--surface": "#0e1015",
      "--surface-2": "#13161c",
      "--surface-3": "#191d25",
      "--border": "#21252e",
      "--border-soft": "#1a1e26",
      "--border-strong": "#2c313b",
      "--text": "#e9ebf0",
      "--text-2": "#a4abba",
      "--text-3": "#6e7686",
      "--accent": "#7d8cff",
      "--accent-2": "#9d7bff",
      "--accent-tint": "rgba(125, 140, 255, 0.14)",
      "--accent-line": "rgba(125, 140, 255, 0.42)",
      "--ok": "#45c78d",
      "--warn": "#e0a253",
      "--danger": "#ee6a6a",
    },
  },
  {
    id: "default-light",
    name: "亮白",
    scheme: "light",
    note: "白底深字，适合白天 / 亮环境",
    tokens: {
      "--bg": "#f4f5f8",
      "--surface": "#ffffff",
      "--surface-2": "#f2f4f8",
      "--surface-3": "#e9ecf2",
      "--border": "#dfe3ea",
      "--border-soft": "#e8ebf1",
      "--border-strong": "#cfd5df",
      "--text": "#1b1f27",
      "--text-2": "#4b5563",
      "--text-3": "#7c8598",
      "--accent": "#5566e8",
      "--accent-2": "#7c5ce0",
      "--accent-tint": "rgba(85, 102, 232, 0.12)",
      "--accent-line": "rgba(85, 102, 232, 0.4)",
      "--ok": "#14875f",
      "--warn": "#a9691a",
      "--danger": "#c8393f",
    },
  },
  {
    id: "paper",
    name: "纸感",
    scheme: "light",
    note: "暖白纸面 + 墨色主调，学 Memos 的 paper",
    tokens: {
      "--bg": "#f3ede0",
      "--surface": "#fbf7ee",
      "--surface-2": "#efe8d9",
      "--surface-3": "#e6dcc8",
      "--border": "#d9cdb4",
      "--border-soft": "#e4dbc7",
      "--border-strong": "#c4b393",
      "--text": "#20180f",
      "--text-2": "#5a4c39",
      "--text-3": "#8a7b64",
      "--accent": "#7a4a2b",
      "--accent-2": "#9a6236",
      "--accent-tint": "rgba(122, 74, 43, 0.14)",
      "--accent-line": "rgba(122, 74, 43, 0.42)",
      "--ok": "#3f7a4a",
      "--warn": "#9a6a12",
      "--danger": "#a8322b",
    },
  },
  {
    id: "ink",
    name: "墨黑",
    scheme: "dark",
    note: "近黑底 + 琥珀强调，夜里写东西不刺眼",
    tokens: {
      "--bg": "#0c0c0d",
      "--surface": "#121213",
      "--surface-2": "#171718",
      "--surface-3": "#1e1e20",
      "--border": "#28282b",
      "--border-soft": "#202022",
      "--border-strong": "#35353a",
      "--text": "#eceae4",
      "--text-2": "#b0aca2",
      "--text-3": "#79756c",
      "--accent": "#d9a44a",
      "--accent-2": "#c8873a",
      "--accent-tint": "rgba(217, 164, 74, 0.16)",
      "--accent-line": "rgba(217, 164, 74, 0.42)",
      "--ok": "#63b36f",
      "--warn": "#d9a44a",
      "--danger": "#dd6a5f",
    },
  },
];

export function presetByID(id: string | undefined): ThemePreset {
  return THEME_PRESETS.find((p) => p.id === id) ?? THEME_PRESETS[0];
}

function systemPrefersLight(): boolean {
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ?? false;
}

// resolveScheme 把 "system" 落成实际值。
export function resolveScheme(scheme: ThemeScheme): "light" | "dark" {
  if (scheme === "light") return "light";
  if (scheme === "dark") return "dark";
  return systemPrefersLight() ? "light" : "dark";
}

// applyTheme 写入当前主题：data-theme 决定 color-scheme，行内变量决定配色。
export function applyTheme(scheme: ThemeScheme, presetID: string | undefined) {
  const root = document.documentElement;
  const preset = presetByID(presetID);
  const resolved = resolveScheme(scheme);

  root.dataset.theme = resolved;
  root.dataset.preset = preset.id;
  // 预设与浅/深不一致时（例如选了 paper 但开着深色），以预设为准并同步 color-scheme
  root.dataset.scheme = preset.scheme;

  for (const [key, value] of Object.entries(preset.tokens)) {
    root.style.setProperty(key, value);
  }
}

// ---- 自定义 CSS --------------------------------------------------------

const CUSTOM_STYLE_ID = "clipnest-custom-css";

// sanitizeCustomCSS 拒绝会把请求发到外网 / 覆盖全局布局的写法。
// 返回清理后的 CSS 与告警列表。
export function sanitizeCustomCSS(css: string): { css: string; warnings: string[] } {
  const warnings: string[] = [];
  let out = css ?? "";

  if (/@import/i.test(out)) {
    warnings.push("已移除 @import（会向外部加载样式表）");
    out = out.replace(/@import[^;]+;/gi, "");
  }
  if (/url\(\s*['"]?https?:/i.test(out)) {
    warnings.push("已移除远程 url(...)（会用你的网络去请求外部资源）");
    out = out.replace(/url\(\s*['"]?https?:[^)]*\)/gi, "none");
  }
  if (/expression\s*\(/i.test(out)) {
    warnings.push("已移除 expression(…)（IE 时代的脚本执行入口）");
    out = out.replace(/expression\s*\([^)]*\)/gi, "none");
  }
  if (/behavior\s*:/i.test(out)) {
    warnings.push("已移除 behavior:（HTC 脚本入口）");
    out = out.replace(/behavior\s*:[^;]+;/gi, "");
  }
  return { css: out, warnings };
}

// applyCustomCSS 把用户样式注入（或清除）。
export function applyCustomCSS(raw: string) {
  const existing = document.getElementById(CUSTOM_STYLE_ID);
  const { css } = sanitizeCustomCSS(raw);
  if (!css.trim()) {
    existing?.remove();
    return;
  }
  const el = (existing as HTMLStyleElement | null) ?? document.createElement("style");
  el.id = CUSTOM_STYLE_ID;
  el.textContent = css;
  if (!existing) document.head.appendChild(el);
}

// ---- 对比度自检 --------------------------------------------------------

function toRGB(color: string): [number, number, number] | null {
  const c = color.trim();
  const hex = c.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i);
  if (hex) {
    let v = hex[1];
    if (v.length === 3) v = v.split("").map((ch) => ch + ch).join("");
    return [parseInt(v.slice(0, 2), 16), parseInt(v.slice(2, 4), 16), parseInt(v.slice(4, 6), 16)];
  }
  const rgb = c.match(/^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i);
  if (rgb) return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])];
  return null;
}

function luminance([r, g, b]: [number, number, number]): number {
  const f = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

export function contrastRatio(fg: string, bg: string): number {
  const a = toRGB(fg);
  const b = toRGB(bg);
  if (!a || !b) return 0;
  const la = luminance(a);
  const lb = luminance(b);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

export type ContrastCheck = { label: string; ratio: number; ok: boolean };

// checkContrast 检查关键文字对的对比度（WCAG AA = 4.5:1）。
export function checkContrast(tokens: ThemeTokens): ContrastCheck[] {
  const bg = tokens["--bg"] ?? "#000";
  const surface = tokens["--surface"] ?? bg;
  const pairs: Array<[string, string]> = [
    ["正文 / 背景", tokens["--text"] ?? "#fff"],
    ["次要文字 / 背景", tokens["--text-2"] ?? "#ccc"],
    ["弱化文字 / 背景", tokens["--text-3"] ?? "#999"],
    ["强调色 / 背景", tokens["--accent"] ?? "#88f"],
  ];
  return pairs.map(([label, fg]) => {
    const ratio = Math.min(contrastRatio(fg, bg), contrastRatio(fg, surface));
    return { label, ratio: Math.round(ratio * 100) / 100, ok: ratio >= 4.5 };
  });
}

// 当前页面实际生效的 token（预设 + 自定义 CSS 之后），用于自检。
export function currentTokens(): ThemeTokens {
  const style = getComputedStyle(document.documentElement);
  const keys = Object.keys(THEME_PRESETS[0].tokens);
  const out: ThemeTokens = {};
  for (const k of keys) out[k] = style.getPropertyValue(k).trim();
  return out;
}
