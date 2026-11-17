package clip

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// WebDAV sync: the whole history is published as one JSON document on a WebDAV
// server and merged by content hash. Blobs (images, unknown formats) travel
// inline as base64 up to a budget, so a fresh machine can restore previews too.

const (
	// DefaultSyncFile is the document name inside the configured directory.
	DefaultSyncFile = "clipnest-sync.json"
	// syncBlobBudget caps inline blob bytes per sync to keep requests sane.
	syncBlobBudget = 6 << 20
	// syncHTTPTimeout bounds a single WebDAV request.
	syncHTTPTimeout = 45 * time.Second
)

// SyncStatus is what the settings page shows.
type SyncStatus struct {
	Enabled     bool   `json:"enabled"`
	InProgress  bool   `json:"inProgress"`
	LastSync    string `json:"lastSync"`
	LastResult  string `json:"lastResult"`
	LastError   string `json:"lastError"`
	RemoteItems int    `json:"remoteItems"`
	Truncated   bool   `json:"truncated"`
}

type syncItem struct {
	Item
	BlobB64 string `json:"blobB64,omitempty"`
}

type syncSnapshot struct {
	Version   int        `json:"version"`
	UpdatedAt time.Time  `json:"updatedAt"`
	Device    string     `json:"device"`
	Items     []syncItem `json:"items"`
	Truncated bool       `json:"truncated,omitempty"`
}

// ---- transport ----------------------------------------------------------

type syncConfig struct {
	base     string
	user     string
	pass     string
	fileName string
}

func (s *ClipService) syncConfig() (syncConfig, error) {
	st := s.store.Settings()
	if strings.TrimSpace(st.SyncURL) == "" {
		return syncConfig{}, errors.New("还没有填写 WebDAV 地址")
	}
	name := strings.TrimSpace(st.SyncFile)
	if name == "" {
		name = DefaultSyncFile
	}
	return syncConfig{
		base:     st.SyncURL,
		user:     st.SyncUser,
		pass:     st.SyncPass,
		fileName: name,
	}, nil
}

func (c syncConfig) endpoint() (string, error) {
	u, err := url.Parse(strings.TrimSpace(c.base))
	if err != nil {
		return "", fmt.Errorf("WebDAV 地址无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("WebDAV 地址必须以 http:// 或 https:// 开头")
	}
	u.Path = path.Join(u.Path, c.fileName)
	return u.String(), nil
}

func (c syncConfig) newRequest(ctx context.Context, method string) (*http.Request, error) {
	target, err := c.endpoint()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)
	return req, nil
}

func newSyncHTTPClient() *http.Client {
	return &http.Client{Timeout: syncHTTPTimeout}
}

