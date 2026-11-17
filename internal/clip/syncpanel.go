package clip

import (
	"encoding/json"
	"strings"
	"time"
)

// Sync panel: a read-only view of "what is on the server" and "what the last run
// actually did". Everything here is diagnostic; no method in this file writes to
// the remote server except the PROPFIND needed to list packages.

// SyncDeviceInfo describes one package file in the remote directory.
type SyncDeviceInfo struct {
	Name string `json:"name"`
	// Device is the hostname part of the file name ("<deviceId>-<host>.zip").
	Device string `json:"device"`
	Self   bool   `json:"self"`
	Bytes  int64  `json:"bytes"`
	// UpdatedAt is the server-reported modification time of the package.
	UpdatedAt time.Time `json:"updatedAt"`
	// Items is how many records that package held when we last read it.
	Items int `json:"items"`
	// Per-run counters (only filled for the run that read this package).
	PulledNew     int `json:"pulledNew"`
	PulledUpdated int `json:"pulledUpdated"`
	Deleted       int `json:"deleted"`
}

// SyncDetail is the outcome of the most recent run, persisted so the panel shows
// something meaningful right after a restart.
type SyncDetail struct {
	At            time.Time `json:"at"`
	DurationMs    int64     `json:"durationMs"`
	PulledNew     int       `json:"pulledNew"`
	PulledUpdated int       `json:"pulledUpdated"`
	DeletedLocal  int       `json:"deletedLocal"`
	Pushed        bool      `json:"pushed"`
	PushedBytes   int64     `json:"pushedBytes"`
	Devices       int       `json:"devices"`
	SettingsPull  bool      `json:"settingsPulled"`
	SettingsPush  bool      `json:"settingsPushed"`
	Note          string    `json:"note,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// SyncPanel is what the settings page renders.
type SyncPanel struct {
	Configured bool             `json:"configured"`
	Message    string           `json:"message,omitempty"`
	DeviceID   string           `json:"deviceId"`
	Interval   int              `json:"intervalMin"`
	Devices    []SyncDeviceInfo `json:"devices"`
	Last       SyncDetail       `json:"last"`
}

const syncDetailKey = "sync.lastDetail"

func (s *ClipService) saveSyncDetail(d SyncDetail, devices []SyncDeviceInfo) {
	d.Devices = len(devices)
	payload, err := json.Marshal(struct {
		Detail  SyncDetail       `json:"detail"`
		Devices []SyncDeviceInfo `json:"devices"`
	}{Detail: d, Devices: devices})
	if err != nil {
		return
	}
	if err := s.store.SetMeta(syncDetailKey, string(payload)); err != nil {
		Logf(s.store.Dir(), "sync: could not persist panel data: %v", err)
	}
}

// GetSyncPanel builds the panel: last run from the local cache, device list from
// a fresh PROPFIND when sync is configured.
func (s *ClipService) GetSyncPanel() SyncPanel {
	st := s.store.Settings()
	panel := SyncPanel{
		DeviceID: st.SyncDeviceID,
		Interval: st.SyncIntervalMin,
		Devices:  []SyncDeviceInfo{},
	}
	if raw := s.store.Meta(syncDetailKey); raw != "" {
		var cached struct {
			Detail  SyncDetail       `json:"detail"`
			Devices []SyncDeviceInfo `json:"devices"`
		}
		if json.Unmarshal([]byte(raw), &cached) == nil {
			panel.Last = cached.Detail
			panel.Devices = cached.Devices
		}
	}

	cfg, err := s.syncConfig()
	if err != nil {
		panel.Message = err.Error()
		return panel
	}
	panel.Configured = true

	entries, err := cfg.listPackages(syncDevicesDir)
	if err != nil {
		panel.Message = err.Error()
		return panel
	}
	if len(entries) == 0 {
		panel.Devices = []SyncDeviceInfo{{Name: cfg.packageName(), Device: hostName(), Self: true}}
		return panel
	}

	// Fresh sizes/times from the server; per-run counters survive from the cache.
	prev := make(map[string]SyncDeviceInfo, len(panel.Devices))
	for _, d := range panel.Devices {
		prev[d.Name] = d
	}
	out := make([]SyncDeviceInfo, 0, len(entries))
	for _, e := range entries {
		info := SyncDeviceInfo{
			Name:      e.Name,
			Device:    deviceLabelFromName(e.Name),
			Bytes:     e.Bytes,
			UpdatedAt: e.Modified,
			Self:      e.Name == cfg.packageName(),
		}
		if old, ok := prev[e.Name]; ok {
			info.Items = old.Items
			info.PulledNew = old.PulledNew
			info.PulledUpdated = old.PulledUpdated
			info.Deleted = old.Deleted
		}
		out = append(out, info)
	}
	panel.Devices = out
	return panel
}

// deviceLabelFromName turns "1f2e3d4c5b6a-desktop_abcd.zip" into "desktop abcd".
func deviceLabelFromName(name string) string {
	base := strings.TrimSuffix(name, ".zip")
	if i := strings.Index(base, "-"); i >= 0 && i+1 < len(base) {
		base = base[i+1:]
	} else {
		return "未知设备"
	}
	return strings.ReplaceAll(base, "_", " ")
}
