package clip

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 快速输入窗的"半透明"只有一个旋钮：Settings.CaptureTransparency（窗口本身一直是
// 透明的，这个是卡片底色的 alpha）。它来自两处都不受控的输入 —— 设置页控件和
// settings.json（用户能手改、同步回来的旧包也可能带脏值），所以两边都要钳。

func TestCaptureTransparencyDefaultsOff(t *testing.T) {
	if got := DefaultSettings().CaptureTransparency; got != 0 {
		t.Fatalf("default CaptureTransparency = %d, want 0 (实心卡片是默认值)", got)
	}
}

func TestSaveSettingsClampsCaptureTransparency(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	for _, tc := range []struct {
		in   int
		want int
	}{
		{in: 30, want: 30},
		{in: -5, want: 0},
		{in: 999, want: MaxCaptureTransparency},
		{in: 0, want: 0},
	} {
		st := svc.store.Settings()
		st.CaptureTransparency = tc.in
		got, err := svc.SaveSettings(st)
		if err != nil {
			t.Fatalf("SaveSettings(captureTransparency=%d): %v", tc.in, err)
		}
		if got.CaptureTransparency != tc.want {
			t.Fatalf("SaveSettings(captureTransparency=%d) stored %d, want %d",
				tc.in, got.CaptureTransparency, tc.want)
		}
	}
}

// 手改 settings.json（或在另一台机器上写坏的包）不能让卡片透明到看不清字：
// loadSettings / PeekSettings 都要在读出时钳一次。
func TestLoadSettingsClampsCaptureTransparency(t *testing.T) {
	dir := t.TempDir()
	raw := map[string]any{}
	st := DefaultSettings()
	blob, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatalf("round-trip defaults: %v", err)
	}
	raw["captureTransparency"] = 250 // 超出 0–100，读出时必须被钳住
	blob, err = json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal dirty settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), blob, 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	peeked, err := PeekSettings(dir)
	if err != nil {
		t.Fatalf("PeekSettings: %v", err)
	}
	if peeked.CaptureTransparency != MaxCaptureTransparency {
		t.Fatalf("PeekSettings returned %d, want %d", peeked.CaptureTransparency, MaxCaptureTransparency)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := store.Settings().CaptureTransparency; got != MaxCaptureTransparency {
		t.Fatalf("loadSettings returned %d, want %d", got, MaxCaptureTransparency)
	}
}

// CaptureTransparency 是可同步的外观项（和 themePreset 一样）：它必须真的进同步包，
// 否则两台机器各调一次还得手改。
func TestCaptureTransparencyIsShareable(t *testing.T) {
	st := DefaultSettings()
	st.CaptureTransparency = 15
	stripped := machineOnlyFields(st)
	if stripped.CaptureTransparency != 15 {
		t.Fatalf("machineOnlyFields dropped CaptureTransparency (%d)", stripped.CaptureTransparency)
	}
}
