package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	dir, _ := os.MkdirTemp("", "tp")
	defer os.RemoveAll(dir)
	db, _ := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	defer db.Close()

	// RFC3339Nano 的小数位长度可变：字符串序 != 时间序
	vals := []string{
		"2026-11-18T13:00:00.5Z",
		"2026-11-18T13:00:00.1234567Z",
		"2026-11-18T13:00:00Z",
	}
	db.Exec(`CREATE TABLE t (ts TEXT)`)
	for _, v := range vals {
		db.Exec(`INSERT INTO t VALUES (?)`, v)
	}

	fmt.Println("按字符串排序:")
	rows, _ := db.Query(`SELECT ts FROM t ORDER BY ts`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		fmt.Println("  ", s)
	}
	rows.Close()

	fmt.Println("按 julianday 排序:")
	rows, _ = db.Query(`SELECT ts FROM t ORDER BY julianday(ts)`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		fmt.Println("  ", s)
	}
	rows.Close()

	// julianday 能否解析 9 位小数
	var ok int
	db.QueryRow(`SELECT CASE WHEN julianday('2026-11-18T13:00:00.123456789Z') IS NULL THEN 0 ELSE 1 END`).Scan(&ok)
	fmt.Println("julianday 支持 9 位小数:", ok == 1)

	// 固定宽度格式（9 位小数）字符串序是否正确
	fmt.Println("固定宽度格式字符串排序:")
	db.Exec(`CREATE TABLE t2 (ts TEXT)`)
	for _, v := range []string{
		"2026-11-18T13:00:00.500000000Z",
		"2026-11-18T13:00:00.123456700Z",
		"2026-11-18T13:00:00.000000000Z",
	} {
		db.Exec(`INSERT INTO t2 VALUES (?)`, v)
	}
	rows, _ = db.Query(`SELECT ts FROM t2 ORDER BY ts`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		fmt.Println("  ", s)
	}
	rows.Close()
}
