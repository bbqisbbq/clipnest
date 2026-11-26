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
//
// 流程拆成 listPeers / pull / merge / adoptSettings / push / report 六段，共享状态
// 放在 syncRun 上。原来这是一个 300 行单函数、十几个局部变量跨段共享，而且录设备
// 明细的 `defer recordPeer()` 写在了 for 循环里 —— defer 全部推迟到函数返回才执行，
// 闭包读到的累加器已是最终值，于是每台设备的"新增/更新/删除"都成了累计差值。
// 现在每台设备的计数在 mergePeer 里就地算好再写回。

// syncPeerResult is one peer package as fetched in the pull phase.
type syncPeerResult struct {
	name string
	pkg  *syncPackage
	err  error
}

// syncPeerStats counts what one peer package changed locally.
type syncPeerStats struct {
	added   int
	updated int
	deleted int
}

// syncPushInfo describes what the push phase did.
type syncPushInfo struct {
	bytes  int64
	pushed bool
	note   string
}

// syncRun holds the state of one sync pass.
type syncRun struct {
	svc       *ClipService
	cfg       syncConfig
	ownName   string
	startedAt time.Time

	// discovery
	deviceInfo []SyncDeviceInfo
	peers      []string
	// ignoredRemote 是同步目录里认不出来的文件（旧布局 / 半截上传）：报给用户看，
	// 静默忽略会让"对方明明在同步"这件事毫无线索。
	ignoredRemote []string
	// siblingHint 是"隔壁目录里也有 clipnest_data"的一句话解释（""=没有）。
	siblingHint string

	// pull
	results []syncPeerResult
	// peerFails 是这一轮读不动的对端包（名字+原因）。**不再中断整轮**。
	peerFails []string

	// merge（也是最终汇报的数字）
	pulledNew     int
	pulledUpdated int
	deletedLocal  int

	peerHasBlob    map[string]bool
	peerTombstones []Tombstone
	newestSettings *settingsPayload
	peerItems      int

	// push 阶段的附件统计（见 buildOwnPackage）
	blobsSent     int
	blobsCap      int
	blobsMissing  int
	blobsOversize int
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

	r := &syncRun{
		svc:            s,
		cfg:            cfg,
		ownName:        cfg.packageName(),
		startedAt:      time.Now(),
		peerHasBlob:    make(map[string]bool, 256),
		peerTombstones: make([]Tombstone, 0, 32),
	}

	s.emit("sync:status", s.GetSyncStatus())
	defer func() {
		s.syncMu.Lock()
		s.syncStatus.InProgress = false
		s.syncMu.Unlock()
		s.emit("sync:status", s.GetSyncStatus())
	}()

	if err := r.listPeers(); err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}
	if err := r.pull(); err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}
	if err := r.merge(); err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}
	settingsPulled, settingsPushed, err := r.adoptSettings()
	if err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}
	info, err := r.push(upload)
	if err != nil {
		s.setSyncError(err)
		return s.GetSyncStatus(), err
	}
	r.report(info, settingsPulled, settingsPushed)
	return s.GetSyncStatus(), nil
}

// ---- 1. discovery -------------------------------------------------------

// listPeers ensures the remote folder exists and enumerates device packages.
func (r *syncRun) listPeers() error {
	if err := r.cfg.mkcol(syncDevicesDir); err != nil {
		return err
	}
	entries, ignored, err := r.cfg.listPackages(syncDevicesDir)
	if err != nil {
		return err
	}
	r.ignoredRemote = ignored
	r.deviceInfo = make([]SyncDeviceInfo, 0, len(entries)+1)
	for _, e := range entries {
		r.deviceInfo = append(r.deviceInfo, SyncDeviceInfo{
			Name:      e.Name,
			Device:    deviceLabelFromName(e.Name),
			Bytes:     e.Bytes,
			UpdatedAt: e.Modified,
			Self:      e.Name == r.ownName,
		})
		if e.Name != r.ownName {
			r.peers = append(r.peers, e.Name)
		}
	}
	if len(r.deviceInfo) == 0 {
		r.deviceInfo = append(r.deviceInfo, SyncDeviceInfo{Name: r.ownName, Device: hostName(), Self: true})
	}
	// 没有对端包时才去体检隔壁目录：这是"两台各写各的"最直接的证据，
	// 而且要多花几次 PROPFIND —— 有对端时就不必了。
	if len(r.peers) == 0 {
		r.siblingHint = r.cfg.siblingHint()
	}
	return nil
}

