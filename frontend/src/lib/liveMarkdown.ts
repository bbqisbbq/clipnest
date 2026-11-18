// 实时的 Markdown 高亮：把输入框里的文本渲染成「同色同字宽的彩色副本」，
// 覆盖在透明 textarea 下面（overlay 方案）。这样打字时就能看到 **粗体**、
// `代码`、#标签 的着色，而不用切到预览，也不引入编辑器依赖。

const ESCAPES: Record<string, string> = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
};

function escapeHtml(s: string): string {
  return s.replace(/[&<>"]/g, (c) => ESCAPES[c] ?? c);
}

// 行内标记：先转义，再按顺序着色。
function inline(line: string): string {
  let out = escapeHtml(line);

  // `代码`
  out = out.replace(/`([^`\n]+)`/g, '<code class="md-code">$1</code>');
  // **粗体**
  out = out.replace(/\*\*([^*\n]+)\*\*/g, '<b class="md-strong">$1</b>');
  // *斜体*
  out = out.replace(/(^|[\s(])\*([^*\n]+)\*/g, '$1<i class="md-em">$2</i>');
  // [文字](链接)
  out = out.replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/g, '<span class="md-link">$1</span>');
  // #标签（中文、字母、数字、下划线、斜杠）
  out = out.replace(/(^|[\s(（])#([\p{L}\p{N}_/-]+)/gu, '$1<span class="md-tag">#$2</span>');

  return out;
}

// highlightMarkdown 返回一段 HTML：每个源行对应一个 <div>，保证行数与 textarea 一致。
export function highlightMarkdown(src: string): string {
  if (!src) return "";
  const lines = src.split("\n");
  return lines
    .map((line) => {
      const heading = line.match(/^(#{1,6})(\s+.*)$/);
      if (heading) {
        return `<div class="md-line md-h">${inline(heading[2].trimStart())}</div>`;
      }
      if (/^>\s?/.test(line)) {
        return `<div class="md-line md-quote">${inline(line.replace(/^>\s?/, ""))}</div>`;
      }
      const bullet = line.match(/^(\s*)([-*+]|\d+\.)(\s+)(.*)$/);
      if (bullet) {
        return `<div class="md-line">${bullet[1]}<span class="md-bullet">${bullet[2]}</span>${bullet[3]}${inline(bullet[4])}</div>`;
      }
      if (/^\s*```/.test(line)) {
        return `<div class="md-line md-fence">${escapeHtml(line)}</div>`;
      }
      // 空行也要占一行高度
      return `<div class="md-line">${line ? inline(line) : "&nbsp;"}</div>`;
    })
    .join("");
}
