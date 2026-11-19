package clip

import (
	"encoding/json"
	"sort"
	"time"
)

// 合并规则里只看字段、与整包无关的部分：设置字段的机器本地裁剪（同步出去的副本与
// 变更戳），以及墓碑并集（同一 hash 取最新时间、总量设上限）。
// 不负责：条目合并的流程与写入存储，那条线在 sync_run.go。

// ---- settings exchange --------------------------------------------------

// localField 是一台机器自己的设置项：既不能进同步包，也不能被对端覆盖。
//
// 以前这两件事各有独立一份清单（下面的 machineOnlyFields 与 sync_run.go 里逐字段
// 拷回），新增一个机器本地字段要记得改两处——漏一处就会把对端的同步凭据、热键或
// 自启写进本机。现在只有下面这一张表，两个方向都从它派生。
type localField struct {
	clear func(*Settings)          // 发出去之前清零（不进包 / 不参与"内容是否变了"的比较）
	keep  func(dst, src *Settings) // 采纳对端设置时，用本机值覆盖回来
}

var localSettingsFields = []localField{
	{func(s *Settings) { s.SyncEnabled = false }, func(d, src *Settings) { d.SyncEnabled = src.SyncEnabled }},
	{func(s *Settings) { s.SyncURL = "" }, func(d, src *Settings) { d.SyncURL = src.SyncURL }},
	{func(s *Settings) { s.SyncUser = "" }, func(d, src *Settings) { d.SyncUser = src.SyncUser }},
	{func(s *Settings) { s.SyncPass = "" }, func(d, src *Settings) { d.SyncPass = src.SyncPass }},
	{func(s *Settings) { s.SyncIntervalMin = 0 }, func(d, src *Settings) { d.SyncIntervalMin = src.SyncIntervalMin }},
	{func(s *Settings) { s.SyncDeviceID = "" }, func(d, src *Settings) { d.SyncDeviceID = src.SyncDeviceID }},
	{func(s *Settings) { s.AutoStart = false }, func(d, src *Settings) { d.AutoStart = src.AutoStart }},
	{func(s *Settings) { s.ShowOnLaunch = false }, func(d, src *Settings) { d.ShowOnLaunch = src.ShowOnLaunch }},
	{func(s *Settings) { s.SyncSettings = false }, func(d, src *Settings) { d.SyncSettings = src.SyncSettings }},
	// SyncUpdatedAt 是"谁更新"的比较戳：发出去要清零，采纳时由调用方按对端戳重设，
	// 所以 keep 方向故意不拷（拷了会被随后的赋值覆盖，只会让人误会）。
	{func(s *Settings) { s.SyncUpdatedAt = time.Time{} }, func(d, src *Settings) {}},
}

// machineOnlyFields are settings that describe this machine or its connection to
// the sync server; a peer never overwrites them.
func machineOnlyFields(st Settings) Settings {
	for _, f := range localSettingsFields {
		f.clear(&st)
	}
	return st
}

// keepLocalSettings starts from a peer's values and puts back every machine-local
// field, so winning a merge can never import another machine's hotkeys,
// auto-start flag or sync credentials.
func keepLocalSettings(remote, local Settings) Settings {
	out := remote
	for _, f := range localSettingsFields {
		f.keep(&out, &local)
	}
	return out
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
