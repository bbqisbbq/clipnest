package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	db := filepath.Join(os.Getenv("APPDATA"), "ClipNest", "clipnest.db")
	conn, _ := sql.Open("sqlite", "file:"+db+"?mode=ro&_pragma=busy_timeout(3000)")
	defer conn.Close()
	fmt.Println("=== 备忘图片迁移结果 ===")
	rows, _ := conn.Query(`SELECT hash, mime, storage, size FROM memo_blobs`)
	n := 0
	for rows.Next() {
		var h, mime, storage string
		var size int
		rows.Scan(&h, &mime, &storage, &size)
		fmt.Printf("  %s %-11s %-5s %d bytes\n", h[:8], mime, storage, size)
		n++
	}
	rows.Close()
	fmt.Println("memo_blobs 条数:", n)
	fmt.Println("=== 备忘正文里的引用 ===")
	r2, _ := conn.Query(`SELECT id, content FROM memos WHERE content LIKE '%memoimg:%'`)
	m := 0
	for r2.Next() {
		var id, content string
		r2.Scan(&id, &content)
		if len(content) > 60 {
			content = content[:60]
		}
		fmt.Printf("  %s %q\n", id[:6], content)
		m++
	}
	r2.Close()
	fmt.Println("含 memoimg 的备忘:", m)
}
