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

	fmt.Println("=== 墓碑（能证明是不是通过应用删除的）===")
	rows, err := conn.Query(`SELECT scope, hash, deleted_at FROM tombstones ORDER BY deleted_at DESC LIMIT 12`)
	if err != nil {
		fmt.Println(err)
		return
	}
	n := 0
	for rows.Next() {
		var scope, hash, at string
		rows.Scan(&scope, &hash, &at)
		fmt.Printf("  %-8s %s  %s\n", scope, hash, at)
		n++
	}
	rows.Close()
	fmt.Println("墓碑数:", n)

	fmt.Println("=== 日志里最近的删除/清空动作 ===")
}
