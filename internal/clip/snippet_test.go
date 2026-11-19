package clip

import "testing"

// 设为片段之后，缩写必须**立刻**能匹配上 —— 触发器索引是内存里的，
// 以前只有 SaveSnippet / 保存设置 / 启动会重建，于是「设为片段」要重启才展开。
func TestSnippetTriggerIndexFollowsEntryWrites(t *testing.T) {
	svc := newTestService(t, t.TempDir())

	e, err := svc.SaveEntry(Entry{Content: "地址在这里，别再翻邮箱了。", Source: SourceManual, Kind: KindText})
	if err != nil {
		t.Fatalf("save entry: %v", err)
	}
	if _, _, ok := svc.Match("zz-test"); ok {
		t.Fatal("还没设成片段就不该命中")
	}

	if _, err := svc.PromoteToSnippet(e.ID, "zz-test-"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	trg, n, ok := svc.Match("zz-test")
	if !ok {
		t.Fatal("设为片段之后应立刻命中（索引没重建）")
	}
	if n != len([]rune("zz-test")) {
		t.Fatalf("消耗长度 = %d, want %d", n, len([]rune("zz-test")))
	}
	if trg.Body != "地址在这里，别再翻邮箱了。" {
		t.Fatalf("命中了别的触发器：%q", trg.Body)
	}

	// 归档会把条目从可展开的片段里摘掉
	if _, err := svc.SetEntryArchived(e.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, _, ok := svc.Match("zz-test"); ok {
		t.Fatal("归档后不该再命中")
	}

	// 恢复归档 + 取消片段：都不该再命中
	if _, err := svc.SetEntryArchived(e.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if _, _, ok := svc.Match("zz-test"); !ok {
		t.Fatal("取消归档后应重新命中")
	}
	if _, err := svc.DemoteFromSnippet(e.ID); err != nil {
		t.Fatalf("demote: %v", err)
	}
	if _, _, ok := svc.Match("zz-test"); ok {
		t.Fatal("取消片段后不该命中")
	}

	// 直接改缩写的保存（详情页的属性面板走这条路）也要立刻生效
	e2, ok := svc.store.GetEntry(e.ID)
	if !ok {
		t.Fatal("entry gone")
	}
	e2.TriggerKey = "zz-direct-"
	e2.SetAction(ActionSnippet, true)
	if _, err := svc.SaveEntry(e2); err != nil {
		t.Fatalf("save entry with trigger: %v", err)
	}
	if _, _, ok := svc.Match("zz-direct"); !ok {
		t.Fatal("在详情里改缩写也要立刻命中")
	}

	// 删除条目同理
	if !svc.DeleteEntry(e.ID) {
		t.Fatal("delete failed")
	}
	if _, _, ok := svc.Match("zz-direct"); ok {
		t.Fatal("删掉条目后不该再命中")
	}
}

// 富文本 / 链接 / 文件引用 / 私有格式的条目设成片段后，展开必须出文字。
// 根因：snippetFromEntry 原来把 entry kind 直接强转成 snippet kind（rich 不在
// SnippetKind 里），Render 落到 default 分支返回空串，引擎记 "produced no text"。
func TestSnippetKindMappingRendersText(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		body string
	}{
		{"富文本", KindRichText, "Hello ClipNest\nsecond line"},
		{"链接", KindLink, "https://example.com/x"},
		{"纯文本", KindText, "plain body"},
		{"私有格式", KindCustom, "fallback text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := newTestService(t, t.TempDir())
			e, err := svc.SaveEntry(Entry{Content: c.body, Kind: c.kind, Source: SourceClipboard})
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			if _, err := svc.PromoteToSnippet(e.ID, "zz-kind-"); err != nil {
				t.Fatalf("promote: %v", err)
			}
			trg, _, ok := svc.Match("zz-kind")
			if !ok {
				t.Fatal("缩写没命中")
			}
			text, _ := svc.Render(trg)
			if text != c.body {
				t.Fatalf("展开结果 = %q, want %q", text, c.body)
			}
		})
	}
}
