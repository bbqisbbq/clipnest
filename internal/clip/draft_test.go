package clip

import (
	"strings"
	"testing"
	"time"
)

// 草稿要能"重启后还在"——所以它必须真的落进库里，而不是留在内存或前端状态里。
//
// 2.8.2 起写库是防抖的（前端每敲一个字送过来，后端合并成一次 SQLite 写），
// 所以"落库"有三个入口，三个都要测：
//   1. 静默期到了自己写（draftFlushDelay）；
//   2. 前端在收起窗口/退出前显式 Flush；
//   3. Shutdown 时兜底再写一次（强杀前唯一能保证的那一步）。
func TestComposerDraftPersists(t *testing.T) {
	dir := t.TempDir()
	svc := newTestService(t, dir)

	if got := svc.GetComposerDraft(); got != "" {
		t.Fatalf("初始草稿应为空，得到 %q", got)
	}

	if err := svc.SetComposerDraft("# 想到一半\n- [ ] 明天接着"); err != nil {
		t.Fatalf("SetComposerDraft: %v", err)
	}

	// 内存里立刻可见（同一进程的读者不该读到旧值）
	if got := svc.GetComposerDraft(); !strings.Contains(got, "明天接着") {
		t.Errorf("内存里的草稿没更新：%q", got)
	}

	// 显式 Flush 之后换一个实例读同一个目录 = 重启后的样子
	if err := svc.FlushComposerDraft(); err != nil {
		t.Fatalf("FlushComposerDraft: %v", err)
	}
	again := newTestService(t, dir)
	if got := again.GetComposerDraft(); !strings.Contains(got, "明天接着") {
		t.Errorf("flush 后重启丢了草稿：%q", got)
	}

	// 存完条目清空草稿：重启不该把已经保存过的内容又塞回输入框
	if err := again.SetComposerDraft(""); err != nil {
		t.Fatalf("清空草稿: %v", err)
	}
	if err := again.FlushComposerDraft(); err != nil {
		t.Fatalf("清空后 flush: %v", err)
	}
	third := newTestService(t, dir)
	if got := third.GetComposerDraft(); got != "" {
		t.Errorf("清空后仍有草稿：%q", got)
	}
}

// 不显式 Flush 也不 Shutdown 时，静默期到了就该自己落库。
func TestComposerDraftFlushesAfterDelay(t *testing.T) {
	dir := t.TempDir()
	svc := newTestService(t, dir)
	if err := svc.SetComposerDraft("静默期之后应该自己写"); err != nil {
		t.Fatal(err)
	}
	// 防抖窗口 400ms，留一倍余量
	time.Sleep(900 * time.Millisecond)
	again := newTestService(t, dir)
	if got := again.GetComposerDraft(); !strings.Contains(got, "静默期") {
		t.Errorf("静默期后没落库：%q", got)
	}
}

// 正常退出（托盘「退出」/ 关闭应用）走 Shutdown：没 flush 过的草稿也要保住。
func TestComposerDraftFlushesOnShutdown(t *testing.T) {
	dir := t.TempDir()
	svc := newTestService(t, dir)
	if err := svc.SetComposerDraft("退出前最后几个字"); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown()

	again := newTestService(t, dir)
	if got := again.GetComposerDraft(); !strings.Contains(got, "最后几个字") {
		t.Errorf("Shutdown 没把草稿写下去：%q", got)
	}
}

// 只有空白的草稿等价于没有草稿（用户敲了几个空格然后关窗口，不该被记住）。
func TestComposerDraftBlankClears(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	if err := svc.SetComposerDraft("有内容"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetComposerDraft("   \n\t "); err != nil {
		t.Fatal(err)
	}
	if got := svc.GetComposerDraft(); got != "" {
		t.Errorf("空白草稿应被清空，得到 %q", got)
	}
}

// 超长草稿截断而不是整条丢弃。
func TestComposerDraftTruncated(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	long := strings.Repeat("字", maxComposerDraftRunes+50)
	if err := svc.SetComposerDraft(long); err != nil {
		t.Fatal(err)
	}
	got := svc.GetComposerDraft()
	if n := len([]rune(got)); n != maxComposerDraftRunes {
		t.Errorf("截断后长度 = %d，期望 %d", n, maxComposerDraftRunes)
	}
}
