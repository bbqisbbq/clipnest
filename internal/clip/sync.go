package clip

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WebDAV sync, third format: one packaged archive per device.
//
//	<WebDAV dir>/
//	  devices/<deviceID>.zip     ← 每台机器只写自己的这一个文件
//
// Why this shape:
//
//   - 打包传输：一次 GET 拿到对方的全部数据，一次 PUT 交出自己的，没有成百上千个小文件，
//     也不会有「1000 条 → 1000 次请求」的首次同步。
//   - 每台机器只写自己的包，**写冲突不可能发生**：没有任何两个客户端 PUT 同一个路径，
//     不需要锁、不需要 CAS，也就不怕服务器支不支持 If-Match。
//   - 合并是幂等的并集（内容哈希 + 时间戳），所以重复拉取、乱序拉取都安全。
//
// Package layout (zip):
//
//	package.json            index: items[hash/updatedAt/blob], deleted[tombstones], settings
//	items/<hash>.json       one record each
//	blobs/<hash>.png|.bin   raw payloads (only ones peers do not have yet)

const (
	// DefaultSyncFile is the directory-relative name of *our* package. Devices
	// keep separate files, so this is a prefix, not a shared document.
	DefaultSyncFile = "clipnest-sync.zip"
	// legacySyncFile/legacyManifest are v1/v2 remote names; existing settings
	// are migrated to the packaged layout automatically.
	legacySyncFile = "clipnest-sync.json"
	legacyManifest = "manifest.json"

	syncDevicesDir = "clipnest_data"
	// syncHTTPTimeout bounds a single WebDAV request.
	syncHTTPTimeout = 60 * time.Second
	// syncPackageVersion is bumped whenever the archive layout changes.
	syncPackageVersion = 3
	// syncBlobBudget caps payload bytes inside one package.
	syncBlobBudget = 96 << 20
	// syncPeerConcurrency is how many peer packages are fetched in parallel.
	syncPeerConcurrency = 4
	// tombstoneRetentionDays keeps deletion markers long enough for a machine
	// that was offline for a while to still learn about the deletion.
	tombstoneRetentionDays = 180
	// maxTombstones caps index growth from deletions.
	maxTombstones = 2000
)

// SyncStatus is what the settings page shows.
type SyncStatus struct {
	Enabled      bool   `json:"enabled"`
	InProgress   bool   `json:"inProgress"`
	LastSync     string `json:"lastSync"`
	LastResult   string `json:"lastResult"`
	LastError    string `json:"lastError"`
	RemoteItems  int    `json:"remoteItems"`
	SettingsSync bool   `json:"settingsSync"`
	Devices      int    `json:"devices"`
	LastBytes    int64  `json:"lastBytes"`
	DeviceID     string `json:"deviceId"`
}

