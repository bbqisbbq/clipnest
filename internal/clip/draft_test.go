package clip

import (
	"strings"
	"testing"
)

// 草稿要能"重启后还在"——所以它必须真的落进库里，而不是留在内存或前端状态里。
func TestComposerDraftPersists(t *testing.T) {
	dir := t.TempDir()
	svc := newTestService(t, dir)

	if got := svc.GetComposerDraft(); got != "" {
		t.Fatalf("初始草稿应为空，得到 %q", got)
	}

	if err := svc.SetComposerDraft("# 想到一半\n- [ ] 明天接着"); err != nil {
		t.Fatalf("SetComposerDraft: %v", err)
	}

	// 换一个服务实例读同一个目录 = 重启后的样子
	again := newTestService(t, dir)
	if got := again.GetComposerDraft(); !strings.Contains(got, "明天接着") {
		t.Errorf("重启后草稿丢了：%q", got)
	}

	// 存完条目清空草稿：重启不该把已经保存过的内容又塞回输入框
	if err := again.SetComposerDraft(""); err != nil {
		t.Fatalf("清空草稿: %v", err)
	}
	third := newTestService(t, dir)
	if got := third.GetComposerDraft(); got != "" {
		t.Errorf("清空后仍有草稿：%q", got)
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
