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
	conn, err := sql.Open("sqlite", "file:"+db+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer conn.Close()

	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		fmt.Println("tables:", err)
		return
	}
	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	fmt.Printf("表: %v\n", names)

	for _, t := range []string{"items", "snippets", "memos", "blobs", "tombstones", "meta"} {
		var c int
		if err := conn.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&c); err != nil {
			fmt.Printf("  %-11s 读取失败: %v\n", t, err)
			continue
		}
		fmt.Printf("  %-11s %d 行\n", t, c)
	}

	var fts int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='memos_fts'`).Scan(&fts); err == nil {
		fmt.Printf("  memos_fts 存在: %v\n", fts == 1)
	}
}
