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
	"time"
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

func TestSyncPropagatesDeletion(t *testing.T) {
	base, _ := newTestDAV(t)

	a := newTestService(t, t.TempDir())
	item, _, _ := a.store.Add(Captured{Kind: KindText, Text: "delete me", Preview: "delete me", Hash: "del-hash", Size: 9})
	if _, _, err := a.store.Add(Captured{Kind: KindText, Text: "keep me", Preview: "keep me", Hash: "keep-hash", Size: 7}); err != nil {
		t.Fatal(err)
	}
	stA := a.store.Settings()
	stA.SyncEnabled, stA.SyncURL = true, base
	_ = a.store.SaveSettings(stA)
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A upload: %v", err)
	}

	b := newTestService(t, t.TempDir())
	stB := b.store.Settings()
	stB.SyncEnabled, stB.SyncURL = true, base
	_ = b.store.SaveSettings(stB)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if len(b.store.All()) != 2 {
		t.Fatalf("B should have both entries, got %d", len(b.store.All()))
	}

	// A deletes one entry (user action → tombstone) and syncs.
	if !a.store.Delete(item.ID) {
		t.Fatal("delete failed")
	}
	if len(a.store.Tombstones(time.Now().Add(-time.Hour), 10)) != 1 {
		t.Fatal("expected exactly one tombstone")
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A resync: %v", err)
	}

	// B must drop its copy instead of re-uploading it.
	status, err := b.SyncNow()
	if err != nil {
		t.Fatalf("B resync: %v", err)
	}
	if got := len(b.store.All()); got != 1 {
		t.Fatalf("B items = %d after deletion sync, want 1", got)
	}
	if !contains(status.LastResult, "删除 1 条") {
		t.Errorf("status should mention the deletion: %q", status.LastResult)
	}
	if _, ok := b.store.findByHash("del-hash"); ok {
		t.Error("deleted entry came back on B")
	}

	// And A's next pull must not resurrect it either.
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A third sync: %v", err)
	}
	if got := len(a.store.All()); got != 1 {
		t.Fatalf("A items = %d after round trip, want 1", got)
	}
}

func TestSyncRecopyAfterDeleteResurrects(t *testing.T) {
	base, _ := newTestDAV(t)

	a := newTestService(t, t.TempDir())
	item, _, _ := a.store.Add(Captured{Kind: KindText, Text: "zombie", Preview: "zombie", Hash: "zombie-hash", Size: 6})
	stA := a.store.Settings()
	stA.SyncEnabled, stA.SyncURL = true, base
	_ = a.store.SaveSettings(stA)
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A upload: %v", err)
	}

	a.store.Delete(item.ID)
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}

	// Copying the same content again is a new intent: it must survive the older
	// tombstone instead of being deleted on the next sync.
	time.Sleep(5 * time.Millisecond)
	if _, _, err := a.store.Add(Captured{Kind: KindText, Text: "zombie", Preview: "zombie", Hash: "zombie-hash", Size: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A recopy sync: %v", err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A idle sync: %v", err)
	}
	if _, ok := a.store.findByHash("zombie-hash"); !ok {
		t.Fatal("re-copied entry was deleted by its own older tombstone")
	}
}