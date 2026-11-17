// Command snippetseed writes a few demo snippets into the real ClipNest database
// so the UI can be inspected without typing them by hand.
//
//	go run ./scratch/snippetseed
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	dir := filepath.Join(os.Getenv("APPDATA"), "ClipNest")
	store, err := clip.Open(dir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	seeds := []clip.Snippet{
		{Kind: clip.SnippetText, Title: "办公地址", Trigger: "addr-", Group: "客户",
			Body: "杭州市滨江区 · ETool 科技园 A 座 1208"},
		{Kind: clip.SnippetText, Title: "周报模板", Trigger: "tpl-", Group: "写作",
			Body: "## 本周进展\n- 完成 …\n\n## 下周计划\n- 推进 …"},
		{Kind: clip.SnippetURL, Title: "部署后台", Trigger: "dev-", Group: "开发",
			Body: "https://dev.etool.site"},
		{Kind: clip.SnippetText, Title: "客户回复·退款", Trigger: "th-", Group: "客户",
			Body: "您好，退换货申请可在订单详情页提交，我们会在 24 小时内处理。"},
		{Kind: clip.SnippetCommand, Title: "查看分支", Trigger: "gb-", Group: "开发",
			Body: "git branch -vv"},
		{Kind: clip.SnippetText, Title: "光标演示", Trigger: "cur-", Group: "测试",
			Body: "AB{{cursor}}CD"},
	}

	for _, sp := range seeds {
		saved, err := store.SaveSnippet(sp)
		if err != nil {
			log.Printf("save %q: %v", sp.Title, err)
			continue
		}
		fmt.Printf("saved snippet %s (%s)\n", saved.Title, saved.Trigger)
	}
}
