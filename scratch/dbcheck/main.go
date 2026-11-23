// 只读体检：把 ClipNest 的库拍成一致性快照，报条目/墓碑/附件数，并可选地跟一个
// 同步包（zip）对账 —— "远端包里有、本机库里没有"和"删了但没留墓碑"都会列出来。
//
// 用法：
//
//	go run ./scratch/dbcheck                                   # 默认查 %APPDATA%\ClipNest
//	go run ./scratch/dbcheck -data "C:\path\to\ClipNest"
//	go run ./scratch/dbcheck -pkg "C:\path\to\device.zip"
//
// 为什么要先 VACUUM INTO：WAL 模式下直接复制 clipnest.db + -wal 会拿到**撕开的**
// 快照（SQLite 会悄悄只用主库文件，看起来像"条目全没了"）。VACUUM INTO 走的是
// 一个读事务，反映的是应用正在用的那份逻辑状态。
package main

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	dataDir := flag.String("data", filepath.Join(os.Getenv("APPDATA"), "ClipNest"), "ClipNest 数据目录")
	pkgPath := flag.String("pkg", "", "（可选）要核对的本机同步包 zip")
	flag.Parse()

	dbPath := filepath.Join(*dataDir, "clipnest.db")
	if _, err := os.Stat(dbPath); err != nil {
		fail("找不到 %s: %v", dbPath, err)
	}
	snap := filepath.Join(os.TempDir(), fmt.Sprintf("clipnest-check-%d.db", time.Now().UnixNano()))
	defer os.Remove(snap)

	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(8000)")
	if err != nil {
		fail("打开库失败: %v", err)
	}
	if _, err := conn.Exec(`VACUUM INTO '` + filepath.ToSlash(snap) + `'`); err != nil {
		fail("拍快照失败（应用正在写？）: %v", err)
	}
	conn.Close()

	sc, err := sql.Open("sqlite", "file:"+filepath.ToSlash(snap)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		fail("打开快照失败: %v", err)
	}
	defer sc.Close()

	fmt.Printf("数据目录: %s\n\n", *dataDir)
	for _, q := range []struct{ label, sql string }{
		{"条目", `SELECT COUNT(*) FROM entries`},
		{"  其中归档", `SELECT COUNT(*) FROM entries WHERE archived = 1`},
		{"墓碑", `SELECT COUNT(*) FROM tombstones`},
		{"附件行", `SELECT COUNT(*) FROM entry_blobs`},
		{"全文索引行", `SELECT COUNT(*) FROM entries_fts`},
	} {
		var n int
		if err := sc.QueryRow(q.sql).Scan(&n); err != nil {
			fmt.Printf("%-12s ERR %v\n", q.label, err)
			continue
		}
		fmt.Printf("%-12s %d\n", q.label, n)
	}

	fmt.Println("\n最近 7 天的墓碑（按天）：")
	dump(sc, `SELECT substr(deleted_at,1,10)||'  '||COUNT(*) FROM tombstones
	          GROUP BY substr(deleted_at,1,10) ORDER BY 1 DESC LIMIT 7`)
	fmt.Println("\n最新 5 条：")
	dump(sc, `SELECT substr(hash,1,8)||'  '||updated_at||'  '||kind FROM entries
	          ORDER BY created_at DESC LIMIT 5`)

	have := map[string]bool{}
	rows, err := sc.Query(`SELECT hash FROM entries`)
	if err == nil {
		for rows.Next() {
			var h string
			if rows.Scan(&h) == nil {
				have[h] = true
			}
		}
		rows.Close()
	}
	tombs := map[string]string{}
	rows, err = sc.Query(`SELECT hash, deleted_at FROM tombstones`)
	if err == nil {
		for rows.Next() {
			var h, at string
			if rows.Scan(&h, &at) == nil {
				tombs[h] = at
			}
		}
		rows.Close()
	}

	if *pkgPath == "" {
		return
	}
	fmt.Printf("\n与本机包对账: %s\n", *pkgPath)
	var idx struct {
		Entries []struct {
			Hash string `json:"hash"`
		} `json:"entries"`
	}
	readIndex(*pkgPath, &idx)
	missing, untombed := 0, 0
	for _, e := range idx.Entries {
		if have[e.Hash] {
			continue
		}
		missing++
		if at, ok := tombs[e.Hash]; ok {
			fmt.Printf("  不在库里（有墓碑 %s） %s\n", at, short(e.Hash))
			continue
		}
		untombed++
		fmt.Printf("  不在库里**且没有墓碑** %s  ← 对端不会跟着删，还会把它传回来\n", short(e.Hash))
	}
	fmt.Printf("\n包内 %d 条：本机有 %d，缺 %d（其中 %d 条没有墓碑）\n",
		len(idx.Entries), len(idx.Entries)-missing, missing, untombed)
}

func dump(db *sql.DB, q string) {
	rows, err := db.Query(q)
	if err != nil {
		fmt.Println("  ERR", err)
		return
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			lines = append(lines, s)
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Println("  " + l)
	}
}

func readIndex(pkg string, out any) {
	zr, err := zip.OpenReader(pkg)
	if err != nil {
		fail("打开同步包失败: %v", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "package.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			fail("读 package.json 失败: %v", err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		if err := json.Unmarshal(body, out); err != nil {
			fail("解析 package.json 失败: %v", err)
		}
		return
	}
	fail("包里没有 package.json")
}

func short(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
