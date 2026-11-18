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

	for _, q := range []string{
		`SELECT created_at FROM memos ORDER BY created_at LIMIT 1`,
		`SELECT created_at FROM items ORDER BY created_at LIMIT 1`,
		`SELECT created_at FROM snippets ORDER BY created_at LIMIT 1`,
	} {
		var v string
		if err := conn.QueryRow(q).Scan(&v); err == nil {
			fmt.Printf("%-40s -> %s\n", q[:34], v)
		}
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM memos`).Scan(&n)
	fmt.Println("memos 条数:", n)
	var bad int
	conn.QueryRow(`SELECT COUNT(*) FROM memos WHERE created_at NOT LIKE '____-__-__T__:__:__._________Z'`).Scan(&bad)
	fmt.Println("非固定宽度格式的备忘:", bad)
}
