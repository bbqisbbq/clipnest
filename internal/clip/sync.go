package clip

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// 同步的总览与门面：远端布局约定、包/超时/保留期常量、对外状态（SyncStatus）与设置页
// 调用的入口（GetSyncStatus / TestSync / SyncNow），以及周期同步循环与错误记录。
// 不负责：WebDAV 传输细节、zip 打包、合并规则、单次同步的流程——分别在
// sync_dav.go / sync_package.go / sync_merge.go / sync_run.go。

// WebDAV sync, third format: one packaged archive per device.
//
//	<WebDAV dir>/
//	  clipnest_data/<deviceId>-<host>.zip   ← 每台机器只写自己的这一个文件
//
// Why this shape:
//
//   - 打包传输：一次 GET 拿到对方的全部数据，一次 PUT 交出自己的，没有成百上千个小文件，
//     也不会有「1000 条 → 1000 次请求」的首次同步。
//   - 每台机器只写自己的包，**写冲突不可能发生**：没有任何两个客户端 PUT 同一个路径，
//     不需要锁、不需要 CAS，也就不怕服务器支不支持 If-Match。
//   - 合并是幂等的并集（内容哈希 + 时间戳），所以重复拉取、乱序拉取都安全。
//
// Package layout (zip)，以 sync_package.go 的 buildPackage/parsePackage 为准：
//
//	package.json             索引：entries[hash/updatedAt/pinned/blob]、deleted[tombstones]、settings
//	entries/<hash>.json      一条一个文件
//	blobs/entry-<hash>.<ext> 原始负载（只带对端还没有的，见 syncBlobBudget）

const (
	syncDevicesDir = "clipnest_data"
	// syncHTTPTimeout bounds a single WebDAV request.
	syncHTTPTimeout = 60 * time.Second
	// syncPackageVersion is bumped whenever the archive layout changes.
	syncPackageVersion = 3
	// syncMaxEntries caps how many entries one package carries (newest first).
	// 超过这个数的老条目**不会**进包——不是"整份历史"，见 docs/SYNC.md。
	syncMaxEntries = 500
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
	others := 0
	for _, e := range entries {
		names = append(names, fmt.Sprintf("%s（%s）", e.Name, humanBytes(int(e.Bytes))))
		if e.Name != cfg.packageName() {
			others++
		}
	}
	if others == 0 {
		// 只有自己的包：连得上、但"另一台机器"从没往这里写过。
		return fmt.Sprintf("连接成功，但远端只有本机这一个包（%s）：其他设备还没上传过。"+
			"请在另一台机器上打开 设置 → 云同步，填同一个目录、点「立即同步」。", names[0]), nil
	}
	return fmt.Sprintf("连接成功，远端有 %d 个设备的同步包：%s", len(entries), strings.Join(names, "、")), nil
}

// SyncNow performs a full two-way merge.
func (s *ClipService) SyncNow() (SyncStatus, error) { return s.syncOnce(true) }

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
