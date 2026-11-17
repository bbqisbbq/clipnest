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

func TestSaveItemAsSnippet(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	textItem, _, err := svc.store.Add(Captured{Kind: KindText, Text: "合同审查要点：一、…", Preview: "合同审查要点：一、…", Hash: "h-text", Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	sn, err := svc.SaveItemAsSnippet(textItem.ID)
	if err != nil {
		t.Fatalf("SaveItemAsSnippet(text): %v", err)
	}
	if sn.Kind != SnippetText || sn.Body != textItem.Text {
		t.Errorf("unexpected snippet: %+v", sn)
	}

	linkItem, _, _ := svc.store.Add(Captured{Kind: KindLink, Text: "https://example.com", Preview: "https://example.com", Hash: "h-link", Size: 19})
	if sn, err := svc.SaveItemAsSnippet(linkItem.ID); err != nil || sn.Kind != SnippetURL {
		t.Errorf("link → %+v err=%v", sn, err)
	}

	imgItem, _, _ := svc.store.Add(Captured{Kind: KindImage, PNG: tinyPNG(t), Width: 2, Height: 2, Preview: "2 × 2 图像", Hash: "h-img", Size: 100})
	if sn, err := svc.SaveItemAsSnippet(imgItem.ID); err != nil {
		t.Fatalf("image → %v", err)
	} else if payload, err := svc.store.SnippetBlob(sn.ID); err != nil || len(payload) == 0 {
		t.Errorf("image payload not copied: %v", err)
	}
}