// ---- 2. pull ------------------------------------------------------------

// pull fetches every peer package with bounded parallelism.
func (r *syncRun) pull() error {
	cfg := r.cfg
	results := make([]syncPeerResult, len(r.peers))
	sem := make(chan struct{}, syncPeerConcurrency)
	var wg sync.WaitGroup
	for i, name := range r.peers {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data, ok, err := cfg.davGet(syncDevicesDir + "/" + name)
			if err != nil {
				results[i] = syncPeerResult{name: name, err: err}
				return
			}
			if !ok {
				return
			}
			pkg, err := parsePackage(data)
			results[i] = syncPeerResult{name: name, pkg: pkg, err: err}
		}(i, name)
	}
	wg.Wait()
	r.results = results
	return nil
}

// ---- 3. merge -----------------------------------------------------------

// merge applies every peer package. 单个对端读不动（半截上传、旧布局、解析失败）
// 只是**跳过它**：以前这里 return err，于是那一轮既不合并其他设备、也不上传自己
// 的包，"一台坏包 = 这台机器永远同步不了"。并集语义下跳过是安全的——最多晚一轮。
func (r *syncRun) merge() error {
	for _, res := range r.results {
		if res.err != nil {
			r.peerFails = append(r.peerFails, fmt.Sprintf("%s（%v）", res.name, res.err))
			Logf(r.svc.store.Dir(), "sync: peer %s unreadable, skipped this round: %v", res.name, res.err)
			continue
		}
		if res.pkg == nil {
			continue
		}
		stats := r.mergePeer(res)
		r.recordPeer(res, stats)
	}
	return nil
}

// mergePeer applies one peer package: tombstones first, then entries (and blobs).
func (r *syncRun) mergePeer(res syncPeerResult) syncPeerStats {
	s := r.svc
	var st syncPeerStats

	r.peerItems += len(res.pkg.index.Entries)
	for _, mi := range res.pkg.index.Entries {
		if mi.Hash == "" {
			continue
		}
		if mi.Blob != "" {
			// 键要和 buildOwnPackage 查的一致（`entry-<hash><ext>`）：少了前缀
			// 就永远查不到，于是每次同步都把对方的附件重传一遍。
			r.peerHasBlob["entry-"+mi.Hash+mi.Blob] = true
		}
	}
	r.peerTombstones = append(r.peerTombstones, res.pkg.index.Deleted...)

	if p := res.pkg.index.Settings; p != nil {
		if r.newestSettings == nil || p.UpdatedAt.After(r.newestSettings.UpdatedAt) {
			r.newestSettings = p
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
				st.deleted++
			}
		}
	}

	for _, remote := range res.pkg.entries {
		if remote.Hash == "" {
			continue
		}
		if at, killed := tombIdx[remote.Hash]; killed && !remote.UpdatedAt.After(at) {
			continue
		}
		if local, ok := s.store.findEntryByHash(remote.Hash); ok {
			// 同一内容在两端各被采集过一次：两条行的 hash 相同、id 不同。
			// 按远端 id upsert 会插出第二条重复卡片（而且此后每轮同步都可能再加一条），
			// 所以这里沿用本地 id —— 内容相同，本来就该是同一条。
			remote.ID = local.ID
			if !remote.UpdatedAt.After(local.UpdatedAt) {
				// 只把置顶这类"或"语义的标记并过来
				if remote.Pinned && !local.Pinned {
					local.Pinned = true
					if err := s.store.mergeEntry(local); err == nil {
						st.updated++
					}
				}
				continue
			}
			if err := s.store.mergeEntry(remote); err == nil {
				st.updated++
			}
		} else {
			if err := s.store.mergeEntry(remote); err == nil {
				st.added++
			} else {
				Logf(s.store.Dir(), "merge entry %s: %v", remote.Hash, err)
			}
		}
		// 附件：图片靠正文里的 memoimg 令牌（内容寻址，天然对得上）；
		// 私有格式没有令牌，按采集哈希挂在同一个键下（见 entryBlobKey）。
		if key := entryBlobKey(remote); key != "" {
			ext := entryBlobExt(remote)
			if payload, ok := res.pkg.blobs["entry-"+remote.Hash+ext]; ok {
				if err := s.store.SaveEntryBlobAs(key, payload, strings.TrimPrefix(ext, ".")); err != nil {
					Logf(s.store.Dir(), "restore entry blob %s: %v", remote.Hash, err)
				}
			}
		}
	}

	r.pulledNew += st.added
	r.pulledUpdated += st.updated
	r.deletedLocal += st.deleted
	return st
}

