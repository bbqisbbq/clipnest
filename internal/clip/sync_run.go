package clip

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// 一次同步的编排：列远端包 → 并行拉取对端包 → 按墓碑/时间戳合并条目与设置 →
// 组装并上传自己的包 → 更新状态、明细与日志。
// 不负责：HTTP 细节（sync_dav.go）、包格式（sync_package.go）、周期调度（sync.go）。

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
