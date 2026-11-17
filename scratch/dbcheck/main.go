package main
import ("database/sql";"fmt";"os";_ "modernc.org/sqlite")
func main() {
 db, _ := sql.Open("sqlite", "file:"+os.Getenv("APPDATA")+"/ClipNest/clipnest.db?mode=ro")
 defer db.Close()
 rows, err := db.Query("SELECT kind, substr(coalesce(nullif(preview,''), text),1,40), created_at, updated_at FROM items ORDER BY updated_at DESC LIMIT 20")
 if err != nil { fmt.Println("err:", err); return }
 for rows.Next() { var k,p,c,u string; rows.Scan(&k,&p,&c,&u); fmt.Printf("%-7s %-42s %s\n", k, p, u) }
 rows.Close()
}
