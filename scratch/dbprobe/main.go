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

	// 模拟各内置筛选器的 SQL
	checks := []struct{ name, where string }{
		{"全部内容", "archived=0"},
		{"我写的", "archived=0 AND source='manual'"},
		{"剪贴板", "archived=0 AND auto_tags LIKE '%,剪贴板,%'"},
		{"片段", "archived=0 AND trigger_key<>''"},
		{"图片", "archived=0 AND kind='image'"},
		{"今天", "archived=0 AND created_at >= date('now','localtime','start of day')"},
	}
	for _, c := range checks {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM entries WHERE ` + c.where).Scan(&n); err != nil {
			fmt.Printf("  %-10s 查询失败: %v\n", c.name, err)
			continue
		}
		fmt.Printf("  %-10s %d 条\n", c.name, n)
	}

	fmt.Println("=== 日历：按天计数（最近 5 天）===")
	rows, _ := conn.Query(`SELECT substr(created_at,1,10) d, COUNT(*) FROM entries GROUP BY d ORDER BY d DESC LIMIT 5`)
	for rows.Next() {
		var d string
		var n int
		rows.Scan(&d, &n)
		fmt.Printf("  %s  %d 条\n", d, n)
	}
	rows.Close()
}
