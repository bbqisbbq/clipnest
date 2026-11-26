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
//  1. 静默期到了自己写（draftFlushDelay）；
//  2. 前端在收起窗口/退出前显式 Flush；
//  3. Shutdown 时兜底再写一次（强杀前唯一能保证的那一步）。
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

// 「记一笔」浮层的草稿：那个窗口闲置会被销毁（idle.go），页面连同 React state
// 一起没了 —— 库里的这一份是它唯一活下来的可能。
//
// 2.12.5 及以前这条完全没有：Capture.tsx 把正文放在自己的 useState 里，不打字就
// 关窗、或者闲置 1.5 秒被销毁，草稿就地蒸发（实测日志里
// `idle: capture window destroyed` 一次接一次）。所以这里测的不是"锦上添花"，
// 而是"关一次窗不再丢一次稿"。
func TestCaptureDraftPersists(t *testing.T) {
	dir := t.TempDir()
	svc := newTestService(t, dir)

	if got := svc.GetCaptureDraft(); got != "" {
		t.Fatalf("初始草稿应为空，得到 %q", got)
	}
	if err := svc.SetCaptureDraft("记一笔里打的半段话\n- [ ] 明天接着"); err != nil {
		t.Fatalf("SetCaptureDraft: %v", err)
	}
	if got := svc.GetCaptureDraft(); !strings.Contains(got, "半段话") {
		t.Errorf("内存里的草稿没更新：%q", got)
	}
	if err := svc.FlushCaptureDraft(); err != nil {
		t.Fatalf("FlushCaptureDraft: %v", err)
	}

	// 换实例读同一目录 = 窗口被销毁后重新打开的样子
	again := newTestService(t, dir)
	if got := again.GetCaptureDraft(); !strings.Contains(got, "明天接着") {
		t.Errorf("重开后丢了「记一笔」草稿：%q", got)
	}
	// 存成条目后要能清干净，否则下次开窗又冒出已经保存过的内容
	if err := again.SetCaptureDraft(""); err != nil {
		t.Fatalf("清空草稿: %v", err)
	}
	if err := again.FlushCaptureDraft(); err != nil {
		t.Fatalf("清空后 flush: %v", err)
	}
	if got := newTestService(t, dir).GetCaptureDraft(); got != "" {
		t.Errorf("清空后仍有草稿：%q", got)
	}
}

// 不 Flush 也不 Shutdown 时，静默期到了就该自己落库（防抖在 Go 侧，所以窗口
// 被销毁这件事不影响它 —— 定时器活在进程里，不活在页面里）。
func TestCaptureDraftFlushesAfterDelay(t *testing.T) {
	dir := t.TempDir()
	svc := newTestService(t, dir)
	if err := svc.SetCaptureDraft("静默期之后应该自己写"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(900 * time.Millisecond)
	if got := newTestService(t, dir).GetCaptureDraft(); !strings.Contains(got, "静默期") {
		t.Errorf("静默期后没落库：%q", got)
	}
}

// Shutdown 要把两个槽都写下去，别只记得 composer。
func TestCaptureDraftFlushesOnShutdown(t *testing.T) {
	dir := t.TempDir()
	svc := newTestService(t, dir)
	if err := svc.SetCaptureDraft("退出前最后几个字（记一笔）"); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown()
	if got := newTestService(t, dir).GetCaptureDraft(); !strings.Contains(got, "记一笔") {
		t.Errorf("Shutdown 没把「记一笔」草稿写下去：%q", got)
	}
}

// 两份草稿必须互不串台：两个窗口可以同时开着，共用一把键会互相覆盖。
func TestDraftsAreIndependent(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	if err := svc.SetComposerDraft("主窗口写的"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetCaptureDraft("浮层写的"); err != nil {
		t.Fatal(err)
	}
	if got := svc.GetComposerDraft(); got != "主窗口写的" {
		t.Errorf("主窗口草稿被污染：%q", got)
	}
	if got := svc.GetCaptureDraft(); got != "浮层写的" {
		t.Errorf("浮层草稿被污染：%q", got)
	}
	// 清掉一个不该影响另一个
	if err := svc.SetCaptureDraft(""); err != nil {
		t.Fatal(err)
	}
	if got := svc.GetComposerDraft(); got != "主窗口写的" {
		t.Errorf("清空浮层草稿时把主窗口的也清了：%q", got)
	}
}
