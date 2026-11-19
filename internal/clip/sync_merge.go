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
