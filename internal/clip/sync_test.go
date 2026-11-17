package clip

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/net/webdav"
)

// newTestDAV starts an in-process WebDAV server rooted at a temp dir and returns
// its URL plus a cleanup function.
func newTestDAV(t *testing.T) (string, *webdav.Dir) {
	t.Helper()
	dir := webdav.Dir(t.TempDir())
	h := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: dir,
		LockSystem: webdav.NewMemLS(),
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL + "/dav", &dir
}

func newTestService(t *testing.T, dataDir string) *ClipService {
	t.Helper()
	svc, err := NewClipService(dataDir, Hooks{})
	if err != nil {
		t.Fatalf("NewClipService: %v", err)
	}
	t.Cleanup(svc.Shutdown)
	return svc
}

func TestSyncUploadsAndPulls(t *testing.T) {
	base, dir := newTestDAV(t)

	// Machine A: one text entry, one image entry.
	a := newTestService(t, t.TempDir())
	if _, _, err := a.store.Add(Captured{Kind: KindText, Text: "hello from A", Preview: "hello from A", Hash: "hash-a", Size: 12, SourceApp: "a.exe"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.Add(Captured{Kind: KindImage, PNG: tinyPNG(t), Width: 2, Height: 2, Preview: "2 × 2 图像", Hash: "hash-img", Size: 100}); err != nil {
		t.Fatal(err)
	}
	st := a.store.Settings()
	st.SyncEnabled = true
	st.SyncURL = base
	if err := a.store.SaveSettings(st); err != nil {
		t.Fatal(err)
	}

	status, err := a.SyncNow()
	if err != nil {
		t.Fatalf("SyncNow upload: %v", err)
	}
	if status.LastError != "" {
		t.Fatalf("unexpected error status: %+v", status)
	}

	// The document must exist on the server.
	data, err := os.ReadFile(filepath.Join(string(*dir), DefaultSyncFile))
	if err != nil {
		t.Fatalf("remote file missing: %v", err)
	}
	var man syncManifest
	if err := json.Unmarshal(data, &man); err != nil {
		t.Fatalf("remote manifest unreadable: %v", err)
	}
	if len(man.Items) != 2 {
		t.Fatalf("remote items = %d, want 2", len(man.Items))
	}
	var withBlob int
	for _, it := range man.Items {
		if it.Blob != "" {
			withBlob++
			// Records and blobs live as separate files, one per content hash.
			if _, err := os.Stat(filepath.Join(string(*dir), "items", it.Hash+".json")); err != nil {
				t.Errorf("record file missing for %s: %v", it.Hash, err)
			}
			if _, err := os.Stat(filepath.Join(string(*dir), "blobs", it.Hash+it.Blob)); err != nil {
				t.Errorf("blob file missing for %s: %v", it.Hash, err)
			}
		}
	}
	if withBlob != 1 {
		t.Errorf("remote blobs = %d, want 1 (the image)", withBlob)
	}
	if man.Settings == nil {
		t.Error("settings should be published in the manifest")
	}

	// Machine B: empty store, pulls everything including the image bytes.
	b := newTestService(t, t.TempDir())
	stB := b.store.Settings()
	stB.SyncEnabled = true
	stB.SyncURL = base
	if err := b.store.SaveSettings(stB); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("SyncNow pull: %v", err)
	}
	items := b.store.All()
	if len(items) != 2 {
		t.Fatalf("machine B items = %d, want 2", len(items))
	}
	var image *Item
	for i := range items {
		if items[i].Kind == KindImage {
			image = &items[i]
		}
	}
	if image == nil {
		t.Fatal("image entry did not arrive")
	}
	if payload, err := b.store.LoadBlobByName(image.ImageFile); err != nil || len(payload) == 0 {
		t.Errorf("image blob not restored: %v", err)
	}
}

func TestSyncMergesByHashAndUnionsFlags(t *testing.T) {
	base, _ := newTestDAV(t)

	a := newTestService(t, t.TempDir())
	aItem, _, _ := a.store.Add(Captured{Kind: KindText, Text: "shared", Preview: "shared", Hash: "same-hash", Size: 6})
	a.store.SetPinned(aItem.ID, true)
	if _, _, err := a.store.Add(Captured{Kind: KindText, Text: "only-a", Preview: "only-a", Hash: "only-a", Size: 6}); err != nil {
		t.Fatal(err)
	}
	stA := a.store.Settings()
	stA.SyncEnabled, stA.SyncURL = true, base
	_ = a.store.SaveSettings(stA)
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	// Machine B independently captured the same content (same hash) but never
	// starred it, and has an entry of its own.
	b := newTestService(t, t.TempDir())
	if _, _, err := b.store.Add(Captured{Kind: KindText, Text: "shared", Preview: "shared", Hash: "same-hash", Size: 6}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.store.Add(Captured{Kind: KindText, Text: "only-b", Preview: "only-b", Hash: "only-b", Size: 6}); err != nil {
		t.Fatal(err)
	}
	stB := b.store.Settings()
	stB.SyncEnabled, stB.SyncURL = true, base
	_ = b.store.SaveSettings(stB)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}

	items := b.store.All()
	if len(items) != 3 {
		t.Fatalf("merged items = %d, want 3 (shared + only-a + only-b)", len(items))
	}
	var shared *Item
	for i := range items {
		if items[i].Hash == "same-hash" {
			shared = &items[i]
		}
	}
	if shared == nil {
		t.Fatal("shared entry missing")
	}
	if !shared.Pinned {
		t.Error("pinned flag was not unioned across machines")
	}

	// A pulls B's entry on the next round.
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A second sync: %v", err)
	}
	if len(a.store.All()) != 3 {
		t.Fatalf("A items = %d, want 3", len(a.store.All()))
	}
}

func TestSyncErrorsAreReported(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	st := svc.store.Settings()
	st.SyncEnabled, st.SyncURL = true, "not-a-url"
	_ = svc.store.SaveSettings(st)

	if _, err := svc.SyncNow(); err == nil {
		t.Fatal("expected an error for an invalid URL")
	}
	if svc.GetSyncStatus().LastError == "" {
		t.Error("status should carry the last error")
	}
}

func TestTestSyncReportsEmptyRemote(t *testing.T) {
	base, _ := newTestDAV(t)
	svc := newTestService(t, t.TempDir())
	st := svc.store.Settings()
	st.SyncURL = base
	_ = svc.store.SaveSettings(st)

	msg, err := svc.TestSync()
	if err != nil {
		t.Fatalf("TestSync: %v", err)
	}
	if msg == "" {
		t.Fatal("expected a human readable message")
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{B: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
