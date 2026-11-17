package clip

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestSnippetCRUDAndTriggerLookup(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	saved, err := svc.SaveSnippet(Snippet{
		Kind:    SnippetText,
		Title:   "办公地址",
		Trigger: "addr-",
		Group:   "客户",
		Body:    "杭州市滨江区 · ETool 科技园 A 座 1208",
	}, "")
	if err != nil {
		t.Fatalf("SaveSnippet: %v", err)
	}
	if saved.ID == "" || saved.Hash == "" {
		t.Fatalf("snippet not persisted: %+v", saved)
	}
	if saved.UpdatedAt.IsZero() {
		t.Error("updatedAt should be set")
	}

	list := svc.GetSnippets("", "")
	if len(list) != 1 {
		t.Fatalf("GetSnippets = %d, want 1", len(list))
	}
	if got := svc.GetSnippetGroups(); len(got) != 1 || got[0] != "客户" {
		t.Errorf("groups = %v", got)
	}

	// Trigger lookup is case-insensitive and feeds the inline expander.
	if body, ok := lookupTrigger("ADDR-"); !ok || body != saved.Body {
		t.Errorf("lookupTrigger(ADDR-) = %q,%v", body, ok)
	}

	// Saving the same meaning again updates instead of duplicating.
	again, err := svc.SaveSnippet(Snippet{Kind: SnippetText, Title: "办公地址", Trigger: "addr-", Body: saved.Body}, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != saved.ID {
		t.Errorf("duplicate snippet created: %s vs %s", again.ID, saved.ID)
	}
	if got := len(svc.GetSnippets("", "")); got != 1 {
		t.Errorf("snippets = %d after re-save, want 1", got)
	}

	if !svc.DeleteSnippet(saved.ID) {
		t.Fatal("delete failed")
	}
	if got := len(svc.GetSnippets("", "")); got != 0 {
		t.Errorf("snippets = %d after delete", got)
	}
	if _, ok := lookupTrigger("addr-"); ok {
		t.Error("trigger should be gone after delete")
	}
}

func TestSnippetImageAndActions(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	b64 := base64.StdEncoding.EncodeToString(tinyPNG(t))

	sp, err := svc.SaveSnippet(Snippet{
		Kind:  SnippetImage,
		Title: "logo",
		Group: "素材",
		Body:  "logo.png",
	}, b64)
	if err != nil {
		t.Fatalf("SaveSnippet(image): %v", err)
	}
	if sp.ImageFile == "" {
		t.Fatal("image snippet should carry a blob name")
	}
	dataURL, err := svc.GetSnippetImage(sp.ID)
	if err != nil || dataURL == "" {
		t.Fatalf("GetSnippetImage: %v", err)
	}
	if SnippetAction(sp) != "复制图片" {
		t.Errorf("action = %q", SnippetAction(sp))
	}

	cases := map[SnippetKind]string{
		SnippetURL:     "打开网址",
		SnippetApp:     "启动程序",
		SnippetCommand: "执行命令",
		SnippetPassword: "复制密码",
		SnippetText:    "复制文本",
	}
	for kind, want := range cases {
		if got := SnippetAction(Snippet{Kind: kind, Body: "x"}); got != want {
			t.Errorf("SnippetAction(%s) = %q, want %q", kind, got, want)
		}
	}
}

func TestSnippetSyncRoundTrip(t *testing.T) {
	base, _, _ := newTestDAV(t)

	a := syncedService(t, base)
	if _, err := a.SaveSnippet(Snippet{Kind: SnippetText, Title: "模板", Trigger: "tpl-", Group: "写作", Body: "## 周报\n本周进展…"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSnippet(Snippet{Kind: SnippetURL, Title: "后台", Trigger: "dev-", Body: "https://dev.example.com"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSnippet(Snippet{Kind: SnippetImage, Title: "logo", Body: "logo"}, base64.StdEncoding.EncodeToString(tinyPNG(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	b := syncedService(t, base)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	got := b.GetSnippets("", "")
	if len(got) != 3 {
		t.Fatalf("B snippets = %d, want 3", len(got))
	}
	var imageID string
	for _, sp := range got {
		if sp.Kind == SnippetImage {
			imageID = sp.ID
		}
		if sp.Kind == SnippetText && sp.Trigger != "tpl-" {
			t.Errorf("trigger lost in transit: %+v", sp)
		}
		if sp.Kind == SnippetText && sp.Group != "写作" {
			t.Errorf("group lost in transit: %+v", sp)
		}
	}
	if imageID == "" {
		t.Fatal("image snippet did not arrive")
	}
	if payload, err := b.store.SnippetBlob(imageID); err != nil || len(payload) == 0 {
		t.Errorf("image payload not restored: %v", err)
	}

	// B edits and A picks the newer version up.
	sp := got[0]
	sp.Body = sp.Body + "\n（B 补充）"
	if _, err := b.SaveSnippet(sp, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B resync: %v", err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A resync: %v", err)
	}
	found := false
	for _, cand := range a.GetSnippets("", "") {
		if cand.ID != "" && cand.Hash == sp.Hash {
			found = true
		}
	}
	if !found {
		t.Error("edited snippet did not propagate back to A")
	}

	// Deletion travels too.
	var textID string
	for _, cand := range a.GetSnippets("", "") {
		if cand.Kind == SnippetText {
			textID = cand.ID
		}
	}
	if !a.DeleteSnippet(textID) {
		t.Fatal("delete failed")
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B delete sync: %v", err)
	}
	for _, cand := range b.GetSnippets("", "") {
		if cand.ID == textID {
			t.Error("deleted snippet survived on B")
		}
	}
}
