package clip

import "strconv"

// "上次同步"的持久化键。
//
// 这些值以前只活在内存里（syncStatus），重启后设置页就显示「从未同步」加一个
// 1970 年的时间戳（用户看到的是 1/01/01 08:05:43）——明明刚同步成功过。
// 现在每次成功收尾写一份到 meta，服务启动时读回。
const (
	metaSyncLastAt     = "sync.lastAt"
	metaSyncLastResult = "sync.lastResult"
	metaSyncLastBytes  = "sync.lastBytes"
	metaSyncDevices    = "sync.devices"
)

// saveSyncStatus 把最近一次成功同步的摘要写进 meta。写库失败不连累同步流程。
func (s *ClipService) saveSyncStatus() {
	s.syncMu.Lock()
	st := s.syncStatus
	s.syncMu.Unlock()
	if st.LastSync == "" {
		return
	}
	_ = s.store.SetMeta(metaSyncLastAt, st.LastSync)
	_ = s.store.SetMeta(metaSyncLastResult, st.LastResult)
	_ = s.store.SetMeta(metaSyncLastBytes, strconv.FormatInt(st.LastBytes, 10))
	_ = s.store.SetMeta(metaSyncDevices, strconv.Itoa(st.Devices))
}

// loadSyncStatus 在服务启动时把 meta 里的"上次同步"读回内存。
func (s *ClipService) loadSyncStatus() {
	st := s.syncStatus
	st.LastSync = s.store.Meta(metaSyncLastAt)
	st.LastResult = s.store.Meta(metaSyncLastResult)
	if v, err := strconv.ParseInt(s.store.Meta(metaSyncLastBytes), 10, 64); err == nil {
		st.LastBytes = v
	}
	if v, err := strconv.Atoi(s.store.Meta(metaSyncDevices)); err == nil {
		st.Devices = v
	}
	s.syncMu.Lock()
	s.syncStatus = st
	s.syncMu.Unlock()
}
