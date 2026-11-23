package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

// davStats counts the requests a sync actually makes, so tests can assert that
// a packaged sync does not fan out into hundreds of calls.
type davStats struct {
	gets     atomic.Int32
	puts     atomic.Int32
	propfind atomic.Int32
	mkcol    atomic.Int32
}

type countingHandler struct {
	next  http.Handler
	stats *davStats
}

func (c countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c.stats.gets.Add(1)
	case http.MethodPut:
		c.stats.puts.Add(1)
	case "PROPFIND":
		c.stats.propfind.Add(1)
	case "MKCOL":
		c.stats.mkcol.Add(1)
	}
	c.next.ServeHTTP(w, r)
}

// newTestDAV starts an in-process WebDAV server rooted at a temp dir.
func newTestDAV(t *testing.T) (string, *webdav.Dir, *davStats) {
	t.Helper()
	dir := webdav.Dir(t.TempDir())
	stats := &davStats{}
	h := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: dir,
		LockSystem: webdav.NewMemLS(),
	}
	srv := httptest.NewServer(countingHandler{next: h, stats: stats})
	t.Cleanup(srv.Close)
	return srv.URL + "/dav", &dir, stats
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

// syncedService returns a service wired to the given WebDAV base.
func syncedService(t *testing.T, base string) *ClipService {
	t.Helper()
	svc := newTestService(t, t.TempDir())
	st := svc.store.Settings()
	st.SyncEnabled = true
	st.SyncURL = base
	if err := svc.store.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestSyncPackagesEverythingIntoOneFile(t *testing.T) {
	base, dir, stats := newTestDAV(t)

	a := syncedService(t, base)
	if _, _, err := a.store.SaveCapture(Captured{Kind: KindText, Text: "hello from A", Preview: "hello from A", Hash: "hash-a", Size: 12, SourceApp: "a.exe"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.SaveCapture(Captured{Kind: KindImage, PNG: tinyPNG(t), Width: 2, Height: 2, Preview: "2 × 2 图像", Hash: "hash-img", Size: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	// Exactly one package file, written by A, and it is one PUT (+ MKCOL/PROPFIND).
	files, err := os.ReadDir(filepath.Join(string(*dir), syncDevicesDir))
	if err != nil {
		t.Fatalf("devices dir: %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".zip") {
		t.Fatalf("want a single .zip package, got %v", names(files))
	}
	if got := stats.puts.Load(); got != 1 {
		t.Errorf("PUT count = %d, want 1 (the whole history uploads in one request)", got)
	}

	pkg, err := os.ReadFile(filepath.Join(string(*dir), syncDevicesDir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parsePackage(pkg)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	if len(parsed.entries) != 2 {
		t.Errorf("package holds %d records, want 2", len(parsed.entries))
	}
	if len(parsed.blobs) != 1 {
		t.Errorf("package holds %d blobs, want 1 (the image)", len(parsed.blobs))
	}
	if parsed.index.Settings == nil {
		t.Error("package should carry the settings payload")
	}

	// A second machine pulls the package: one GET for the peer package.
	b := syncedService(t, base)
	before := stats.gets.Load()
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	if got := stats.gets.Load() - before; got != 1 {
		t.Errorf("B made %d GETs, want 1 (single peer package)", got)
	}

	items := b.store.ListEntries(EntryQuery{})
	if len(items) != 2 {
		t.Fatalf("B entries = %d, want 2", len(items))
	}
	var imageItem *Entry
	for i := range items {
		if items[i].Kind == KindImage {
			imageItem = &items[i]
		}
	}
	if imageItem == nil {
		t.Fatal("image entry did not arrive")
	}
	if payload, _, err := b.store.LoadEntryBlob(memoImageToken(imageItem.Content)); err != nil || len(payload) == 0 {
		t.Errorf("image blob not restored: %v", err)
	}
}

func TestSyncTwoDevicesConvergeWithoutOverwriting(t *testing.T) {
	base, dir, _ := newTestDAV(t)

	a := syncedService(t, base)
	b := syncedService(t, base)

	if _, _, err := a.store.SaveCapture(Captured{Kind: KindText, Text: "only-a", Preview: "only-a", Hash: "only-a", Size: 6}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.store.SaveCapture(Captured{Kind: KindText, Text: "only-b", Preview: "only-b", Hash: "only-b", Size: 6}); err != nil {
		t.Fatal(err)
	}

	// Simulate a true race: both devices publish before either sees the other.
	// (Calling SyncNow twice in a row would let B notice A and hide the point.)
	racePush(t, a)
	racePush(t, b)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}

	files, err := os.ReadDir(filepath.Join(string(*dir), syncDevicesDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want one package per device, got %v", names(files))
	}

	// Each package must still contain its own item (nobody clobbered anyone).
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(string(*dir), syncDevicesDir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := parsePackage(raw)
		if err != nil {
			t.Fatalf("%s: %v", f.Name(), err)
		}
		// Whatever a peer already published, our own item must never be missing.
		hashes := make(map[string]bool, len(pkg.entries))
		for _, it := range pkg.entries {
			hashes[it.Hash] = true
		}
		if !hashes["only-a"] && !hashes["only-b"] {
			t.Errorf("%s holds none of the local items: %v", f.Name(), hashes)
		}
	}

	// Next round: both see each other and converge to the union.
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A resync: %v", err)
	}
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B resync: %v", err)
	}
	for name, svc := range map[string]*ClipService{"A": a, "B": b} {
		if got := svc.store.CountEntries(); got != 2 {
			t.Errorf("%s entries = %d, want 2 (union)", name, got)
		}
		for _, hash := range []string{"only-a", "only-b"} {
			if _, ok := svc.store.findEntryByHash(hash); !ok {
				t.Errorf("%s lost %s", name, hash)
			}
		}
	}

	// And both packages now describe the whole history.
	for _, f := range files {
		raw, _ := os.ReadFile(filepath.Join(string(*dir), syncDevicesDir, f.Name()))
		pkg, _ := parsePackage(raw)
		if len(pkg.entries) != 2 {
			t.Errorf("%s holds %d records after convergence, want 2", f.Name(), len(pkg.entries))
		}
	}
}

func TestSyncMergesFlagsAndRepublishesOnce(t *testing.T) {
	base, _, stats := newTestDAV(t)

	a := syncedService(t, base)
	shared, _, _ := a.store.SaveCapture(Captured{Kind: KindText, Text: "shared", Preview: "shared", Hash: "same-hash", Size: 6})
	shared.Pinned = true
	_, _ = a.store.SaveEntry(*shared)
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	b := syncedService(t, base)
	if _, _, err := b.store.SaveCapture(Captured{Kind: KindText, Text: "shared", Preview: "shared", Hash: "same-hash", Size: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	got, ok := b.store.findEntryByHash("same-hash")
	if !ok {
		t.Fatal("shared entry missing on B")
	}
	if !got.Pinned {
		t.Error("pinned flag was not unioned across devices")
	}

	// A machine that has nothing new must not re-upload its package.
	puts := stats.puts.Load()
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B idle sync: %v", err)
	}
	if got := stats.puts.Load(); got != puts {
		t.Errorf("idle sync uploaded again (%d -> %d); the fingerprint check should skip it", puts, got)
	}
}

func TestSyncPropagatesDeletion(t *testing.T) {
	base, _, _ := newTestDAV(t)

	a := syncedService(t, base)
	item, _, _ := a.store.SaveCapture(Captured{Kind: KindText, Text: "delete me", Preview: "delete me", Hash: "del-hash", Size: 9})
	if _, _, err := a.store.SaveCapture(Captured{Kind: KindText, Text: "keep me", Preview: "keep me", Hash: "keep-hash", Size: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A upload: %v", err)
	}

	b := syncedService(t, base)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if got := b.store.CountEntries(); got != 2 {
		t.Fatalf("B should have both entries, got %d", got)
	}

	if !a.store.DeleteEntry(item.ID) {
		t.Fatal("delete failed")
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A resync: %v", err)
	}

	status, err := b.SyncNow()
	if err != nil {
		t.Fatalf("B resync: %v", err)
	}
	if got := b.store.CountEntries(); got != 1 {
		t.Fatalf("B items = %d after deletion sync, want 1", got)
	}
	if !strings.Contains(status.LastResult, "删除 1 条") {
		t.Errorf("status should mention the deletion: %q", status.LastResult)
	}
	if _, ok := b.store.findEntryByHash("del-hash"); ok {
		t.Error("deleted entry came back on B")
	}

	// A's next pull must not resurrect it from B's (older) package either.
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A third sync: %v", err)
	}
	if got := a.store.CountEntries(); got != 1 {
		t.Fatalf("A items = %d after round trip, want 1", got)
	}
}

func TestSyncRecopyAfterDeleteResurrects(t *testing.T) {
	base, _, _ := newTestDAV(t)

	a := syncedService(t, base)
	item, _, _ := a.store.SaveCapture(Captured{Kind: KindText, Text: "zombie", Preview: "zombie", Hash: "zombie-hash", Size: 6})
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A upload: %v", err)
	}
	a.store.DeleteEntry(item.ID)
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A delete sync: %v", err)
	}

	time.Sleep(5 * time.Millisecond)
	if _, _, err := a.store.SaveCapture(Captured{Kind: KindText, Text: "zombie", Preview: "zombie", Hash: "zombie-hash", Size: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A recopy sync: %v", err)
	}
	if _, ok := a.store.findEntryByHash("zombie-hash"); !ok {
		t.Fatal("re-copied entry was deleted by its own older tombstone")
	}
}

func TestSyncSettingsTravelWithTheNewestStamp(t *testing.T) {
	base, _, _ := newTestDAV(t)

	a := syncedService(t, base)
	stA := a.store.Settings()
	stA.SyncSettings = true
	stA.MaxTextLength = 321
	_, _ = a.SaveSettings(stA)
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	b := syncedService(t, base)
	stB := b.store.Settings()
	stB.SyncSettings = true
	stB.SyncURL = base
	b.store.SaveSettings(stB)
	if _, err := b.SyncNow(); err != nil {
		t.Fatalf("B sync: %v", err)
	}

	gotB := b.store.Settings()
	if gotB.MaxTextLength != 321 {
		t.Errorf("B MaxTextLength = %d, want 321 from A", gotB.MaxTextLength)
	}

	if gotB.SyncURL != base {
		t.Errorf("B SyncURL = %q, machine-only fields must not be overwritten", gotB.SyncURL)
	}
}

func TestTestSyncReportsDevices(t *testing.T) {
	base, _, _ := newTestDAV(t)
	a := syncedService(t, base)
	if _, _, err := a.store.SaveCapture(Captured{Kind: KindText, Text: "x", Preview: "x", Hash: "h", Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	b := syncedService(t, base)
	msg, err := b.TestSync()
	if err != nil {
		t.Fatalf("TestSync: %v", err)
	}
	if !strings.Contains(msg, "同步包") {
		t.Errorf("message should mention the packages: %q", msg)
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

// racePush publishes a device's package without running the pull phase, which is
// exactly what two machines syncing at the same moment look like from the server.
func racePush(t *testing.T, svc *ClipService) {
	t.Helper()
	cfg, err := svc.syncConfig()
	if err != nil {
		t.Fatalf("syncConfig: %v", err)
	}
	if err := cfg.mkcol(syncDevicesDir); err != nil {
		t.Fatalf("mkcol: %v", err)
	}
	payload, _, _, err := svc.buildOwnPackage(nil, nil, svc.store.Settings())
	if err != nil {
		t.Fatalf("buildOwnPackage: %v", err)
	}
	if err := cfg.davPut(syncDevicesDir+"/"+cfg.packageName(), "application/zip", payload); err != nil {
		t.Fatalf("davPut: %v", err)
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
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