// manifestItem is the per-entry index record inside a package.
type manifestItem struct {
	Hash      string    `json:"hash"`
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Preview   string    `json:"preview,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	Blob      string    `json:"blob,omitempty"` // ".png" / ".bin"
	Pinned    bool      `json:"pinned,omitempty"`
	SeenCount int       `json:"seenCount,omitempty"`
	CopyCount int       `json:"copyCount,omitempty"`
}

// settingsPayload carries settings across machines with its own timestamp.
type settingsPayload struct {
	UpdatedAt time.Time `json:"updatedAt"`
	Device    string    `json:"device,omitempty"`
	Values    Settings  `json:"values"`
}

// pkgIndex is package.json: everything needed to merge without opening the
// per-item files.
type pkgIndex struct {
	Version   int            `json:"version"`
	DeviceID  string         `json:"deviceId"`
	Device    string         `json:"device"`
	UpdatedAt time.Time      `json:"updatedAt"`
	// Entries carries every entry (clipboard captures, snippets, handwritten).
	Entries []manifestItem `json:"entries"`
	Deleted []Tombstone    `json:"deleted,omitempty"`
	Settings     *settingsPayload `json:"settings,omitempty"`
	Truncated    bool             `json:"truncated,omitempty"`
}

// entryRecord is one entries/<hash>.json body.
type entryRecord struct {
	Version int   `json:"version"`
	Entry   Entry `json:"entry"`
}

// snippetRecord is one snippets/<hash>.json body.
type snippetRecord struct {
	Version int     `json:"version"`
	Snippet Snippet `json:"snippet"`
}

// syncPackage is a parsed archive.
type syncPackage struct {
	index   pkgIndex
	entries []Entry
	blobs   map[string][]byte // "entry-<hash><ext>"
	size     int64
}

// ---- transport ----------------------------------------------------------

type syncConfig struct {
	base     string
	user     string
	pass     string
	deviceID string
}

func (s *ClipService) syncConfig() (syncConfig, error) {
	st := s.store.Settings()
	if strings.TrimSpace(st.SyncURL) == "" {
		return syncConfig{}, errors.New("还没有填写 WebDAV 地址")
	}
	deviceID := strings.TrimSpace(st.SyncDeviceID)
	if deviceID == "" {
		deviceID = s.ensureDeviceID()
	}
	return syncConfig{
		base:     strings.TrimRight(strings.TrimSpace(st.SyncURL), "/") + "/",
		user:     st.SyncUser,
		pass:     st.SyncPass,
		deviceID: deviceID,
	}, nil
}

// ensureDeviceID gives this installation a stable random identity. Hostnames are
// not unique enough (cloned VMs, renamed machines) and two devices sharing an id
// would overwrite each other's package.
func (s *ClipService) ensureDeviceID() string {
	st := s.store.Settings()
	if strings.TrimSpace(st.SyncDeviceID) != "" {
		return st.SyncDeviceID
	}
	st.SyncDeviceID = newID()
	if err := s.store.SaveSettings(st); err != nil {
		Logf(s.store.Dir(), "sync: could not persist device id: %v", err)
	}
	return st.SyncDeviceID
}

// packageName is this machine's file inside syncDevicesDir. The hostname is part
// of the name on purpose: a user who copies %APPDATA\%ClipNest to another PC
// would otherwise clone the device id and the two machines would overwrite the
// same package.
func (c syncConfig) packageName() string {
	host := slug(hostName())
	if host == "" {
		return c.deviceID + ".zip"
	}
	return c.deviceID + "-" + host + ".zip"
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func (c syncConfig) urlFor(rel string) (string, error) {
	u, err := url.Parse(c.base)
	if err != nil {
		return "", fmt.Errorf("WebDAV 地址无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("WebDAV 地址必须以 http:// 或 https:// 开头")
	}
	u.Path = path.Join(u.Path, rel)
	return u.String(), nil
}

func newSyncHTTPClient() *http.Client { return &http.Client{Timeout: syncHTTPTimeout} }

func (c syncConfig) newRequest(ctx context.Context, method, rel string, body io.Reader) (*http.Request, error) {
	target, err := c.urlFor(rel)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)
	return req, nil
}

// davGet returns (body, true) or (nil, false) when the file does not exist.
func (c syncConfig) davGet(rel string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
	defer cancel()

	req, err := c.newRequest(ctx, http.MethodGet, rel, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("连接失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, false, nil
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, false, fmt.Errorf("认证失败（HTTP %d），请检查用户名/密码", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, false, fmt.Errorf("读取 %s 失败（HTTP %d）: %s",
			rel, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (c syncConfig) davPut(rel, contentType string, payload []byte) error {
	put := func() (*http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
		defer cancel()
		req, err := c.newRequest(ctx, http.MethodPut, rel, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		req.ContentLength = int64(len(payload))
		return newSyncHTTPClient().Do(req)
	}

	resp, err := put()
	if err != nil {
		return fmt.Errorf("上传失败: %w", err)
	}
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		if err := c.mkcol(path.Dir(rel)); err != nil {
			return err
		}
		if resp, err = put(); err != nil {
			return fmt.Errorf("上传失败: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return fmt.Errorf("上传 %s 失败（HTTP %d）: %s", rel, resp.StatusCode, strings.TrimSpace(string(body)))
}

// mkcol creates one directory level (and its parent when needed).
func (c syncConfig) mkcol(dir string) error {
	if dir == "." || dir == "/" || dir == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, "MKCOL", dir+"/", nil)
	if err != nil {
		return err
	}
	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusMethodNotAllowed, http.StatusCreated, http.StatusOK, http.StatusNoContent:
		return nil // 405 = already there on most servers
	case http.StatusConflict:
		if err := c.mkcol(path.Dir(dir)); err != nil {
			return err
		}
		return c.mkcol(dir)
	}
	return fmt.Errorf("创建目录 %s 失败（HTTP %d）", dir, resp.StatusCode)
}

// listPackages returns the package names (without directory) inside dir.
// davEntry is one file in the remote directory, as reported by PROPFIND.
type davEntry struct {
	Name     string
	Bytes    int64
	Modified time.Time
}

func (c syncConfig) listPackages(dir string) ([]davEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, "PROPFIND", dir+"/", strings.NewReader(
		`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")

	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("列目录失败: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, nil
	case http.StatusMultiStatus, http.StatusOK:
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("列目录失败（HTTP %d）: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var ms struct {
		Responses []struct {
			Href     string `xml:"href"`
			Propstat []struct {
				Prop struct {
					ContentLength string `xml:"getcontentlength"`
					LastModified  string `xml:"getlastmodified"`
					ResourceType  struct {
						Collection *struct{} `xml:"collection"`
					} `xml:"resourcetype"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, fmt.Errorf("目录列表解析失败: %w", err)
	}

	out := make([]davEntry, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		name := path.Base(strings.TrimSuffix(r.Href, "/"))
		if unescaped, err := url.PathUnescape(name); err == nil {
			name = unescaped
		}
		if !strings.HasSuffix(strings.ToLower(name), ".zip") {
			continue
		}
		e := davEntry{Name: name}
		for _, ps := range r.Propstat {
			if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.ContentLength), 10, 64); err == nil {
				e.Bytes = n
			}
			if t, err := http.ParseTime(strings.TrimSpace(ps.Prop.LastModified)); err == nil {
				e.Modified = t
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ---- package (zip) ------------------------------------------------------

// buildPackage serialises an index + records + payloads into one archive.
func buildPackage(idx pkgIndex, entries []entryRecord, blobs map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	head, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "package.json", Method: zip.Deflate})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(head); err != nil {
		return nil, err
	}

	for _, rec := range entries {
		body, err := json.Marshal(rec)
		if err != nil {
			return nil, err
		}
		f, err := zw.CreateHeader(&zip.FileHeader{
			Name:   "entries/" + rec.Entry.Hash + ".json",
			Method: zip.Deflate,
		})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(body); err != nil {
			return nil, err
		}
	}

	// Stable order keeps the fingerprint (and therefore the skip-push check)
	// deterministic between runs.
	names := make([]string, 0, len(blobs))
	for name := range blobs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := zw.CreateHeader(&zip.FileHeader{
			Name:   "blobs/" + name,
			Method: zip.Store, // already-compressed payloads
		})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(blobs[name]); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parsePackage reads an archive produced by buildPackage.
func parsePackage(data []byte) (*syncPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("同步包解析失败: %w", err)
	}
	pkg := &syncPackage{blobs: make(map[string][]byte, 8), size: int64(len(data))}

	for _, f := range zr.File {
		switch {
		case f.Name == "package.json":
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(io.LimitReader(rc, 64<<20))
			rc.Close()
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(body, &pkg.index); err != nil {
				return nil, fmt.Errorf("同步包索引解析失败: %w", err)
			}
		case strings.HasPrefix(f.Name, "entries/") && strings.HasSuffix(f.Name, ".json"):
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(io.LimitReader(rc, 8<<20))
			rc.Close()
			if err != nil {
				return nil, err
			}
			var rec entryRecord
			if err := json.Unmarshal(body, &rec); err != nil {
				continue // 单条坏数据不影响整包
			}
			if rec.Entry.Hash == "" {
				continue
			}
			pkg.entries = append(pkg.entries, rec.Entry)
		case strings.HasPrefix(f.Name, "blobs/"):
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(io.LimitReader(rc, 64<<20))
			rc.Close()
			if err != nil {
				return nil, err
			}
			pkg.blobs[strings.TrimPrefix(f.Name, "blobs/")] = body
		}
	}
	return pkg, nil
}

// ---- settings exchange --------------------------------------------------

// machineOnlyFields are settings that describe this machine or its connection to
// the sync server; a peer never overwrites them.
func machineOnlyFields(st Settings) Settings {
	st.SyncEnabled = false
	st.SyncURL = ""
	st.SyncUser = ""
	st.SyncPass = ""
	st.SyncFile = ""
	st.SyncIntervalMin = 0
	st.SyncDeviceID = ""
	st.AutoStart = false
	st.ShowOnLaunch = false
	st.SyncSettings = false
	st.SyncUpdatedAt = time.Time{}
	return st
}

// shareableStamp serialises the fields that sync, so SaveSettings can tell a
// real change (bump the stamp) from a no-op save.
func shareableStamp(st Settings) string {
	b, err := json.Marshal(machineOnlyFields(st))
	if err != nil {
		return ""
	}
	return string(b)
}

// ---- public API ---------------------------------------------------------

// GetSyncStatus returns the current sync state.
func (s *ClipService) GetSyncStatus() SyncStatus {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	st := s.syncStatus
	cur := s.store.Settings()
	st.Enabled = cur.SyncEnabled
	st.SettingsSync = cur.SyncSettings
	st.DeviceID = cur.SyncDeviceID
	return st
}

// TestSync verifies the endpoint and reports what is already there.
func (s *ClipService) TestSync() (string, error) {
	cfg, err := s.syncConfig()
	if err != nil {
		return "", err
	}
	if err := cfg.mkcol(syncDevicesDir); err != nil {
		return "", err
	}
	entries, err := cfg.listPackages(syncDevicesDir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "连接成功，远端还没有任何设备的同步包（首次同步会创建）", nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, fmt.Sprintf("%s（%s）", e.Name, humanBytes(int(e.Bytes))))
	}
	return fmt.Sprintf("连接成功，远端有 %d 个设备的同步包：%s", len(entries), strings.Join(names, "、")), nil
}

// SyncNow performs a full two-way merge.
func (s *ClipService) SyncNow() (SyncStatus, error) { return s.syncOnce(true) }

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

	startedAt := time.Now()
	s.emit("sync:status", s.GetSyncStatus())
	defer func() {
		s.syncMu.Lock()
		s.syncStatus.InProgress = false
		s.syncMu.Unlock()
		s.emit("sync:status", s.GetSyncStatus())
	}()

	if err := cfg.mkcol(syncDevicesDir); err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}

	entries, err := cfg.listPackages(syncDevicesDir)
	if err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}

	ownName := cfg.packageName()
	peers := make([]string, 0, len(entries))
	deviceInfo := make([]SyncDeviceInfo, 0, len(entries)+1)
	for _, e := range entries {
		info := SyncDeviceInfo{
			Name:      e.Name,
			Device:    deviceLabelFromName(e.Name),
			Bytes:     e.Bytes,
			UpdatedAt: e.Modified,
			Self:      e.Name == ownName,
		}
		deviceInfo = append(deviceInfo, info)
		if e.Name != ownName {
			peers = append(peers, e.Name)
		}
	}
	if len(deviceInfo) == 0 {
		deviceInfo = append(deviceInfo, SyncDeviceInfo{Name: ownName, Device: hostName(), Self: true})
	}

	// ---- pull every peer package (bounded parallelism) ----
	type peerResult struct {
		name string
		pkg  *syncPackage
		err  error
	}
	results := make([]peerResult, len(peers))
	sem := make(chan struct{}, syncPeerConcurrency)
	var wg sync.WaitGroup
	for i, name := range peers {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data, ok, err := cfg.davGet(syncDevicesDir + "/" + name)
			if err != nil {
				results[i] = peerResult{name: name, err: err}
				return
			}
			if !ok {
				return
			}
			pkg, err := parsePackage(data)
			results[i] = peerResult{name: name, pkg: pkg, err: err}
		}(i, name)
	}
	wg.Wait()

	pulledNew, pulledUpd, deletedLocal := 0, 0, 0
	peerHasBlob := make(map[string]bool, 256)
	peerTombstones := make([]Tombstone, 0, 32)
	var newestPeerSettings *settingsPayload

	for _, res := range results {
		if res.err != nil {
			s.setSyncError(fmt.Errorf("读取 %s 失败: %w", res.name, res.err))
			return s.GetSyncStatus(), res.err
		}
		if res.pkg == nil {
			continue
		}
		newBefore, updBefore, delBefore := pulledNew, pulledUpd, deletedLocal
		recordPeer := func() {
			for i := range deviceInfo {
				if deviceInfo[i].Name != res.name {
					continue
				}
				deviceInfo[i].Items = len(res.pkg.index.Entries)
				deviceInfo[i].PulledNew = pulledNew - newBefore
				deviceInfo[i].PulledUpdated = pulledUpd - updBefore
				deviceInfo[i].Deleted = deletedLocal - delBefore
			}
		}
		defer recordPeer()
		for _, mi := range res.pkg.index.Entries {
			if mi.Hash == "" {
				continue
			}
			if mi.Blob != "" {
				peerHasBlob[mi.Hash+mi.Blob] = true
			}
		}
		peerTombstones = append(peerTombstones, res.pkg.index.Deleted...)

		if p := res.pkg.index.Settings; p != nil {
			if newestPeerSettings == nil || p.UpdatedAt.After(newestPeerSettings.UpdatedAt) {
				newestPeerSettings = p
			}
		}

		// Deletions first: a peer that deleted an entry must not be undone by
		// our (older) copy of it.
		tombIdx := make(map[string]time.Time, len(res.pkg.index.Deleted))
		for _, t := range res.pkg.index.Deleted {
			if cur, ok := tombIdx[t.Hash]; !ok || t.DeletedAt.After(cur) {
				tombIdx[t.Hash] = t.DeletedAt
			}
			if local, ok := s.store.findEntryByHash(t.Hash); ok && t.DeletedAt.After(local.UpdatedAt) {
				if s.store.DeleteEntry(local.ID) {
					deletedLocal++
				}
			}
		}

		// ---- entries ----
		for _, remote := range res.pkg.entries {
			if remote.Hash == "" {
				continue
			}
			if at, killed := tombIdx[remote.Hash]; killed && !remote.UpdatedAt.After(at) {
				continue
			}
			if local, ok := s.store.findEntryByHash(remote.Hash); ok {
				if !remote.UpdatedAt.After(local.UpdatedAt) {
					// 只把置顶这类"或"语义的标记并过来
					if remote.Pinned && !local.Pinned {
						local.Pinned = true
						if err := s.store.mergeEntry(local); err == nil {
							pulledUpd++
						}
					}
					continue
				}
				if err := s.store.mergeEntry(remote); err == nil {
					pulledUpd++
				}
			} else {
				if err := s.store.mergeEntry(remote); err == nil {
					pulledNew++
				} else {
					Logf(s.store.Dir(), "merge entry %s: %v", remote.Hash, err)
				}
			}
			// 附件：内容寻址，正文里的 memoimg:<hash> 天然对得上
			if token := memoImageToken(remote.Content); token != "" {
				ext := blobExtForKind(remote.Kind)
				if payload, ok := res.pkg.blobs["entry-"+remote.Hash+ext]; ok {
					if _, err := s.store.SaveEntryBlob(payload, strings.TrimPrefix(ext, ".")); err != nil {
						Logf(s.store.Dir(), "restore entry blob %s: %v", remote.Hash, err)
					}
				}
			}
		}
}

	peerItemCount := 0
	for _, res := range results {
		if res.pkg != nil {
			peerItemCount += len(res.pkg.index.Entries)
		}
	}

	// ---- settings: newest stamp wins (machine-only fields excluded) ----
	settingsPulled, settingsPushed := false, false
	cur := s.store.Settings()
	if cur.SyncSettings {
		localStamp := cur.SyncUpdatedAt
		if newestPeerSettings != nil && newestPeerSettings.UpdatedAt.After(localStamp) {
			merged := newestPeerSettings.Values
			merged.SyncEnabled = cur.SyncEnabled
			merged.SyncURL = cur.SyncURL
			merged.SyncUser = cur.SyncUser
			merged.SyncPass = cur.SyncPass
			merged.SyncFile = cur.SyncFile
			merged.SyncIntervalMin = cur.SyncIntervalMin
			merged.SyncDeviceID = cur.SyncDeviceID
			merged.AutoStart = cur.AutoStart
			merged.ShowOnLaunch = cur.ShowOnLaunch
			merged.SyncSettings = cur.SyncSettings
			merged.SyncUpdatedAt = newestPeerSettings.UpdatedAt
			if err := s.store.SaveSettings(merged); err != nil {
				s.setSyncError(err)
				return s.GetSyncStatus(), err
			}
			Logf(s.store.Dir(), "sync: adopted settings from %s", newestPeerSettings.Device)
			s.emit("settings:changed", merged)
			settingsPulled = true
		} else if newestPeerSettings == nil || localStamp.After(newestPeerSettings.UpdatedAt) {
			settingsPushed = true
		}
	}

	// ---- push our package ----
	var pushedBytes int64
	skipNote := ""
	pushedNow := false
	if upload && s.store.CountEntries() == 0 && peerItemCount > 0 {
		// Our pull clearly did not work; publishing an empty package would make
		// this device look authoritative about having no history.
		Logf(s.store.Dir(), "sync: skip push, local history is empty but peers hold %d records", peerItemCount)
		upload = false
		skipNote = "本地历史为空但对方有数据，已跳过上传"
	}
	if upload {
		payload, fingerprint, err := s.buildOwnPackage(peerHasBlob, peerTombstones, cur)
		if err != nil {
			s.setSyncError(err)
			return s.GetSyncStatus(), err
		}
		pushedBytes = int64(len(payload))
		lastPush := s.store.Meta("sync.lastPush")
		target := syncDevicesDir + "/" + ownName
		if fingerprint == lastPush {
			// Nothing changed since the last successful push: skip the upload
			// entirely, so an idle 30-minute tick costs one PROPFIND.
			skipNote = "内容未变化，已跳过上传"
			Logf(s.store.Dir(), "sync: package unchanged (%d KiB), push skipped", len(payload)/1024)
		} else {
			if resolved, err := cfg.urlFor(target); err == nil {
				Logf(s.store.Dir(), "sync: PUT %s", resolved)
			}
			if err := cfg.davPut(target, "application/zip", payload); err != nil {
				s.setSyncError(err)
				return s.GetSyncStatus(), err
			}
			_ = s.store.SetMeta("sync.lastPush", fingerprint)
			pushedNow = true
			Logf(s.store.Dir(), "sync: pushed %s (%d KiB)", target, len(payload)/1024)
		}
	}

	result := fmt.Sprintf("拉取 %d 条新增 / %d 条更新", pulledNew, pulledUpd)
	if deletedLocal > 0 {
		result += fmt.Sprintf("，删除 %d 条", deletedLocal)
	}
	if upload {
		result += fmt.Sprintf("，本机包 %d KiB", pushedBytes/1024)
	}
	if settingsPulled {
		result += "，已同步远端设置"
	}
	if settingsPushed {
		result += "，已上传本机设置"
	}
	if len(peers) > 0 {
		result += fmt.Sprintf("（%d 台设备）", len(peers)+1)
	}

	s.syncMu.Lock()
	s.syncStatus.LastSync = time.Now().Format(time.RFC3339)
	s.syncStatus.LastResult = result
	s.syncStatus.LastError = ""
	s.syncStatus.RemoteItems = peerItemCount
	s.syncStatus.Devices = len(peers) + 1
	s.syncStatus.LastBytes = pushedBytes
	s.syncMu.Unlock()

	if pushedNow {
		for i := range deviceInfo {
			if deviceInfo[i].Self {
				deviceInfo[i].Bytes = pushedBytes
				deviceInfo[i].Items = s.store.CountEntries()
				deviceInfo[i].UpdatedAt = time.Now()
			}
		}
	}
	s.saveSyncDetail(SyncDetail{
		At:            time.Now(),
		DurationMs:    time.Since(startedAt).Milliseconds(),
		PulledNew:     pulledNew,
		PulledUpdated: pulledUpd,
		DeletedLocal:  deletedLocal,
		Pushed:        pushedNow,
		PushedBytes:   pushedBytes,
		SettingsPull:  settingsPulled,
		SettingsPush:  settingsPushed,
		Note:          skipNote,
	}, deviceInfo)

	Logf(s.store.Dir(), "sync ok: %s", result)
	s.emit("clip:stats", s.store.Stats())
	return s.GetSyncStatus(), nil
}

// buildOwnPackage renders everything this machine knows: all record bodies, the
// blobs no peer has yet, our tombstones and (when newest) our settings.
func (s *ClipService) buildOwnPackage(peerHasBlob map[string]bool, peerTombstones []Tombstone, st Settings) ([]byte, string, error) {
	entries := s.store.ListEntries(EntryQuery{Limit: 500})
	idx := pkgIndex{
		Version:   syncPackageVersion,
		DeviceID:  st.SyncDeviceID,
		Device:    s.deviceName(),
		UpdatedAt: time.Now().UTC(),
		Entries:   make([]manifestItem, 0, len(entries)),
	}
	records := make([]entryRecord, 0, len(entries))
	blobs := make(map[string][]byte, 8)
	budget := syncBlobBudget

	for _, e := range entries {
		if e.Hash == "" {
			continue
		}
		ext := blobExtForKind(e.Kind)
		if memoImageToken(e.Content) == "" {
			ext = ""
		}
		idx.Entries = append(idx.Entries, manifestItem{
			Hash:      e.Hash,
			ID:        e.ID,
			Kind:      e.Kind,
			Preview:   truncateRunes(e.Preview(), 120),
			UpdatedAt: e.UpdatedAt,
			Blob:      ext,
			Pinned:    e.Pinned,
			SeenCount: e.SeenCount,
			CopyCount: e.CopyCount,
		})
		records = append(records, entryRecord{Version: syncPackageVersion, Entry: e})

		if ext == "" || peerHasBlob["entry-"+e.Hash+ext] {
			continue // a peer already carries this payload
		}
		if budget <= 0 {
			idx.Truncated = true
			continue
		}
		payload, _, err := s.store.LoadEntryBlob(memoImageToken(e.Content))
		if err != nil || len(payload) == 0 || len(payload) > budget {
			idx.Truncated = true
			continue
		}
		blobs["entry-"+e.Hash+ext] = payload
		budget -= len(payload)
	}

	horizon := time.Now().AddDate(0, 0, -tombstoneRetentionDays)

	idx.Deleted = mergeTombstones(peerTombstones, s.store.TombstonesScoped("entry", horizon, maxTombstones))
	s.store.PruneTombstones(horizon)

	if st.SyncSettings {
		idx.Settings = &settingsPayload{
			UpdatedAt: st.SyncUpdatedAt,
			Device:    s.deviceName(),
			Values:    machineOnlyFields(st),
		}
	}

	blobNames := make([]string, 0, len(blobs))
	for name := range blobs {
		blobNames = append(blobNames, name)
	}
	payload, err := buildPackage(idx, records, blobs)
	if err != nil {
		return nil, "", err
	}
	return payload, packageFingerprint(idx, blobNames), nil
}

// packageFingerprint digests the *content* of a package (not the archive bytes
// and not the "updatedAt" stamp), so an idle machine can tell that re-uploading
// would change nothing.
func packageFingerprint(idx pkgIndex, blobNames []string) string {
	h := sha256.New()
	for _, it := range idx.Entries {
		fmt.Fprintf(h, "i|%s|%d|%t|%d|%d|%s\n", it.Hash, it.UpdatedAt.UnixNano(), it.Pinned, it.SeenCount, it.CopyCount, it.Blob)
	}
	for _, t := range idx.Deleted {
		fmt.Fprintf(h, "d|%s|%d\n", t.Hash, t.DeletedAt.UnixNano())
	}
	sort.Strings(blobNames)
	for _, n := range blobNames {
		fmt.Fprintf(h, "b|%s\n", n)
	}
	if idx.Settings != nil {
		fmt.Fprintf(h, "s|%d|%s\n", idx.Settings.UpdatedAt.UnixNano(), shareableStamp(idx.Settings.Values))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// mergeTombstones unions deletion markers, keeping the newest timestamp per hash.
func mergeTombstones(peer, local []Tombstone) []Tombstone {
	best := make(map[string]Tombstone, len(peer)+len(local))
	add := func(t Tombstone) {
		if t.Hash == "" {
			return
		}
		if cur, ok := best[t.Hash]; !ok || t.DeletedAt.After(cur.DeletedAt) {
			best[t.Hash] = t
		}
	}
	for _, t := range peer {
		add(t)
	}
	for _, t := range local {
		add(t)
	}
	out := make([]Tombstone, 0, len(best))
	for _, t := range best {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	if len(out) > maxTombstones {
		out = out[:maxTombstones]
	}
	return out
}

// blobExt reports the extension a record carries, from its derived blob name.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (s *ClipService) setSyncError(err error) {
	s.syncMu.Lock()
	s.syncStatus.LastError = err.Error()
	s.syncMu.Unlock()
	Logf(s.store.Dir(), "sync failed: %v", err)
	s.emit("sync:status", s.GetSyncStatus())
}

func (s *ClipService) deviceName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "windows"
}

// syncLoop runs the periodic sync; it is restarted after a settings change so a
// new interval takes effect immediately.
func (s *ClipService) syncLoop() {
	s.syncRestart.Lock()
	if s.syncCancel != nil {
		s.syncCancel()
		s.syncCancel = nil
	}
	st := s.store.Settings()
	if !st.SyncEnabled || st.SyncIntervalMin <= 0 {
		Logf(s.store.Dir(), "sync loop idle (enabled=%t, interval=%d)", st.SyncEnabled, st.SyncIntervalMin)
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