// recordPeer writes one peer's numbers into the device list right away (the old
// code deferred this to the end of the function, which mixed up the counters).
func (r *syncRun) recordPeer(res syncPeerResult, st syncPeerStats) {
	for i := range r.deviceInfo {
		if r.deviceInfo[i].Name != res.name {
			continue
		}
		r.deviceInfo[i].Items = len(res.pkg.index.Entries)
		r.deviceInfo[i].PulledNew = st.added
		r.deviceInfo[i].PulledUpdated = st.updated
		r.deviceInfo[i].Deleted = st.deleted
	}
}

// ---- 4. settings --------------------------------------------------------

// adoptSettings takes the newest peer settings when they beat ours.
// 机器本地字段由 keepLocalSettings 换回本机值，见 sync_merge.go 的 localSettingsFields。
func (r *syncRun) adoptSettings() (pulled, pushed bool, err error) {
	s := r.svc
	cur := s.store.Settings()
	if !cur.SyncSettings {
		return false, false, nil
	}
	localStamp := cur.SyncUpdatedAt
	if r.newestSettings != nil && r.newestSettings.UpdatedAt.After(localStamp) {
		merged := keepLocalSettings(r.newestSettings.Values, cur)
		merged.SyncUpdatedAt = r.newestSettings.UpdatedAt
		if err := s.store.SaveSettings(merged); err != nil {
			return false, false, err
		}
		Logf(s.store.Dir(), "sync: adopted settings from %s", r.newestSettings.Device)
		s.emit("settings:changed", merged)
		return true, false, nil
	}
	if r.newestSettings == nil || localStamp.After(r.newestSettings.UpdatedAt) {
		return false, true, nil
	}
	return false, false, nil
}

// ---- 5. push ------------------------------------------------------------

// push uploads our package unless nothing changed since the last push.
func (r *syncRun) push(upload bool) (syncPushInfo, error) {
	s := r.svc
	var info syncPushInfo

	// 判定"本地为空"要用全量：归档条目也要同步，否则"全都归档了"会被当成空库。
	if upload && s.store.CountAllEntries() == 0 && r.peerItems > 0 {
		// Our pull clearly did not work; publishing an empty package would make
		// this device look authoritative about having no history.
		Logf(s.store.Dir(), "sync: skip push, local history is empty but peers hold %d records", r.peerItems)
		info.note = "本地历史为空但对方有数据，已跳过上传"
		return info, nil
	}
	if !upload {
		return info, nil
	}

	// 用采纳过之后的设置：刚合并进来的远端设置如果不写进包，对端会一直把它当"更新"。
	//
	// 墓碑裁剪挪到组包**之前**：裁剪会改变墓碑集合，放在组包之后（老实现）会让
	// 下一轮的指纹不同 —— 于是每次都白推一个整包。放在前面则指纹如实反映最终内容。
	if n := s.store.PruneTombstones(time.Now().AddDate(0, 0, -tombstoneRetentionDays)); n > 0 {
		Logf(s.store.Dir(), "sync: pruned %d expired tombstones", n)
	}
	payload, fingerprint, stats, err := s.buildOwnPackage(r.peerHasBlob, r.peerTombstones, s.store.Settings())
	if err != nil {
		return info, err
	}
	r.blobsSent, r.blobsCap, r.blobsMissing = stats.sent, stats.capped, stats.missing
	r.blobsOversize = stats.oversize
	info.bytes = int64(len(payload))

	target := syncDevicesDir + "/" + r.ownName
	if fingerprint == s.store.Meta("sync.lastPush") {
		// Nothing changed since the last successful push: skip the upload
		// entirely, so an idle 30-minute tick costs one PROPFIND.
		info.note = "内容未变化，已跳过上传"
		Logf(s.store.Dir(), "sync: package unchanged (%d KiB), push skipped", len(payload)/1024)
		return info, nil
	}
	if resolved, err := r.cfg.urlFor(target); err == nil {
		Logf(s.store.Dir(), "sync: PUT %s", resolved)
	}
	if err := r.cfg.davPut(target, "application/zip", payload); err != nil {
		return info, err
	}
	_ = s.store.SetMeta("sync.lastPush", fingerprint)
	info.pushed = true
	Logf(s.store.Dir(), "sync: pushed %s (%d KiB)", target, len(payload)/1024)
	return info, nil
}

