package clip

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"
	"io"
	"strings"
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

// fakeInstaller 是假发布里的安装包字节。
const fakeInstaller = "FAKEINSTALL"

// newFakeGitHub 起一个假的 GitHub：一个发布索引 + 一个安装包 + 它的 .sig。
//
// 强制验签之后，"发布忘了传 .sig"就等于"没有更新"，所以假服务必须连签名一起发；
// 这里现生成一对测试密钥并注入 updatePubKey（不是写死固定签名，免得和内置公钥纠缠）。
func newFakeGitHub(t *testing.T, version string) *httptest.Server {
	t.Helper()
	return newFakeGitHubWith(t, version, fakeInstaller, true)
}

// newFakeGitHubWith 允许换掉实际下发的字节（servedBody）或干脆不发签名（withSig），
// 用来覆盖"安装包被替换"和"发布漏签名"两条失败路径。
func newFakeGitHubWith(t *testing.T, version, servedBody string, withSig bool) *httptest.Server {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	oldPub := updatePubKey
	updatePubKey = pub
	t.Cleanup(func() { updatePubKey = oldPub })

	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(fakeInstaller)))

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + updateRepo + "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			sigAsset := ""
			if withSig {
				sigAsset = fmt.Sprintf(`,{"name":"clipnest-%s-amd64-setup.exe.sig","size":%d,"browser_download_url":"%s/dl/sig"}`,
					version, len(sig), srv.URL)
			}
			fmt.Fprintf(w, `{
			  "tag_name": "v%s",
			  "name": "ClipNest %s",
			  "body": "测试发布说明",
			  "published_at": "2026-11-17T07:00:00Z",
			  "assets": [
			    {"name":"clipnest-%s-amd64-setup-per-user.exe","size":11,"browser_download_url":"%s/dl/peruser.exe"},
			    {"name":"clipnest-%s-amd64-setup.exe","size":%d,"browser_download_url":"%s/dl/setup.exe"}%s
			  ]
			}`, version, version, version, srv.URL, version, len(servedBody), srv.URL, sigAsset)
		case "/dl/setup.exe":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(servedBody))
		case "/dl/sig":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(sig))
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

// 这次真实发布的形状（2026-11-19 从 /releases/latest 抓的）：tag v2.7.0，
// 唯一资产 clipnest-2.7.0-amd64-setup.exe。发版时最容易错的两处就是 tag 前缀和
// 资产名——一旦错了，update check 会静默地说"没有可用的安装包"，所以固化下来。
func TestPublishedReleaseIsConsumable(t *testing.T) {
	const payload = `{
	  "tag_name": "v2.7.0",
	  "name": "ClipNest 2.7.0",
	  "draft": false,
	  "prerelease": false,
	  "assets": [{
	    "name": "clipnest-2.7.0-amd64-setup.exe",
	    "size": 25382289,
	    "browser_download_url": "https://github.com/bbqisbbq/clipnest/releases/download/v2.7.0/clipnest-2.7.0-amd64-setup.exe",
	    "content_type": "application/x-msdownload"
	  }]
	}`
	var rel ghRelease
	if err := json.Unmarshal([]byte(payload), &rel); err != nil {
		t.Fatalf("unmarshal release: %v", err)
	}
	ver := strings.TrimPrefix(rel.TagName, "v")
	if ver != "2.7.0" {
		t.Fatalf("version = %q, want 2.7.0", ver)
	}
	if !versionLess("2.6.3", ver) {
		t.Fatal("从 2.6.3 看 2.7.0 应当算新版本")
	}
	asset := pickInstallerAsset(rel.Assets)
	if asset == nil || asset.Name != "clipnest-2.7.0-amd64-setup.exe" {
		t.Fatalf("picked %+v, want the setup exe", asset)
	}
	if !strings.Contains(asset.DownloadURL, "/download/v2.7.0/") {
		t.Fatalf("download url = %q", asset.DownloadURL)
	}
}

// 发布漏传 .sig：宁可停在旧版本，也不装"来源无法确认"的可执行文件。
func TestCheckUpdateRejectsUnsignedRelease(t *testing.T) {
	newFakeGitHubWith(t, "9.9.9", fakeInstaller, false)

	svc := newTestService(t, t.TempDir())
	info := svc.CheckUpdate()
	if info.Available {
		t.Fatalf("签名缺失时不应提供更新: %+v", info)
	}
	if !contains(info.Error, "签名") {
		t.Fatalf("错误信息要说清是签名问题，实际 %q", info.Error)
	}
}

