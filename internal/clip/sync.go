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
	// 包里是**全量**：库里有的条目都进包。早期版本只带"最新 500 条"，超过 500 条
	// 之后更老的历史永远上不了云——那才是"家里电脑上找不到"的第二个原因。
	// 现在包大小由压缩、附件预算（syncBlobBudget）、指纹跳过三件事兜着。
	//
	// syncBlobBudget caps payload bytes inside one package.
	syncBlobBudget = 96 << 20
	// syncPackageWarnBytes 只是日志阈值：包超过它就写一行，让人知道"再攒下去
	// 会撞服务器配额 / 60 秒请求超时"，而不是等同步开始失败才发现。
	syncPackageWarnBytes = 24 << 20
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
	entries, ignored, err := cfg.listPackages(syncDevicesDir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		msg := "连接成功，远端还没有任何设备的同步包（首次同步会创建）"
		if len(ignored) > 0 {
			msg += fmt.Sprintf("；不过目录里有 %d 个无法识别的文件：%s（可能是旧版本留下的布局）",
				len(ignored), strings.Join(ignored, "、"))
		}
		if hint := cfg.siblingHint(); hint != "" {
			msg += "；" + hint
		}
		return msg, nil
	}
	names := make([]string, 0, len(entries))
	others := 0
	for _, e := range entries {
		names = append(names, fmt.Sprintf("%s（%s，%s）", e.Name, humanBytes(int(e.Bytes)), describeStamp(e.Modified)))
		if e.Name != cfg.packageName() {
			others++
		}
	}
	if others == 0 {
		// 只有自己的包：连得上、但"另一台机器"从没往这里写过。
		msg := fmt.Sprintf("连接成功，但远端只有本机这一个包（%s）：其他设备还没上传过。"+
			"请在另一台机器上打开 设置 → 云同步，填同一个目录、点「立即同步」。", names[0])
		if len(ignored) > 0 {
			msg += fmt.Sprintf("另外这个目录里还有 %d 个无法识别的文件：%s。", len(ignored), strings.Join(ignored, "、"))
		}
		if hint := cfg.siblingHint(); hint != "" {
			msg += hint
		}
		return msg, nil
	}
	return fmt.Sprintf("连接成功，远端有 %d 个设备的同步包：%s", len(entries), strings.Join(names, "、")), nil
}

// describeStamp renders a server-reported modification time for the status line.
func describeStamp(t time.Time) string {
	if t.IsZero() {
		return "服务器未提供时间"
	}
	return t.Local().Format("2006-01-02 15:04")
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
//
// 节奏：启动后 5 秒先跑一轮（新机器要赶紧把历史拉下来），成功之后按 interval
// 定时；**失败就退避重试**（1 → 3 → 10 分钟，之后落回 interval），成功即清零。
//
// 以前失败后只等下一个 interval：开机那一枪撞上网络还没连上（机器日志里就有
// `Mkcol … EOF`）就等于白等 30 分钟，用户看到的是"两天一次都没成功"。
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
	ctx, cancel := context.WithCancel(s.bgCtx)
	s.syncCancel = cancel
	s.syncRestart.Unlock()

	go func() {
		// 刚同步过（<60 秒）就不再跑第一枪：设置页是自动保存的，以前改几个字段
		// 就会连着重启循环、连着跑整轮同步（日志里 10:04:17/29/30 三连就是它）。
		if !sleepCtx(ctx, firstPassDelay(s.GetSyncStatus().LastSync)) {
			return
		}
		failures := 0
		if _, err := s.syncOnce(true); err != nil {
			failures = 1
		}
		for {
			if !sleepCtx(ctx, nextDelay(interval, failures)) {
				return
			}
			if _, err := s.syncOnce(true); err != nil {
				failures++
			} else {
				failures = 0
			}
		}
	}()
}

// firstPassDelay is how long to wait before the loop's first round: 5 seconds
// after startup, but never sooner than 60 seconds after the last successful run.
func firstPassDelay(lastSync string) time.Duration {
	const grace = 5 * time.Second
	last, err := time.Parse(time.RFC3339, lastSync)
	if err != nil {
		return grace
	}
	since := time.Since(last)
	if since >= time.Minute {
		return grace
	}
	return time.Minute - since
}

// nextDelay is the wait before the next round: the normal interval after a good
// round, a short backoff after failures (capped by the interval).
func nextDelay(interval time.Duration, failures int) time.Duration {
	if failures <= 0 {
		return interval
	}
	backoff := []time.Duration{time.Minute, 3 * time.Minute, 10 * time.Minute}
	idx := failures - 1
	if idx >= len(backoff) {
		return interval
	}
	if d := backoff[idx]; d < interval {
		return d
	}
	return interval
}

// sleepCtx waits for d, returning false when the loop was cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