// ---- 6. report ----------------------------------------------------------

func (r *syncRun) report(info syncPushInfo, settingsPulled, settingsPushed bool) {
	s := r.svc

	result := fmt.Sprintf("拉取 %d 条新增 / %d 条更新", r.pulledNew, r.pulledUpdated)
	if r.deletedLocal > 0 {
		result += fmt.Sprintf("，删除 %d 条", r.deletedLocal)
	}
	if info.bytes > 0 {
		result += fmt.Sprintf("，本机包 %d KiB", info.bytes/1024)
	}
	if settingsPulled {
		result += "，已同步远端设置"
	}
	if settingsPushed {
		result += "，已上传本机设置"
	}
	if len(r.peers) > 0 {
		result += fmt.Sprintf("（%d 台设备）", len(r.peers)+1)
	} else {
		// 只有本机一个包时，"拉取 0 条"看起来像"一切正常"，其实是**没有对方可拉**。
		// 这句是给"另一台机器怎么没同步过来"直接给答案的。
		result += "（远端只有本机一个包：其他设备还没上传过）"
	}
	// 以下是"看起来成功、其实没同步到"的三种具体线索，必须出现在结果行里，
	// 否则用户只看到一句 sync ok。
	if len(r.peerFails) > 0 {
		result += fmt.Sprintf("，%d 个对端包读取失败已跳过（%s）", len(r.peerFails), strings.Join(r.peerFails, "；"))
	}
	if len(r.ignoredRemote) > 0 {
		result += fmt.Sprintf("，远端有 %d 个无法识别的文件（%s）", len(r.ignoredRemote), strings.Join(r.ignoredRemote, "、"))
	}
	if r.blobsMissing > 0 {
		result += fmt.Sprintf("，%d 个附件本机已丢失（对端也拿不到，建议重新采集）", r.blobsMissing)
	}
	if r.blobsCap > 0 {
		result += fmt.Sprintf("，%d 个附件超出本轮预算未上传（下轮优先补发）", r.blobsCap)
	}
	if r.blobsOversize > 0 {
		result += fmt.Sprintf("，%d 个附件单个就超过单包预算（%d MiB），永远发不出去", r.blobsOversize, syncBlobBudget>>20)
	}
	if r.siblingHint != "" {
		result += "；" + r.siblingHint
	}

	s.syncMu.Lock()
	s.syncStatus.LastSync = time.Now().Format(time.RFC3339)
	s.syncStatus.LastResult = result
	s.syncStatus.LastError = ""
	s.syncStatus.RemoteItems = r.peerItems
	s.syncStatus.Devices = len(r.peers) + 1
	s.syncStatus.LastBytes = info.bytes
	s.syncMu.Unlock()
	// 落库："上次同步"以前只活在内存里，重启后设置页就变回「从未同步」/1970 —— 明明刚同步过。
	s.saveSyncStatus()

	if info.pushed {
		for i := range r.deviceInfo {
			if r.deviceInfo[i].Self {
				r.deviceInfo[i].Bytes = info.bytes
				r.deviceInfo[i].Items = s.store.CountEntries()
				r.deviceInfo[i].UpdatedAt = time.Now()
			}
		}
	}

	s.saveSyncDetail(SyncDetail{
		At:            time.Now(),
		DurationMs:    time.Since(r.startedAt).Milliseconds(),
		PulledNew:     r.pulledNew,
		PulledUpdated: r.pulledUpdated,
		DeletedLocal:  r.deletedLocal,
		Pushed:        info.pushed,
		PushedBytes:   info.bytes,
		SettingsPull:  settingsPulled,
		SettingsPush:  settingsPushed,
		Note:          info.note,
	}, r.deviceInfo)

	Logf(s.store.Dir(), "sync ok: %s", result)
	s.emit("clip:stats", s.store.Stats())
}

