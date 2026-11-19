package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// Prints the meta table of the live ClipNest db. Device-local state such as
// capture.size lives here and there is no sqlite CLI on this machine, so this is
// the read path; `-del <key>` removes one (only while the app is not running).
func main() {
	readOnly := true
	delKey := ""
	if len(os.Args) >= 3 && os.Args[1] == "-del" {
		readOnly = false
		delKey = os.Args[2]
	}
	mode := "?mode=ro"
	if !readOnly {
		mode = "?mode=rw"
	}
	db, err := sql.Open("sqlite", "file:"+os.Getenv("APPDATA")+"/ClipNest/clipnest.db"+mode)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer db.Close()
	if delKey != "" {
		if _, err := db.Exec("DELETE FROM meta WHERE k = ?", delKey); err != nil {
			fmt.Println("delete:", err)
			return
		}
		fmt.Println("deleted:", delKey)
		return
	}
	rows, err := db.Query("SELECT k, v FROM meta ORDER BY k")
	if err != nil {
		fmt.Println("query:", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		fmt.Printf("%-24s %s\n", k, v)
	}
}
