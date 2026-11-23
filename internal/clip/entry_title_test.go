package clip

import (
	"strings"
	"testing"
)

// 标题派生：只认 CommonMark 的 ATX 一级标题，代码块里的不算，`#标签` 更不算。
func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通一级标题", "# 周报\n正文", "周报"},
		{"前面有空行", "\n\n# 周报\n", "周报"},
		{"二级标题不算", "## 二级\n正文", ""},
		{"标签不算（# 后没空格）", "#工作 今天做完\n正文", ""},
		{"取第一个一级标题", "正文\n\n# 第一个\n\n# 第二个", "第一个"},
		{"收尾井号去掉", "## x\n# 标题 ###\n", "标题"},
		{"行内强调去掉", "# **加粗**的`标题`\n", "加粗的标题"},
		{"链接只留文字", "# 看[这里](https://a.b)\n", "看这里"},
		{"围栏代码块里的不算", "```go\n# 注释\n```\n# 真标题\n", "真标题"},
		{"波浪围栏也算代码", "~~~\n# 注释\n~~~\n", ""},
		{"缩进 4 空格算代码", "    # 注释\n# 真标题\n", "真标题"},
		{"空内容", "", ""},
		{"只有题目没有正文", "# 只有标题", "只有标题"},
		{"CRLF 也要认", "# 标题\r\n正文\r\n", "标题"},
		{"转义井号还原不了就跳过标记", `# C\# 入门` + "\n", "C# 入门"},
	}
	for _, c := range cases {
		if got := deriveTitle(c.in); got != c.want {
			t.Errorf("%s: deriveTitle(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// SaveEntry 是唯一写入口：标题的三种状态都在这里判。
func TestSaveEntryTitleLifecycle(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	// ① 新建：正文有一级标题 → 自动派生
	e, err := svc.store.SaveEntry(Entry{Content: "# 会议纪要\n\n讨论了三件事。", Source: SourceManual})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if e.Title != "会议纪要" || e.TitleSource != TitleSourceAuto {
		t.Fatalf("自动派生失败: title=%q source=%q", e.Title, e.TitleSource)
	}

	// ② 自动标题跟着正文变
	e.Content = "# 会议纪要（修订）\n\n内容改了。"
	e, err = svc.store.SaveEntry(e)
	if err != nil {
		t.Fatalf("SaveEntry(改正文): %v", err)
	}
	if e.Title != "会议纪要（修订）" || e.TitleSource != TitleSourceAuto {
		t.Fatalf("自动标题没跟着正文变: title=%q source=%q", e.Title, e.TitleSource)
	}

	// ③ 正文里的一级标题删掉 → 标题清空（卡片不多那一行）
	e.Content = "没有标题的正文。"
	e, err = svc.store.SaveEntry(e)
	if err != nil {
		t.Fatalf("SaveEntry(删标题): %v", err)
	}
	if e.Title != "" || e.TitleSource != "" {
		t.Fatalf("正文没有一级标题时应清空: title=%q source=%q", e.Title, e.TitleSource)
	}

	// ④ 手写标题：正文再改也不动
	e.Title, e.TitleSource = "我自己的名字", TitleSourceUser
	e, err = svc.store.SaveEntry(e)
	if err != nil {
		t.Fatalf("SaveEntry(手写): %v", err)
	}
	e.Content = "# 正文里冒出来的标题\n\n又改了。"
	e, err = svc.store.SaveEntry(e)
	if err != nil {
		t.Fatalf("SaveEntry(手写后再改正文): %v", err)
	}
	if e.Title != "我自己的名字" || e.TitleSource != TitleSourceUser {
		t.Fatalf("手写标题被正文覆盖了: title=%q source=%q", e.Title, e.TitleSource)
	}

	// ⑤ 把标题清空 → 退回自动派生
	e.Title, e.TitleSource = "", ""
	e, err = svc.store.SaveEntry(e)
	if err != nil {
		t.Fatalf("SaveEntry(清空标题): %v", err)
	}
	if e.Title != "正文里冒出来的标题" || e.TitleSource != TitleSourceAuto {
		t.Fatalf("清空后没有退回派生: title=%q source=%q", e.Title, e.TitleSource)
	}
}

// 升级兼容：对端老版本同步来的条目没有 title_source，标题恰好等于派生值时应认作
// 自动（否则会被错锁成"手写"，之后正文改了标题也不动）。
func TestNormalizeTitleLegacyRows(t *testing.T) {
	legacyAuto := Entry{Content: "# 老条目\n正文", Title: "老条目"}
	normalizeTitle(&legacyAuto)
	if legacyAuto.TitleSource != TitleSourceAuto {
		t.Errorf("标题等于派生值时应认作 auto，得到 %q", legacyAuto.TitleSource)
	}

	legacyUser := Entry{Content: "# 老条目\n正文", Title: "我自己起的"}
	normalizeTitle(&legacyUser)
	if legacyUser.TitleSource != TitleSourceUser {
		t.Errorf("标题与正文不一致时应认作 user，得到 %q", legacyUser.TitleSource)
	}
}

// 采集进来的内容也走同一套：复制的 Markdown 片段带一级标题时，卡片上就有标题。
func TestSaveCaptureDerivesTitle(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, _, err := svc.store.SaveCapture(Captured{
		Kind: KindText, Text: "# 复制来的标题\n\n正文", Hash: "cap-title",
	})
	if err != nil {
		t.Fatalf("SaveCapture: %v", err)
	}
	if e.Title != "复制来的标题" {
		t.Fatalf("采集条目没派生出标题: %q", e.Title)
	}
}

// 片段：用户起的名字就是手写标题，不能被正文的一级标题顶掉。
func TestSnippetTitleIsUserSource(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, err := svc.store.SaveEntry(Entry{
		Content: "# 正文里的标题\n\n片段内容", Title: "片段名", Source: SourceManual, SnippetKind: "text",
		Actions: []string{ActionSnippet},
	})
	if err != nil {
		t.Fatalf("SaveEntry(snippet): %v", err)
	}
	if e.Title != "片段名" || e.TitleSource != TitleSourceUser {
		t.Fatalf("片段标题被覆盖: title=%q source=%q", e.Title, e.TitleSource)
	}
	if !strings.Contains(e.Content, "# 正文里的标题") {
		t.Errorf("正文被改动了: %q", e.Content)
	}
}
