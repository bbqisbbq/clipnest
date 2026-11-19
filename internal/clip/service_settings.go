package clip

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbqisbbq/clipnest/internal/expand"
)

// 设置的读写与副作用：保存后应用热键与展开器、开机自启的对账与修复，
// 以及同步/更新循环的重启（间隔或开关一变就立即生效）。
// 不负责：设置字段本身（settings.go）、同步逻辑（sync_*.go）。

// GetSettings returns the current settings.
func (s *ClipService) GetSettings() Settings { return s.store.Settings() }

// SaveSettings persists settings, applies side effects and returns what was
// actually stored.
func (s *ClipService) SaveSettings(st Settings) (Settings, error) {
	prev := s.store.Settings()
	if st.Hotkey == "" {
		st.Hotkey = prev.Hotkey
	}
	// 可同步的字段变了才 bump 时间戳——否则设置永远同步不出去，
	// 或者每次保存都无谓地覆盖别的机器。
	if shareableStamp(st) != shareableStamp(prev) {
		st.SyncUpdatedAt = nowUTC()
	}
	if err := s.store.SaveSettings(st); err != nil {
		return prev, err
	}
	if prev.AutoStart != st.AutoStart {
		if err := setAutoStart(st.AutoStart, s.hooks.ExePath); err != nil {
			return s.store.Settings(), fmt.Errorf("设置开机启动失败: %w", err)
		}
	}
	if s.hooks.OnSettings != nil {
		if err := s.hooks.OnSettings(st); err != nil {
			return s.store.Settings(), err
		}
	}
	// 两个后台循环都是"重启式"：syncLoop 内部取消上一轮，更新检查走 restartUpdateCheck。
	// 之前这里是 `go s.updateCheckLoop()`，保存几次就多几个待执行的检查。
	go s.syncLoop()
	s.restartUpdateCheck()
	s.refreshTriggers()
	s.applyExpander(s.store.Settings())
	s.emit("settings:changed", st)
	s.emit("clip:stats", s.store.Stats())
	return s.store.Settings(), nil
}

// applyExpander pushes trigger settings into the expansion engine.
func (s *ClipService) applyExpander(st Settings) {
	suffix := strings.TrimSpace(st.ExpandSuffix)
	if suffix == "" {
		suffix = "-"
	}
	endChars := st.ExpandEndChars
	if endChars == "" {
		endChars = DefaultEndChars
	}
	err := s.engine.Configure(expand.Config{
		Enabled:   st.ExpandEnabled,
		Suffix:    suffix,
		EndChars:  endChars,
		Debug:     false,
		IdleReset: 2 * time.Second,
		Logf:      func(format string, args ...any) { Logf(s.store.Dir(), format, args...) },
	})
	if err != nil {
		Logf(s.store.Dir(), "expander start failed: %v", err)
		return
	}
	if st.ExpandEnabled {
		Logf(s.store.Dir(), "expander enabled (suffix=%q endChars=%q, %d triggers)",
			suffix, endChars, s.triggerCount())
	}
}

// triggerCount reports how many triggers are loaded (for diagnostics).
func (s *ClipService) triggerCount() int {
	trigMu.RLock()
	defer trigMu.RUnlock()
	return len(trigIndex.folded)
}

// SetCapture is the tray's pause/resume switch.
func (s *ClipService) SetCapture(enabled bool) (Settings, error) {
	st := s.store.Settings()
	st.Capture = enabled
	return s.SaveSettings(st)
}

// SetAutoStart toggles the logon entry (tray menu / settings both use it).
func (s *ClipService) SetAutoStart(enabled bool) (Settings, error) {
	st := s.store.Settings()
	st.AutoStart = enabled
	return s.SaveSettings(st)
}

// IsAutoStartEnabled reports the actual registry state, not just the setting.
func (s *ClipService) IsAutoStartEnabled() bool {
	if !s.hooks.PlatformWorks {
		return false
	}
	return autoStartEnabled()
}

// ReconcileAutoStart makes the Run registry entry match the stored setting,
// repairing a stale path after the app was moved or reinstalled. Called once at
// startup so a moved installation doesn't silently break autostart.
//
// A pre-existing entry that ClipNest did not write itself (for example, the one
// the installer creates when the user ticks "开机自动启动") is adopted rather
// than deleted: the installer's choice wins on first run. An entry pointing at a
// binary that no longer exists (a moved or deleted build) is *repaired*, never
// adopted — adopting a dead path is how a stale debug build keeps hijacking
// logon.
func (s *ClipService) ReconcileAutoStart() {
	if !s.hooks.PlatformWorks || s.hooks.ExePath == "" {
		return
	}
	st := s.store.Settings()
	current := autoStartCommandValue()
	registered := exePathFromCommand(current)
	registeredOK := registered != "" && fileExists(registered)
	self := filepath.Clean(s.hooks.ExePath)
	sameExe := registered != "" && strings.EqualFold(filepath.Clean(registered), self)

	Logf(s.store.Dir(), "autostart: registry=%q (exists=%v) settings=%v exe=%s",
		current, registeredOK, st.AutoStart, self)

	register := func(reason string) {
		if err := setAutoStart(true, s.hooks.ExePath); err != nil {
			Logf(s.store.Dir(), "autostart reconcile failed (%s): %v", reason, err)
			return
		}
		Logf(s.store.Dir(), "autostart registered (%s): %s", reason, autoStartCommand(s.hooks.ExePath))
	}

	switch {
	case current == "":
		if st.AutoStart {
			register("missing entry")
		}

	case !registeredOK:
		// Dead path: repoint if the user wants autostart, otherwise clean up.
		if st.AutoStart {
			register("stale path " + registered)
		} else if err := setAutoStart(false, ""); err == nil {
			Logf(s.store.Dir(), "autostart removed stale entry: %q", current)
		}

	case st.AutoStart && !sameExe:
		register("points at another build")

	case !st.AutoStart:
		// Adopt the installer's choice.
		st.AutoStart = true
		if err := s.store.SaveSettings(st); err != nil {
			Logf(s.store.Dir(), "autostart adopt failed: %v", err)
			return
		}
		Logf(s.store.Dir(), "autostart adopted from registry: %q", current)
		s.emit("settings:changed", st)
	}
}

// exePathFromCommand pulls the executable path out of a Run value like
// `"C:\Program Files\X\app.exe" --background`.
func exePathFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.Index(cmd[1:], `"`); end >= 0 {
			return cmd[1 : end+1]
		}
		return strings.TrimPrefix(cmd, `"`)
	}
	if i := strings.Index(strings.ToLower(cmd), ".exe"); i >= 0 {
		return cmd[:i+len(".exe")]
	}
	return ""
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := osStat(path)
	return err == nil
}

// AutoStartTarget is the command line that would be registered, for the UI to
// show what "开机自启" actually points at.
func (s *ClipService) AutoStartTarget() string {
	if cmd := autoStartCommandValue(); cmd != "" {
		return cmd
	}
	if s.hooks.ExePath == "" {
		return ""
	}
	return autoStartCommand(s.hooks.ExePath)
}
