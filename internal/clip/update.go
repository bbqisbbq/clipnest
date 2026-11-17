package clip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Self-update: ask GitHub for the latest release, download the NSIS installer
// asset and hand over to it.
//
// GitHub's API is the source of truth (the repo is public, so no token is
// needed). Everything is plain net/http so it can be pointed at a test server.

const (
	// updateRepo is "owner/repo" on GitHub.
	updateRepo = "bbqisbbq/clipnest"
	// updateAPIBase is overridable in tests.
	updateAPIBase = "https://api.github.com"
	// updateHTTPTimeout bounds one request.
	updateHTTPTimeout = 30 * time.Second
)

// UpdateInfo is what the UI shows about the update check.
type UpdateInfo struct {
	Checked   bool   `json:"checked"`
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	AssetName string `json:"assetName"`
	AssetSize int64  `json:"assetSize"`
	AssetURL  string `json:"assetUrl"`
	Published string `json:"published"`
	Error     string `json:"error,omitempty"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
	ContentType string `json:"content_type"`
}

// CheckUpdate asks GitHub for the newest release and compares it with Version.
func (s *ClipService) CheckUpdate() UpdateInfo {
	info := UpdateInfo{Checked: true, Current: Version}

	rel, err := fetchLatestRelease()
	if err != nil {
		info.Error = err.Error()
		Logf(s.store.Dir(), "update check failed: %v", err)
		return info
	}

	info.Version = strings.TrimPrefix(rel.TagName, "v")
	info.Notes = strings.TrimSpace(rel.Body)
	if !rel.PublishedAt.IsZero() {
		info.Published = rel.PublishedAt.Local().Format("2006-01-02 15:04")
	}

	if !versionLess(Version, info.Version) {
		Logf(s.store.Dir(), "update check: up to date (%s)", Version)
		return info
	}

	asset := pickInstallerAsset(rel.Assets)
	if asset == nil {
		info.Error = fmt.Sprintf("新版本 %s 没有可用的安装包", info.Version)
		Logf(s.store.Dir(), "update check: %s has no installer asset", info.Version)
		return info
	}
	info.Available = true
	info.AssetName = asset.Name
	info.AssetSize = asset.Size
	info.AssetURL = asset.DownloadURL
	Logf(s.store.Dir(), "update available: %s -> %s (%s)", Version, info.Version, asset.Name)
	s.emit("update:available", info)
	return info
}

// fetchLatestRelease reads /releases/latest (public repo, no token).
func fetchLatestRelease() (*ghRelease, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateHTTPTimeout)
	defer cancel()

	url := strings.TrimRight(updateAPIBase, "/") + "/repos/" + updateRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ClipNest/"+Version)

	resp, err := (&http.Client{Timeout: updateHTTPTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errors.New("仓库还没有发布任何版本")
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("检查更新失败（HTTP %d）: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析发布信息失败: %w", err)
	}
	return &rel, nil
}

// pickInstallerAsset prefers the setup executable matching this machine.
func pickInstallerAsset(assets []ghAsset) *ghAsset {
	var fallback *ghAsset
	for i := range assets {
		a := &assets[i]
		name := strings.ToLower(a.Name)
		if !strings.HasSuffix(name, ".exe") {
			continue
		}
		// Never auto-install the "per-user" build over a machine-wide one.
		if strings.Contains(name, "per-user") || strings.Contains(name, "portable") {
			if fallback == nil {
				fallback = a
			}
			continue
		}
		if strings.Contains(name, "setup") || strings.Contains(name, "installer") {
			return a
		}
	}
	if fallback != nil {
		return fallback
	}
	for i := range assets {
		if strings.HasSuffix(strings.ToLower(assets[i].Name), ".exe") {
			return &assets[i]
		}
	}
	return nil
}

// DownloadUpdate fetches the installer into a temp directory and returns its path.
func (s *ClipService) DownloadUpdate() (string, error) {
	info := s.CheckUpdate()
	if info.Error != "" {
		return "", errors.New(info.Error)
	}
	if !info.Available {
		return "", errors.New("已经是最新版本")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)

	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败（HTTP %d）", resp.StatusCode)
	}

	dir := filepath.Join(os.TempDir(), "clipnest-update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, sanitizeFileName(info.AssetName))
	f, err := os.Create(target)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("写入安装包失败: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return "", closeErr
	}
	if info.AssetSize > 0 && n != info.AssetSize {
		_ = os.Remove(target)
		return "", fmt.Errorf("安装包大小不符（%d/%d 字节）", n, info.AssetSize)
	}
	Logf(s.store.Dir(), "update downloaded: %s (%d bytes)", target, n)
	return target, nil
}

// InstallUpdate runs a downloaded installer silently and quits so it can
// replace the binaries. The NSIS script keeps the clipboard history on /S.
func (s *ClipService) InstallUpdate() error {
	path, err := s.DownloadUpdate()
	if err != nil {
		return err
	}
	cmd := exec.Command(path, "/S")
	cmd.Dir = filepath.Dir(path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动安装程序失败: %w", err)
	}
	Logf(s.store.Dir(), "update: launched installer %s, quitting", path)
	if s.hooks.Quit != nil {
		s.hooks.Quit()
	}
	return nil
}

// OpenReleasePage opens the releases page in the default browser.
func (s *ClipService) OpenReleasePage() error {
	url := "https://github.com/" + updateRepo + "/releases/latest"
	if s.hooks.OpenURL != nil {
		return s.hooks.OpenURL(url)
	}
	return errors.New("当前平台不支持打开链接")
}

// sanitizeFileName keeps only the base name and strips characters Windows
// rejects in file names.
func sanitizeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name = replacer.Replace(name)
	if name == "" || name == "." || name == ".." {
		return "clipnest-update.exe"
	}
	return name
}

// versionLess reports whether a < b for dotted numeric versions ("0.2.0" <
// "0.10.1"). Missing segments count as zero; a build suffix is ignored.
func versionLess(a, b string) bool {
	as := versionParts(a)
	bs := versionParts(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av < bv
		}
	}
	return false
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n := 0
		for _, r := range f {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
}

// updateCheckLoop runs one check a few seconds after startup, when enabled.
func (s *ClipService) updateCheckLoop() {
	if !s.store.Settings().UpdateCheck {
		return
	}
	select {
	case <-time.After(8 * time.Second):
	}
	s.CheckUpdate()
}

// updatePlatform is exposed for the UI ("windows/amd64").
func updatePlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }
