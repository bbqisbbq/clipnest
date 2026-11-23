package clip

// 条目标题：从正文"第一个一级标题"派生，手写的优先且不再被覆盖。
//
// 为什么派生而不是要求用户填：条目标题唯一的自然来源就是正文（像 Markdown 文档）。
// 但**派生值同时落库**，不是每次读都现算——理由有两条：
//   · 卡片/待办/同步都要用它，现算等于每次读都扫一遍正文；
//   · 用户可能手写覆盖，那就得有个地方记住"这是手写的"，否则下次保存又被正文盖掉。
// 于是 `title` 存的是"当前生效的标题"，`title_source` 说明它是哪来的。

import (
	"regexp"
	"strings"
)

// 标题来源（entries.title_source）。
const (
	// TitleSourceAuto 表示标题是从正文的一级标题派生的，正文变了就跟着变。
	TitleSourceAuto = "auto"
	// TitleSourceUser 表示标题是人手写的，正文再怎么变都不动它。
	TitleSourceUser = "user"
)

var (
	// CommonMark 的 ATX 一级标题：`#` + 至少一个空格 + 内容。
	// 注意 `#标签` 中间没有空格 —— 那是标签，不是标题，两种语法在正文里紧挨着。
	titleH1RE = regexp.MustCompile(`^#\s+(\S.*)$`)
	// 围栏代码块的开始（``` 或 ~~~，可带语言名）。
	titleFenceRE = regexp.MustCompile("^(`{3,}|~{3,})")
	// ATX 标题的收尾井号（`# 标题 ###`）。
	titleCloseRE = regexp.MustCompile(`\s+#+\s*$`)
	// 行内强调与代码标记，标题里不该带出来。
	titleInlineRE = regexp.MustCompile("[*`~]+")
	// 链接只留文字：`[文字](url)` → 文字。
	titleLinkRE = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)

// deriveTitle returns the entry's first level-1 heading, or "".
//
// 跳过的东西和正文渲染器保持一致：围栏代码块里的 `# xxx` 是代码注释，缩进 4 空格的
// 同样算代码；二级及以下的标题不参与（那是章节，不是文档标题）。
func deriveTitle(content string) string {
	inFence := false
	fence := ""
	for _, raw := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		body := strings.TrimLeft(line, " \t")

		if inFence {
			if strings.HasPrefix(body, fence) && strings.Trim(body, "`~") == "" {
				inFence = false
			}
			continue
		}
		if m := titleFenceRE.FindStringSubmatch(body); m != nil {
			inFence, fence = true, m[1]
			continue
		}
		if len(line)-len(body) >= 4 {
			continue // 缩进代码块
		}
		if m := titleH1RE.FindStringSubmatch(body); m != nil {
			if title := cleanTitle(m[1]); title != "" {
				return title
			}
		}
	}
	return ""
}

// cleanTitle strips the markup a heading line can carry, so the value is safe to
// show on a card: 收尾井号、强调/代码标记、链接只留文字、转义符还原。
func cleanTitle(s string) string {
	s = titleCloseRE.ReplaceAllString(s, "")
	s = titleLinkRE.ReplaceAllString(s, "$1")
	s = titleInlineRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, `\`, "")
	return strings.TrimSpace(s)
}

// normalizeTitle decides the entry's title before it is written.
//
// 规则（也是界面上的三种状态）：
//   - 手写的留着；人把它清空了就退回自动派生；
//   - 自动的跟着正文重算，正文里没有一级标题了就变回空（卡片不多那一行）；
//   - 来源不明（前端没传、对端老版本同步来的）：标题与派生值相同就认作自动，
//     否则当手写 —— 这样升级后不会把一批历史条目错锁成"手写"。
func normalizeTitle(in *Entry) {
	in.Title = truncateRunes(strings.TrimSpace(in.Title), 60)
	switch in.TitleSource {
	case TitleSourceUser:
		if in.Title != "" {
			return
		}
		in.TitleSource = ""
	case TitleSourceAuto:
		in.Title = deriveTitle(in.Content)
		if in.Title == "" {
			in.TitleSource = ""
		}
		return
	}

	derived := deriveTitle(in.Content)
	if in.Title != "" {
		if in.Title == derived {
			in.TitleSource = TitleSourceAuto
		} else {
			in.TitleSource = TitleSourceUser
		}
		return
	}
	if derived != "" {
		in.Title = derived
		in.TitleSource = TitleSourceAuto
	}
}
