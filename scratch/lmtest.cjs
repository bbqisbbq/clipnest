const { highlightMarkdown } = require(process.env.TEMP + "\\lm.cjs");
const strip = (html) => html.replace(/<br \/>/g, "").replace(/<[^>]+>/g, "")
  .replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"');
const cases = [
  "#光标位置不对",
  "今天 #工作 和 #会议 都在这里",
  "**粗体** 和 *斜体* 以及 `code`",
  "# 标题 一行",
  "> 引用一行",
  "- 列表项 #标签",
  "1. 有序 #a1",
  "[文字](https://x.com) 结尾",
  "```go\nfunc main() {}\n```",
  "多行\n\n空行上面\n\n\n下面",
  "纯文本没有标记",
  "中英混排 mixed 内容 #tag-x/y",
];
let bad = 0;
for (const src of cases) {
  const got = strip(highlightMarkdown(src));
  const want = src.replace(/\n/g, "");
  if (got !== want) {
    bad++;
    console.log("MISMATCH\n  src:", JSON.stringify(src), "\n  got:", JSON.stringify(got));
  }
}
console.log(bad === 0 ? `全部 ${cases.length} 个用例：高亮层纯文本与原文逐字一致 ✅` : `${bad} 个用例不一致 ❌`);
