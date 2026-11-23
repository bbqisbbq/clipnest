// vacuum：把正在被 ClipNest 使用的库导出成一致的快照（VACUUM INTO）。
//
// 为什么不能直接拷：WAL 模式下 clipnest.db 与 clipnest.db-wal 是一对，分开拷
// 会得到撕裂的半成品（提交了一半的页、索引与表对不上）。VACUUM INTO 由 SQLite
// 自己在一个读事务里写出一份自洽的单文件副本。
//
// 用法：go run ./scratch/vacuum <源目录> <目标文件>
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("用法: go run ./scratch/vacuum <源目录> <目标文件>")
		os.Exit(2)
	}
	dir, dst := os.Args[1], os.Args[2]
	src := filepath.Join(dir, "clipnest.db")
	if _, err := os.Stat(src); err != nil {
		fmt.Printf("找不到 %s: %v\n", src, err)
		os.Exit(1)
	}
	abs, err := filepath.Abs(dst)
	if err != nil {
		panic(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(src))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO '` + filepath.ToSlash(abs) + `'`); err != nil {
		panic(err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		panic(err)
	}
	fmt.Printf("快照已写出: %s (%d bytes)\n", abs, st.Size())
}
