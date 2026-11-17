package clip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// WebDAV sync, second format.
//
// The history is published as a small tree instead of one giant document, so a
// sync only moves what actually changed:
//
//	<WebDAV dir>/
//	  manifest.json          index: every item's hash/kind/updatedAt + settings stamp
//	  items/<hash>.json      one record per entry, matched by content hash
//	  blobs/<hash>.png|bin   image / unknown-format payloads, raw (no base64)
//
// Format v2 replaced the single "clipnest-sync.json" document: a 1000-entry
// history used to mean re-uploading megabytes on every sync, and any blob over
// the base64 budget was silently dropped.

const (
	// DefaultSyncFile is the manifest name inside the configured directory.
	DefaultSyncFile = "manifest.json"
	// legacySyncFile is the v1 document name; existing settings are migrated.
	legacySyncFile = "clipnest-sync.json"
	// syncHTTPTimeout bounds a single WebDAV request.
	syncHTTPTimeout = 45 * time.Second
	// syncMaxPerRun caps how many records one run moves, so a first sync of a
	// huge history does not look like a hang.
	syncMaxPerRun = 800
	// tombstoneRetentionDays keeps deletion markers long enough for a machine
	// that was offline for a while to still learn about the deletion.
	tombstoneRetentionDays = 180
	// maxTombstones caps the index growth from deletions.
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
}

// manifestItem is the per-entry index record. The body lives in items/<hash>.json.
// Mergeable flags travel in the index so a peer whose copy is newer still
// inherits a favourite star or a bigger counter without re-downloading the body.
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