// fetchRemote returns the remote snapshot, or (nil, nil) when it does not exist.
func fetchRemote(cfg syncConfig) (*syncSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
	defer cancel()

	req, err := cfg.newRequest(ctx, http.MethodGet)
	if err != nil {
		return nil, err
	}
	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("认证失败（HTTP %d），请检查用户名/密码", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("读取失败（HTTP %d）: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	var snap syncSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("远端文件解析失败: %w", err)
	}
	return &snap, nil
}

// putRemote uploads the snapshot, creating the parent collection when needed.
func putRemote(cfg syncConfig, snap *syncSnapshot) error {
	payload, err := json.Marshal(snap)
	if err != nil {
		return err
	}

	upload := func() (*http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
		defer cancel()
		req, err := cfg.newRequest(ctx, http.MethodPut)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Body = io.NopCloser(bytes.NewReader(payload))
		req.ContentLength = int64(len(payload))
		return newSyncHTTPClient().Do(req)
	}

	resp, err := upload()
	if err != nil {
		return fmt.Errorf("上传失败: %w", err)
	}
	// Some servers refuse a PUT whose parent collection is missing.
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		if err := mkcolParent(cfg); err != nil {
			return err
		}
		if resp, err = upload(); err != nil {
			return fmt.Errorf("上传失败: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("上传失败（HTTP %d）: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func mkcolParent(cfg syncConfig) error {
	target, err := cfg.endpoint()
	if err != nil {
		return err
	}
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	parent := path.Dir(u.Path)
	u.Path = parent
	if parent == "/" || parent == "." {
		return errors.New("WebDAV 目录不存在，且无法创建根目录")
	}

	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "MKCOL", u.String(), nil)
	if err != nil {
		return err
	}
	if cfg.user != "" || cfg.pass != "" {
		req.SetBasicAuth(cfg.user, cfg.pass)
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)
	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == http.StatusMethodNotAllowed {
		// 405 means it already exists on most servers.
		return nil
	}
	return fmt.Errorf("创建目录失败（HTTP %d）", resp.StatusCode)
}

// ---- snapshot building --------------------------------------------------

func (s *ClipService) buildSnapshot(includeBlobs bool) (*syncSnapshot, error) {
	items := s.store.All()
	snap := &syncSnapshot{
		Version:   1,
		UpdatedAt: time.Now().UTC(),
		Device:    s.deviceName(),
		Items:     make([]syncItem, 0, len(items)),
	}
	budget := syncBlobBudget
	for _, it := range items {
		si := syncItem{Item: it}
		if includeBlobs {
			name := it.ImageFile
			if name == "" {
				name = it.BinaryFile
			}
			if name != "" {
				if info, err := os.Stat(s.store.BlobPath(name)); err == nil && info.Size() <= int64(budget) {
					if data, err := os.ReadFile(s.store.BlobPath(name)); err == nil {
						si.BlobB64 = base64.StdEncoding.EncodeToString(data)
						budget -= len(si.BlobB64)
					}
				} else {
					snap.Truncated = true
				}
			}
		}
		snap.Items = append(snap.Items, si)
	}
	return snap, nil
}

func (s *ClipService) deviceName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "windows"
}

// ---- merge -------------------------------------------------------------

// applyRemote merges a remote snapshot into the local store. It keeps the
// newest version of each entry, unions the flags that must never regress
// (favourite state, counters) and restores blobs that only the remote has.
func (s *ClipService) applyRemote(remote *syncSnapshot) (added, updated int) {
	if remote == nil {
		return 0, 0
	}
	for _, ri := range remote.Items {
		if ri.ID == "" || ri.Hash == "" {
			continue
		}
		isNew, changed := s.store.UpsertRemote(ri.Item)
		if isNew {
			added++
		} else if changed {
			updated++
		}
		if ri.BlobB64 == "" {
			continue
		}
		name := ri.ImageFile
		ext := ".png"
		if name == "" {
			name = ri.BinaryFile
			ext = ".bin"
		}
		if name != "" && !fileExists(s.store.BlobPath(name)) {
			if data, err := base64.StdEncoding.DecodeString(ri.BlobB64); err == nil {
				_, _ = s.store.SaveBlob(strings.TrimSuffix(name, ext), ext, data)
			}
		}
	}
	return added, updated
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// ---- public API ---------------------------------------------------------

// GetSyncStatus returns the current sync state.
func (s *ClipService) GetSyncStatus() SyncStatus {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	st := s.syncStatus
	st.Enabled = s.store.Settings().SyncEnabled
	return st
}

// TestSync verifies the WebDAV endpoint without changing anything.
func (s *ClipService) TestSync() (string, error) {
	cfg, err := s.syncConfig()
	if err != nil {
		return "", err
	}
	snap, err := fetchRemote(cfg)
	if err != nil {
		return "", err
	}
	if snap == nil {
		return "连接成功，远端还没有同步文件（首次同步会创建）", nil
	}
	return fmt.Sprintf("连接成功，远端有 %d 条记录（更新于 %s）",
		len(snap.Items), snap.UpdatedAt.Local().Format("2006-01-02 15:04")), nil
}

// SyncNow performs a full two-way merge and upload.
func (s *ClipService) SyncNow() (SyncStatus, error) {
	return s.syncOnce(true)
}

func (s *ClipService) syncOnce(upload bool) (SyncStatus, error) {
	cfg, err := s.syncConfig()
	if err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}

	s.syncMu.Lock()
	if s.syncStatus.InProgress {
		s.syncMu.Unlock()
		return s.GetSyncStatus(), errors.New("同步正在进行")
	}
	s.syncStatus.InProgress = true
	s.syncMu.Unlock()

	s.emit("sync:status", s.GetSyncStatus())
	defer func() {
		s.syncMu.Lock()
		s.syncStatus.InProgress = false
		s.syncMu.Unlock()
		s.emit("sync:status", s.GetSyncStatus())
	}()

	remote, err := fetchRemote(cfg)
	if err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}
	added, updated := s.applyRemote(remote)

	if upload {
		snap, err := s.buildSnapshot(true)
		if err != nil {
			s.setSyncError(err)
			return s.GetSyncStatus(), err
		}
		if err := putRemote(cfg, snap); err != nil {
			s.setSyncError(err)
			return s.GetSyncStatus(), err
		}
	}

	remoteCount := 0
	if remote != nil {
		remoteCount = len(remote.Items)
	}
	result := fmt.Sprintf("拉取 %d 条新增 / %d 条更新", added, updated)
	if upload {
		result += fmt.Sprintf("，已上传 %d 条", len(s.store.All()))
	}

	s.syncMu.Lock()
	s.syncStatus.LastSync = time.Now().Format(time.RFC3339)
	s.syncStatus.LastResult = result
	s.syncStatus.LastError = ""
	s.syncStatus.RemoteItems = remoteCount
	s.syncMu.Unlock()

	Logf(s.store.Dir(), "sync ok: %s", result)
	s.emit("clip:stats", s.store.Stats())
	return s.GetSyncStatus(), nil
}

func (s *ClipService) setSyncError(err error) {
	s.syncMu.Lock()
	s.syncStatus.LastError = err.Error()
	s.syncMu.Unlock()
	Logf(s.store.Dir(), "sync failed: %v", err)
	s.emit("sync:status", s.GetSyncStatus())
}

// syncLoop runs the periodic sync; restartSyncLoop restarts it after a settings
// change so a new interval takes effect immediately.
func (s *ClipService) syncLoop() {
	s.syncRestart.Lock()
	if s.syncCancel != nil {
		s.syncCancel()
		s.syncCancel = nil
	}
	st := s.store.Settings()
	if !st.SyncEnabled || st.SyncIntervalMin <= 0 {
		s.syncRestart.Unlock()
		return
	}
	interval := time.Duration(st.SyncIntervalMin) * time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncRestart.Unlock()

	go func() {
		// First pass shortly after startup so a fresh machine pulls the history.
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			_, _ = s.syncOnce(true)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.syncOnce(true)
			}
		}
	}()
}
