// 片段模板变量：与 internal/clip/template.go 的 renderTemplate 一一对应。
//
// 增删变量必须两边一起改，否则会出现"界面能插进去、展开时却不替换"（或反过来）。
// 展开时由后端替换，所以这里只负责给用户一个能点的地方 —— 记住 `{{cursor}}`
// 的语义是"之后把光标停在这里"，其它变量是就地替换。

export type SnippetVar = {
  token: string;
  label: string;
  hint: string;
};

export const SNIPPET_VARS: SnippetVar[] = [
  { token: "{{date}}", label: "日期", hint: "展开时替换成 2026-11-19" },
  { token: "{{date:long}}", label: "日期（长）", hint: "展开时替换成 2026年11月19日" },
  { token: "{{time}}", label: "时间", hint: "展开时替换成 16:20" },
  { token: "{{datetime}}", label: "日期时间", hint: "展开时替换成 2026-11-19 16:20" },
  { token: "{{clipboard}}", label: "剪贴板", hint: "展开那一刻剪贴板里的文本" },
  { token: "{{cursor}}", label: "光标位置", hint: "展开后光标停在这里（变量本身会被删掉）" },
];