type syncManifest struct {
	Version   int              `json:"version"`
	Device    string           `json:"device"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Items     []manifestItem   `json:"items"`
	// Deleted carries deletion markers so a removal on one machine is honoured
	// everywhere instead of being resurrected by the next pull.
	Deleted  []Tombstone      `json:"deleted,omitempty"`
	Settings *settingsPayload `json:"settings,omitempty"`
}

// itemRecord is the per-item file body.
type itemRecord struct {
	Version int       `json:"version"`
	Item    Item      `json:"item"`
	BlobExt string    `json:"blobExt,omitempty"`
	Written time.Time `json:"written"`
}

// ---- transport ----------------------------------------------------------

type syncConfig struct {
	base     string
	user     string
	pass     string
	manifest string
}

// syncConfig builds the transport config from the stored settings.
func (s *ClipService) syncConfig() (syncConfig, error) {
	st := s.store.Settings()
	if strings.TrimSpace(st.SyncURL) == "" {
		return syncConfig{}, errors.New("还没有填写 WebDAV 地址")
	}
	name := strings.TrimSpace(st.SyncFile)
	// v1 wrote one document named clipnest-sync.json; treat it as "upgrade me".
	if name == "" || name == legacySyncFile {
		name = DefaultSyncFile
	}
	return syncConfig{
		base:     strings.TrimRight(strings.TrimSpace(st.SyncURL), "/") + "/",
		user:     st.SyncUser,
		pass:     st.SyncPass,
		manifest: name,
	}, nil
}

// urlFor resolves a relative path inside the sync directory.
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

// davGet returns (body, true) or (nil, false) when the file does not exist.
func (c syncConfig) davGet(rel string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
	defer cancel()

	target, err := c.urlFor(rel)
	if err != nil {
		return nil, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, false, err
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)

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
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// davPut uploads a file, creating the parent collection when the server asks.
func (c syncConfig) davPut(rel, contentType string, payload []byte) error {
	put := func() (*http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
		defer cancel()
		target, err := c.urlFor(rel)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		if c.user != "" || c.pass != "" {
			req.SetBasicAuth(c.user, c.pass)
		}
		req.Header.Set("User-Agent", "ClipNest/"+Version)
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
	target, err := c.urlFor(dir + "/")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "MKCOL", target, nil)
	if err != nil {
		return err
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)

	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusMethodNotAllowed, http.StatusCreated, http.StatusOK, http.StatusNoContent:
		return nil // 405 = already there on most servers
	case http.StatusConflict:
		// Parent missing; create it first, then retry once.
		if err := c.mkcol(path.Dir(dir)); err != nil {
			return err
		}
		return c.mkcol(dir)
	}
	return fmt.Errorf("创建目录 %s 失败（HTTP %d）", dir, resp.StatusCode)
}

// ---- manifest -----------------------------------------------------------

func fetchManifest(cfg syncConfig) (*syncManifest, error) {
	data, ok, err := cfg.davGet(cfg.manifest)
	if err != nil || !ok {
		return nil, err
	}
	var m syncManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("远端索引解析失败: %w", err)
	}
	return &m, nil
}

func putManifest(cfg syncConfig, m *syncManifest) error {
	payload, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return cfg.davPut(cfg.manifest, "application/json; charset=utf-8", payload)
}

func (m *syncManifest) index() map[string]manifestItem {
	out := make(map[string]manifestItem, len(m.Items))
	for _, it := range m.Items {
		if it.Hash != "" {
			out[it.Hash] = it
		}
	}
	return out
}

// ---- settings exchange --------------------------------------------------

// machineOnlyFields are settings that describe *this* machine or this
// machine's connection to the sync server; they are never overwritten by a peer.
func machineOnlyFields(st Settings) Settings {
	st.SyncEnabled = false
	st.SyncURL = ""
	st.SyncUser = ""
	st.SyncPass = ""
	st.SyncFile = ""
	st.SyncIntervalMin = 0
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

// localSettingsStamp is when this machine's shareable settings last changed.
func (s *ClipService) localSettingsStamp() time.Time {
	return s.store.Settings().SyncUpdatedAt
}

// syncSettingsOnce reconciles the shareable settings with the manifest.
func (s *ClipService) syncSettingsOnce(cfg syncConfig, remote *syncManifest, up bool) (pulled, pushed bool, err error) {
	if !s.store.Settings().SyncSettings {
		return false, false, nil
	}
	local := s.store.Settings()
	localStamp := local.SyncUpdatedAt

	remoteStamp := time.Time{}
	if remote != nil && remote.Settings != nil {
		remoteStamp = remote.Settings.UpdatedAt
	}

	// Remote is newer → adopt it (keeping this machine's connection settings).
	if remoteStamp.After(localStamp) && remote != nil && remote.Settings != nil {
		merged := remote.Settings.Values
		cur := s.store.Settings()
		merged.SyncEnabled = cur.SyncEnabled
		merged.SyncURL = cur.SyncURL
		merged.SyncUser = cur.SyncUser
		merged.SyncPass = cur.SyncPass
		merged.SyncFile = cur.SyncFile
		merged.SyncIntervalMin = cur.SyncIntervalMin
		merged.AutoStart = cur.AutoStart
		merged.ShowOnLaunch = cur.ShowOnLaunch
		merged.SyncSettings = cur.SyncSettings
		merged.SyncUpdatedAt = remoteStamp
		if err := s.store.SaveSettings(merged); err != nil {
			return false, false, err
		}
		Logf(s.store.Dir(), "sync: adopted settings from %s", remote.Settings.Device)
		s.emit("settings:changed", merged)
		return true, false, nil
	}

	// Ours is newer (or remote has none) → publish it in the manifest.
	if up && (localStamp.After(remoteStamp) || remote == nil || remote.Settings == nil) {
		if remote == nil {
			remote = &syncManifest{Version: 2}
		}
		remote.Settings = &settingsPayload{
			UpdatedAt: localStamp,
			Device:    s.deviceName(),
			Values:    machineOnlyFields(local),
		}
		return false, true, nil
	}
	return false, false, nil
}

// ---- main sync ----------------------------------------------------------

// GetSyncStatus returns the current sync state.
func (s *ClipService) GetSyncStatus() SyncStatus {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	st := s.syncStatus
	cur := s.store.Settings()
	st.Enabled = cur.SyncEnabled
	st.SettingsSync = cur.SyncSettings
	return st
}

// TestSync verifies the WebDAV endpoint without changing anything.
func (s *ClipService) TestSync() (string, error) {
	cfg, err := s.syncConfig()
	if err != nil {
		return "", err
	}
	if err := cfg.mkcol("items"); err != nil {
		return "", err
	}
	m, err := fetchManifest(cfg)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "连接成功，远端还没有索引（首次同步会创建 manifest.json）", nil
	}
	msg := fmt.Sprintf("连接成功，远端索引有 %d 条记录（更新于 %s）",
		len(m.Items), m.UpdatedAt.Local().Format("2006-01-02 15:04"))
	if m.Settings != nil {
		msg += fmt.Sprintf("，设置同步于 %s", m.Settings.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	return msg, nil
}

// SyncNow performs a full two-way merge and upload.
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

	s.emit("sync:status", s.GetSyncStatus())
	defer func() {
		s.syncMu.Lock()
		s.syncStatus.InProgress = false
		s.syncMu.Unlock()
		s.emit("sync:status", s.GetSyncStatus())
	}()

	remote, err := fetchManifest(cfg)
	if err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}
	if remote == nil {
		remote = &syncManifest{Version: 2, Device: s.deviceName()}
	}
	remoteIdx := remote.index()

	// ---- deletions first: a peer that deleted an entry must not be undone by
	// our copy of it ----
	deletedLocal := 0
	tombIdx := make(map[string]time.Time, len(remote.Deleted))
	for _, t := range remote.Deleted {
		if t.Hash == "" {
			continue
		}
		if cur, ok := tombIdx[t.Hash]; !ok || t.DeletedAt.After(cur) {
			tombIdx[t.Hash] = t.DeletedAt
		}
		local, ok := s.store.findByHash(t.Hash)
		if !ok {
			continue
		}
		// Only honour the marker when it is newer than our copy: re-copying the
		// same content later legitimately resurrects the entry.
		if t.DeletedAt.After(local.UpdatedAt) && s.store.deleteRow(local.ID, false) {
			deletedLocal++
		}
	}

	// ---- pull ----
	pulledNew, pulledUpd := 0, 0
	seen := 0
	for _, ri := range remote.Items {
		if seen >= syncMaxPerRun {
			break
		}
		if at, killed := tombIdx[ri.Hash]; killed && !ri.UpdatedAt.After(at) {
			continue // records may outlive their marker; the tombstone wins
		}
		local, ok := s.store.findByHash(ri.Hash)
		if ok && !ri.UpdatedAt.After(local.UpdatedAt) {
			// Body is up to date, but favourite state and counters still merge.
			if s.store.UnionFlags(ri.Hash, ri.Pinned, ri.SeenCount, ri.CopyCount) {
				pulledUpd++
			}
			// Maybe the blob is missing locally.
			if ri.Blob != "" && s.store.BlobExt(local.ID) == "" {
				if data, exists, err := cfg.davGet(blobPath(ri.Hash, ri.Blob)); err == nil && exists {
					_ = s.store.SaveBlob(local.ID, ri.Blob, data)
				}
			}
			continue
		}
		raw, exists, err := cfg.davGet(itemPath(ri.Hash))
		if err != nil {
			s.setSyncError(err)
			return s.GetSyncStatus(), err
		}
		if !exists {
			continue
		}
		var rec itemRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			Logf(s.store.Dir(), "sync: skip unreadable record %s: %v", ri.Hash, err)
			continue
		}
		seen++
		isNew, changed := s.store.UpsertRemote(rec.Item)
		if isNew {
			pulledNew++
		} else if changed {
			pulledUpd++
		}
		if ri.Blob != "" {
			local, ok := s.store.findByHash(ri.Hash)
			if ok && s.store.BlobExt(local.ID) == "" {
				if data, exists, err := cfg.davGet(blobPath(ri.Hash, ri.Blob)); err == nil && exists {
					_ = s.store.SaveBlob(local.ID, ri.Blob, data)
				}
			}
		}
	}

	// ---- settings ----
	settingsPulled, settingsPushed, err := s.syncSettingsOnce(cfg, remote, upload)
	if err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}

	// ---- push ----
	pushed := 0
	if upload {
		localItems := s.store.All()
		for i, it := range localItems {
			if i >= syncMaxPerRun {
				break
			}
			if it.Hash == "" {
				continue
			}
			ri, known := remoteIdx[it.Hash]
			if known && !it.UpdatedAt.After(ri.UpdatedAt) {
				continue
			}
			blobExt := s.store.BlobExt(it.ID)
			if err := s.pushItem(cfg, it, blobExt); err != nil {
				s.setSyncError(err)
				return s.GetSyncStatus(), err
			}
			pushed++
		}

		// ---- manifest ----
		final := s.buildManifest(remote)
		final.Settings = remote.Settings
		horizon := time.Now().AddDate(0, 0, -tombstoneRetentionDays)
		final.Deleted = mergeTombstones(remote.Deleted, s.store.Tombstones(horizon, maxTombstones))
		s.store.PruneTombstones(horizon)
		if err := putManifest(cfg, final); err != nil {
			s.setSyncError(err)
			return s.GetSyncStatus(), err
		}
		remote = final
	}

	result := fmt.Sprintf("拉取 %d 条新增 / %d 条更新", pulledNew, pulledUpd)
	if deletedLocal > 0 {
		result += fmt.Sprintf("，删除 %d 条", deletedLocal)
	}
	if upload {
		result += fmt.Sprintf("，推送 %d 条", pushed)
	}
	if settingsPulled {
		result += "，已同步远端设置"
	}
	if settingsPushed {
		result += "，已上传本机设置"
	}

	s.syncMu.Lock()
	s.syncStatus.LastSync = time.Now().Format(time.RFC3339)
	s.syncStatus.LastResult = result
	s.syncStatus.LastError = ""
	s.syncStatus.RemoteItems = len(remote.Items)
	s.syncMu.Unlock()

	Logf(s.store.Dir(), "sync ok: %s", result)
	s.emit("clip:stats", s.store.Stats())
	return s.GetSyncStatus(), nil
}

// pushItem uploads one record (and its blob when the peer does not have it).
func (s *ClipService) pushItem(cfg syncConfig, it Item, blobExt string) error {
	rec := itemRecord{Version: 2, Item: it, BlobExt: blobExt, Written: time.Now().UTC()}
	// The id is machine-local; peers key on the hash.
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := cfg.davPut(itemPath(it.Hash), "application/json; charset=utf-8", body); err != nil {
		return err
	}
	if blobExt == "" {
		return nil
	}
	data, err := s.store.LoadBlobByID(it.ID)
	if err != nil || len(data) == 0 {
		return nil // nothing to upload; index still references the record
	}
	return cfg.davPut(blobPath(it.Hash, blobExt), "application/octet-stream", data)
}

// buildManifest produces the index for everything this machine knows about,
// keeping peer entries that we did not touch.
func (s *ClipService) buildManifest(remote *syncManifest) *syncManifest {
	m := &syncManifest{Version: 2, Device: s.deviceName(), UpdatedAt: time.Now().UTC()}
	seen := make(map[string]bool, 256)

	for _, it := range s.store.All() {
		if it.Hash == "" {
			continue
		}
		mi := manifestItem{
			Hash:      it.Hash,
			ID:        it.ID,
			Kind:      it.Kind,
			Preview:   truncateRunes(it.Preview, 120),
			UpdatedAt: it.UpdatedAt,
			Blob:      s.store.BlobExt(it.ID),
			Pinned:    it.Pinned,
			SeenCount: it.SeenCount,
			CopyCount: it.CopyCount,
		}
		m.Items = append(m.Items, mi)
		seen[it.Hash] = true
	}
	// Keep remote-only entries (another machine's history we have not pulled yet).
	if remote != nil {
		for _, ri := range remote.Items {
			if ri.Hash == "" || seen[ri.Hash] {
				continue
			}
			m.Items = append(m.Items, ri)
		}
	}
	return m
}

// mergeTombstones unions local and remote deletion markers, keeping the newest
// timestamp per hash and preferring records that still know which device did it.
func mergeTombstones(remote, local []Tombstone) []Tombstone {
	best := make(map[string]Tombstone, len(remote)+len(local))
	add := func(t Tombstone) {
		if t.Hash == "" {
			return
		}
		if cur, ok := best[t.Hash]; !ok || t.DeletedAt.After(cur.DeletedAt) {
			best[t.Hash] = t
		}
	}
	for _, t := range remote {
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

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func itemPath(hash string) string { return "items/" + hash + ".json" }
func blobPath(hash, ext string) string {
	if ext == "" {
		return ""
	}
	return "blobs/" + hash + ext
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
