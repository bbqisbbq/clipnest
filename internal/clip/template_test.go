package clip

import (
	"strings"
	"testing"
	"time"
)

func TestRenderTemplate(t *testing.T) {
	clipboardTextForTemplate = func() string { return "CLIP" }
	defer func() { clipboardTextForTemplate = nil }()

	text, cursor := renderTemplate("日期 {{date}} 时间 {{time}} 剪贴板 {{clipboard}}")
	if cursor != -1 {
		t.Errorf("cursor = %d, want -1 without marker", cursor)
	}
	if !strings.Contains(text, time.Now().Format("2006-01-02")) {
		t.Errorf("date not substituted: %q", text)
	}
	if !strings.Contains(text, "CLIP") {
		t.Errorf("clipboard not substituted: %q", text)
	}
	if strings.Contains(text, "{{") {
		t.Errorf("unsubstituted variables left: %q", text)
	}

	// {{cursor}} 决定粘贴后光标停在哪
	cursorText, cursor := renderTemplate("to: {{cursor}}@example.com")
	if cursorText != "to: @example.com" {
		t.Errorf("text = %q", cursorText)
	}
	if cursor != 4 {
		t.Errorf("cursor = %d, want 4", cursor)
	}
	if lefts := leftArrows(cursorText, cursor); lefts != len("@example.com") {
		t.Errorf("leftArrows = %d, want %d", lefts, len("@example.com"))
	}

	// 没有变量时原样返回，不引入额外开销
	if out, cur := renderTemplate("plain text"); out != "plain text" || cur != -1 {
		t.Errorf("plain text changed: %q/%d", out, cur)
	}
}

func TestPromoteEntryToSnippet(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	// 采集一条，然后把它升格成片段：同一条条目，加上缩写和 snippet 能力
	e, _, err := svc.store.SaveCapture(Captured{Kind: KindText, Text: "合同审查要点：一、…", Preview: "合同审查要点：一、…", Hash: "h-text", Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := svc.PromoteToSnippet(e.ID, "ht-")
	if err != nil {
		t.Fatalf("PromoteToSnippet: %v", err)
	}
	if promoted.ID != e.ID {
		t.Errorf("升格不应该换一条：%s vs %s", promoted.ID, e.ID)
	}
	if promoted.TriggerKey != "ht-" {
		t.Errorf("trigger = %q", promoted.TriggerKey)
	}
	if !promoted.HasAction(ActionSnippet) || !promoted.HasAction(ActionClipboard) {
		t.Errorf("应该同时具备两种能力: %v", promoted.Actions)
	}
	if promoted.Content != e.Content {
		t.Errorf("内容被改动了")
	}

	// 取消片段能力
	demoted, err := svc.DemoteFromSnippet(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if demoted.TriggerKey != "" || demoted.HasAction(ActionSnippet) {
		t.Errorf("取消失败: %+v", demoted)
	}
}
