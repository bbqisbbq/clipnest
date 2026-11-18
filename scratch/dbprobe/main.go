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

	fmt.Println("=== 图片条目的正文 ===")
	rows, _ := conn.Query(`SELECT id, substr(content,1,42) FROM entries WHERE kind='image' LIMIT 5`)
	for rows.Next() {
		var id, content string
		rows.Scan(&id, &content)
		fmt.Printf("  %s  %q\n", id[:6], content)
	}
	rows.Close()

	var withRef, noRef int
	conn.QueryRow(`SELECT COUNT(*) FROM entries WHERE kind='image' AND content LIKE '%memoimg:%'`).Scan(&withRef)
	conn.QueryRow(`SELECT COUNT(*) FROM entries WHERE kind='image' AND content NOT LIKE '%memoimg:%'`).Scan(&noRef)
	fmt.Printf("  有图片引用 %d 条 / 仍无引用 %d 条\n", withRef, noRef)

	fmt.Println("=== entry_blobs ===")
	var nb, totalSize int
	conn.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size),0) FROM entry_blobs`).Scan(&nb, &totalSize)
	fmt.Printf("  %d 个附件，共 %d 字节\n", nb, totalSize)

	fmt.Println("=== 过期字段是否清空 ===")
	var stillExpiring int
	conn.QueryRow(`SELECT COUNT(*) FROM entries WHERE expires_at <> ''`).Scan(&stillExpiring)
	fmt.Printf("  仍有过期时间的条目: %d（应为 0）\n", stillExpiring)

	fmt.Println("=== 按来源 ===")
	r2, _ := conn.Query(`SELECT source, COUNT(*) FROM entries GROUP BY source`)
	for r2.Next() {
		var src string
		var n int
		r2.Scan(&src, &n)
		fmt.Printf("  %-10s %d\n", src, n)
	}
	r2.Close()
}
