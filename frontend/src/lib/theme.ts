// 主题：settings.theme 取 "system" | "dark" | "light"。
// 只做一件事——把最终结果写到 <html data-theme>，颜色全部由 CSS token 决定。

export type ThemeChoice = "system" | "dark" | "light";

const media = () => window.matchMedia?.("(prefers-color-scheme: light)");

function resolve(choice: ThemeChoice): "dark" | "light" {
  if (choice === "light") return "light";
  if (choice === "dark") return "dark";
  return media()?.matches ? "light" : "dark";
}

export function applyTheme(choice: ThemeChoice) {
  const root = document.documentElement;
  root.dataset.theme = resolve(choice);
  root.dataset.themeChoice = choice;
}

// watchSystemTheme 在 choice === "system" 时跟随系统切换。
export function watchSystemTheme(getChoice: () => ThemeChoice): () => void {
  const mq = media();
  if (!mq) return () => {};
  const onChange = () => {
    if (getChoice() === "system") applyTheme("system");
  };
  mq.addEventListener("change", onChange);
  return () => mq.removeEventListener("change", onChange);
}

export function themeLabel(choice: ThemeChoice): string {
  return choice === "light" ? "浅色" : choice === "dark" ? "深色" : "跟随系统";
}
