package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	dir, _ := os.MkdirTemp("", "fts")
	defer os.RemoveAll(dir)
	db, err := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE f1 USING fts5(content, tokenize='trigram')`); err != nil {
		fmt.Println("FTS5(trigram) 不可用:", err)
	} else {
		fmt.Println("FTS5(trigram) 可用")
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE f2 USING fts5(content)`); err != nil {
		fmt.Println("FTS5(默认 unicode61) 不可用:", err)
	} else {
		fmt.Println("FTS5(默认 unicode61) 可用")
	}
	if _, err := db.Exec(`SELECT sqlite_version()`); err == nil {
		var v string
		db.QueryRow(`SELECT sqlite_version()`).Scan(&v)
		fmt.Println("SQLite 版本:", v)
	}
}
