// 实时的 Markdown 高亮：把输入框里的文本渲染成「同色同字宽的彩色副本」，
// 覆盖在透明 textarea 下面（overlay 方案）。
//
// 铁则：**只上色，绝不增删字符**。
// overlay 与 textarea 是两层，任何删掉字符（比如把 **粗体** 渲染成 <b>粗体</b>）
// 或改变字宽（加 padding / 改 font-weight）都会让两层的排版错位，接着光标就对
// 不准。所以这里只给语法标记和内容上色，字符、空白一个不动。

const ESCAPES: Record<string, string> = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
};

function escapeHtml(s: string): string {
  return s.replace(/[&<>"]/g, (c) => ESCAPES[c] ?? c);
}

// inline 处理行内语法：所有标记字符都保留，只是被不同的 span 包起来。
function inline(line: string): string {
  let out = escapeHtml(line);

  // `代码` —— 反引号保留为标记色
  out = out.replace(/`([^`\n]*)`/g, '<i class="md-mark">`</i><i class="md-code">$1</i><i class="md-mark">`</i>');
  // **粗体**
  out = out.replace(/\*\*([^*\n]*)\*\*/g, '<i class="md-mark">**</i><i class="md-strong">$1</i><i class="md-mark">**</i>');
  // *斜体*（不碰 ** 已处理过的部分）
  out = out.replace(/(^|[\s(])\*([^*\n]+)\*/g, '$1<i class="md-mark">*</i><i class="md-em">$2</i><i class="md-mark">*</i>');
  // [文字](链接)
  out = out.replace(
    /\[([^\]\n]*)\]\(([^)\s]*)\)/g,
    '<i class="md-mark">[</i><i class="md-link">$1</i><i class="md-mark">](</i><i class="md-url">$2</i><i class="md-mark">)</i>',
  );
  // #标签 —— 井号单独上色，标签名用强调色
  out = out.replace(/(^|[\s(（])#([\p{L}\p{N}_/-]+)/gu, '$1<i class="md-mark">#</i><i class="md-tag">$2</i>');

  return out;
}

// highlightMarkdown 返回一段与源文本字符一一对应的 HTML。
export function highlightMarkdown(src: string): string {
  if (!src) return "";
  return src
    .split("\n")
    .map((line) => {
      const heading = line.match(/^(#{1,6})(\s?)(.*)$/);
      if (heading) {
        const marks = `<i class="md-mark">${heading[1]}</i>${heading[2]}`;
        return `<div class="md-line">${marks}<i class="md-h">${inline(heading[3])}</i></div>`;
      }
      const quote = line.match(/^(>\s?)(.*)$/);
      if (quote) {
        return `<div class="md-line"><i class="md-mark">${escapeHtml(quote[1])}</i><i class="md-quote">${inline(quote[2])}</i></div>`;
      }
      const bullet = line.match(/^(\s*)([-*+]|\d+\.)(\s+)(.*)$/);
      if (bullet) {
        return `<div class="md-line">${bullet[1]}<i class="md-mark">${bullet[2]}</i>${bullet[3]}${inline(bullet[4])}</div>`;
      }
      if (/^\s*```/.test(line)) {
        return `<div class="md-line"><i class="md-mark">${escapeHtml(line)}</i></div>`;
      }
      // 空行也要占一行高度，但不能塞 &nbsp;（会多出一个字符宽）
      if (!line) return '<div class="md-line"><br /></div>';
      return `<div class="md-line">${inline(line)}</div>`;
    })
    .join("");
}
