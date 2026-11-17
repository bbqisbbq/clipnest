package clip

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.10.0", "0.9.9", false},
		{"1.0", "1.0.1", true},
		{"v0.2.0", "0.2.1", true},
		{"0.2.0-beta", "0.2.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestPickInstallerAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "clipnest-0.2.0-amd64-setup-per-user.exe"},
		{Name: "notes.txt"},
		{Name: "clipnest-0.2.0-amd64-setup.exe"},
	}
	got := pickInstallerAsset(assets)
	if got == nil || got.Name != "clipnest-0.2.0-amd64-setup.exe" {
		t.Fatalf("picked %+v, want the machine-wide setup", got)
	}

	// Only a per-user build published: still better than nothing.
	got = pickInstallerAsset([]ghAsset{{Name: "clipnest-0.2.0-amd64-setup-per-user.exe"}})
	if got == nil {
		t.Fatal("should fall back to the per-user installer")
	}
	if pickInstallerAsset([]ghAsset{{Name: "readme.md"}}) != nil {
		t.Fatal("must not pick a non-exe asset")
	}
}

// newFakeGitHub serves a release index and one fake installer.
func newFakeGitHub(t *testing.T, version string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + updateRepo + "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
			  "tag_name": "v%s",
			  "name": "ClipNest %s",
			  "body": "测试发布说明",
			  "published_at": "2026-11-17T07:00:00Z",
			  "assets": [
			    {"name":"clipnest-%s-amd64-setup-per-user.exe","size":11,"browser_download_url":"%s/dl/peruser.exe"},
			    {"name":"clipnest-%s-amd64-setup.exe","size":11,"browser_download_url":"%s/dl/setup.exe"}
			  ]
			}`, version, version, version, srv.URL, version, srv.URL)
		case "/dl/setup.exe":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("FAKEINSTALL"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CLIPNEST_UPDATE_API", srv.URL)
	return srv
}

func TestCheckUpdateFindsNewerRelease(t *testing.T) {
	newFakeGitHub(t, "9.9.9")

	svc := newTestService(t, t.TempDir())
	info := svc.CheckUpdate()

	if info.Error != "" {
		t.Fatalf("unexpected error: %s", info.Error)
	}
	if !info.Available {
		t.Fatalf("expected an update, got %+v", info)
	}
	if info.Version != "9.9.9" || info.Current != Version {
		t.Errorf("versions = %s / %s", info.Version, info.Current)
	}
	if info.AssetName != "clipnest-9.9.9-amd64-setup.exe" {
		t.Errorf("asset = %q, want the machine-wide setup", info.AssetName)
	}
	if info.AssetSize != 11 {
		t.Errorf("asset size = %d, want 11", info.AssetSize)
	}
}

func TestCheckUpdateUpToDate(t *testing.T) {
	// Publish exactly the running version: no update should be offered.
	newFakeGitHub(t, Version)

	svc := newTestService(t, t.TempDir())
	info := svc.CheckUpdate()
	if info.Error != "" {
		t.Fatalf("unexpected error: %s", info.Error)
	}
	if info.Available {
		t.Fatalf("must not offer a downgrade/same version: %+v", info)
	}
}

func TestCheckUpdateReportsRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for 1.2.3.4."}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CLIPNEST_UPDATE_API", srv.URL)

	svc := newTestService(t, t.TempDir())
	info := svc.CheckUpdate()
	if info.Error == "" || !contains(info.Error, "限流") {
		t.Fatalf("expected a friendly rate-limit error, got %q", info.Error)
	}
	if info.Available {
		t.Fatal("a failed check must not offer an update")
	}
}

func TestDownloadUpdateWritesInstaller(t *testing.T) {
	newFakeGitHub(t, "9.9.9")

	svc := newTestService(t, t.TempDir())
	path, err := svc.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "FAKEINSTALL" {
		t.Errorf("downloaded %q, want the served bytes", data)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