// 安装包被替换（签名对不上）：下载阶段就必须拦下，并且把临时文件删掉。
func TestDownloadUpdateRejectsTamperedInstaller(t *testing.T) {
	newFakeGitHubWith(t, "9.9.9", "TAMPERED", true)

	svc := newTestService(t, t.TempDir())
	path, err := svc.DownloadUpdate()
	if err == nil {
		t.Fatalf("签名不符必须报错，实际返回 %q", path)
	}
	if !contains(err.Error(), "签名") {
		t.Fatalf("err = %v, want a signature error", err)
	}
}

// 纯函数三连：合法签名通过 / 内容被改不通过 / 签名长度异常不通过。
func TestVerifySignatureText(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	old := updatePubKey
	updatePubKey = pub
	t.Cleanup(func() { updatePubKey = old })

	file := filepath.Join(t.TempDir(), "setup.exe")
	if err := os.WriteFile(file, []byte(fakeInstaller), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(fakeInstaller)))
	if err := verifySignatureText(file, good); err != nil {
		t.Fatalf("合法签名应通过: %v", err)
	}
	other := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("other bytes")))
	if err := verifySignatureText(file, other); err == nil {
		t.Fatal("内容不符的签名不该通过")
	}
	if err := verifySignatureText(file, "AAAA"); err == nil {
		t.Fatal("长度异常的签名不该通过")
	}
}

// 真实发布的签名链路体检（默认跳过，要联网）：
//
//	$env:CLIPNEST_LIVE_RELEASE_TEST="1"; go test ./internal/clip -run TestLiveReleaseSignature -v
//
// 验的是最容易出岔子的那一环：**线上那份 .sig 能不能被程序里内置的公钥验通**。
// 单元测试只能证明"逻辑对"，证明不了"密钥配对没错、资产传对了"。
func TestLiveReleaseSignature(t *testing.T) {
	if os.Getenv("CLIPNEST_LIVE_RELEASE_TEST") != "1" {
		t.Skip("设 CLIPNEST_LIVE_RELEASE_TEST=1 才跑（需要联网访问 GitHub）")
	}
	// 两条路：给了直链就走直链（API 有每 IP 60 次/小时的限流，发版当天很容易撞上），
	// 否则走正常的发布索引。
	assetURL := os.Getenv("CLIPNEST_LIVE_ASSET_URL")
	sigURL := os.Getenv("CLIPNEST_LIVE_SIG_URL")
	assetSize := int64(0)
	sigName := "sig"
	if assetURL != "" && sigURL != "" {
		t.Logf("用直链体检: %s", assetURL)
	} else {
		rel, err := fetchLatestRelease()
		if err != nil {
			t.Fatalf("fetch latest release: %v", err)
		}
		asset := pickInstallerAsset(rel.Assets)
		if asset == nil {
			t.Fatalf("%s 没有可用的安装包", rel.TagName)
		}
		sig := resolveSigAsset(rel.Assets, asset.Name)
		if sig == nil {
			t.Fatalf("%s 的发布缺少 %s.sig —— 这一版没人能更新上来", rel.TagName, asset.Name)
		}
		t.Logf("latest=%s installer=%s (%d bytes) sig=%s", rel.TagName, asset.Name, asset.Size, sig.Name)
		assetURL, sigURL, assetSize, sigName = asset.DownloadURL, sig.DownloadURL, asset.Size, sig.Name
	}

	path := filepath.Join(t.TempDir(), "live-setup.exe")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(assetURL)
	if err != nil {
		f.Close()
		t.Fatalf("download: %v", err)
	}
	n, copyErr := io.Copy(f, resp.Body)
	resp.Body.Close()
	if cerr := f.Close(); copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil {
		t.Fatalf("download body: %v", copyErr)
	}
	if assetSize > 0 && n != assetSize {
		t.Fatalf("size %d, want %d", n, assetSize)
	}
	if err := verifyUpdateSignature(path, &ghAsset{Name: sigName, DownloadURL: sigURL}); err != nil {
		t.Fatalf("线上签名验不过（内置公钥与私钥不配对，或资产传错了）: %v", err)
	}
	t.Logf("签名校验通过：%d 字节安装包 + 内置公钥", n)
}
