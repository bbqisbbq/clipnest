package clip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这一组用例锁住 2026-11-23 那轮同步加固的行为：
//
//  1. PROPFIND 必须带 getcontentlength/getlastmodified（否则设备表"包大小 0 B"）
//  2. 一个坏对端包不能毒死整轮同步（既不能拦合并，也不能拦上传）
//  3. MKCOL 遇 409 不能无限递归
//  4. 同级目录里另一个 clipnest_data 要被点出来（"两边地址填得不一样"）
//  5. 删除必须留墓碑（删了没墓碑 ⇒ 对端会把内容再传回来）
//  6. 失败退避的节奏

func TestSyncReportsRemotePackageSizeAndTime(t *testing.T) {
	base, dir, _ := newTestDAV(t)
	a := syncedService(t, base)
	if _, _, err := a.store.SaveCapture(Captured{Kind: KindText, Text: "sized", Preview: "sized", Hash: "sized-h", Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	files, err := os.ReadDir(filepath.Join(string(*dir), syncDevicesDir))
	if err != nil || len(files) != 1 {
		t.Fatalf("want 1 package, got %v (%v)", names(files), err)
	}
	info, err := files[0].Info()
	if err != nil {
		t.Fatal(err)
	}

	b := syncedService(t, base)
	panel := b.GetSyncPanel()
	if len(panel.Devices) == 0 {
		t.Fatal("panel lists no devices")
	}
	var peer *SyncDeviceInfo
	for i := range panel.Devices {
		if !panel.Devices[i].Self {
			peer = &panel.Devices[i]
		}
	}
	if peer == nil {
		t.Fatal("panel does not list the peer package")
	}
	// PROPFIND 里如果不请求 getcontentlength/getlastmodified，坚果云只回
	// resourcetype，这里就会是 0 B / 零时间 —— 正是排查"对方到底有没有上传"最需要的两列。
	if peer.Bytes != info.Size() {
		t.Errorf("peer package size = %d, want %d（PROPFIND 必须请求 getcontentlength）", peer.Bytes, info.Size())
	}
	if peer.UpdatedAt.IsZero() {
		t.Error("peer package has no server-reported mtime（PROPFIND 必须请求 getlastmodified）")
	}
}

func TestSyncSurvivesBrokenPeerPackage(t *testing.T) {
	base, dir, _ := newTestDAV(t)
	a := syncedService(t, base)
	if _, _, err := a.store.SaveCapture(Captured{Kind: KindText, Text: "survivor", Preview: "survivor", Hash: "survivor-h", Size: 8}); err != nil {
		t.Fatal(err)
	}

	// 一个半截上传 / 旧布局留下的坏包：解不开的 zip。
	devices := filepath.Join(string(*dir), syncDevicesDir)
	if err := os.MkdirAll(devices, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devices, "zzz-broken.zip"), []byte("not a zip at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := a.SyncNow()
	if err != nil {
		t.Fatalf("一个坏包不该让整轮同步失败: %v", err)
	}
	if !strings.Contains(status.LastResult, "读取失败") {
		t.Errorf("结果行应该提到读不动的对端包: %q", status.LastResult)
	}
	if status.LastError != "" {
		t.Errorf("坏包属于警告、不是本轮失败: %q", status.LastError)
	}
	// 关键：本机包照样要传上去（老实现这里 return err，于是这台机器永远不上传）。
	if _, err := os.Stat(filepath.Join(devices, a.syncConfigForTest(t).packageName())); err != nil {
		t.Errorf("本机包没有上传: %v", err)
	}
}

func TestSyncMkcolConflictDoesNotRecurse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MKCOL" {
			// 有些服务器对"已存在"或"上级不存在"回 409 而不是 405。
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
	}))
	defer srv.Close()

	cfg := syncConfig{base: srv.URL + "/dav/", ctx: context.Background()}
	done := make(chan error, 1)
	go func() { done <- cfg.mkcol(syncDevicesDir) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("409 应该报错，而不是被当成成功")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mkcol 卡住了：409 分支在 . 上无限递归")
	}
}

func TestSyncPointsAtSiblingDataDir(t *testing.T) {
	base, dir, _ := newTestDAV(t)
	// 另一台机器把「服务器地址」填成了 …/dav/我的坚果云，于是包落在隔壁。
	sibling := filepath.Join(string(*dir), "我的坚果云", syncDevicesDir)
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "aaaa1111-home_pc.zip"), []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := syncedService(t, base)
	msg, err := svc.TestSync()
	if err != nil {
		t.Fatalf("TestSync: %v", err)
	}
	if !strings.Contains(msg, "我的坚果云") {
		t.Errorf("测试连接应该指出隔壁目录里的同步包（地址填错一层）: %q", msg)
	}

	// 同步结果行同样要带上这条线索，否则用户只看到一句 sync ok。
	status, err := svc.SyncNow()
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if !strings.Contains(status.LastResult, "我的坚果云") {
		t.Errorf("结果行应该点出隔壁目录: %q", status.LastResult)
	}
}

func TestDeleteEntryLeavesTombstone(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	e, _, err := svc.store.SaveCapture(Captured{Kind: KindText, Text: "delete me", Preview: "delete me", Hash: "del-tomb-h", Size: 9})
	if err != nil {
		t.Fatal(err)
	}
	if !svc.store.DeleteEntry(e.ID) {
		t.Fatal("删除失败")
	}
	// 没墓碑 ⇒ 对端不会跟着删，而且会把这条再传回来（"删了又回来"）。
	tombs := svc.store.TombstonesScoped("entry", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), 10)
	if len(tombs) != 1 || tombs[0].Hash != "del-tomb-h" {
		t.Fatalf("删除没有留下墓碑: %+v", tombs)
	}
}

func TestSyncRetryBacksOff(t *testing.T) {
	interval := 30 * time.Minute
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, interval},
		{1, time.Minute},
		{2, 3 * time.Minute},
		{3, 10 * time.Minute},
		{4, interval}, // 退避用完了，回到正常节奏
	}
	for _, c := range cases {
		if got := nextDelay(interval, c.failures); got != c.want {
			t.Errorf("nextDelay(interval, %d) = %v, want %v", c.failures, got, c.want)
		}
	}
	// 间隔本身比退避还短时，不要因为"退避"反而变慢。
	if got := nextDelay(time.Minute, 2); got != time.Minute {
		t.Errorf("退避不该超过 interval：%v", got)
	}

	// 刚同步过就不再跑第一枪（设置页自动保存会连着重启循环）。
	if d := firstPassDelay(time.Now().UTC().Format(time.RFC3339)); d < 50*time.Second {
		t.Errorf("刚同步完应该等够 60 秒，得到 %v", d)
	}
	if d := firstPassDelay(""); d != 5*time.Second {
		t.Errorf("没有历史时应该在 5 秒后首跑，得到 %v", d)
	}
	if d := firstPassDelay(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); d != 5*time.Second {
		t.Errorf("很久没同步时应该 5 秒后首跑，得到 %v", d)
	}
}

// syncConfigForTest is a tiny helper so the test above can ask for this
// service's package name without duplicating the device-id plumbing.
func (s *ClipService) syncConfigForTest(t *testing.T) syncConfig {
	t.Helper()
	cfg, err := s.syncConfig()
	if err != nil {
		t.Fatalf("syncConfig: %v", err)
	}
	return cfg
}
