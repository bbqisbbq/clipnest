// 一次性探针：把真实库的副本喂给 clip.Open，验证墓碑表迁移确实生效。
// 用法：go run ./scratch/migrateprobe "C:\path\to\dir(含 clipnest.db)"
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	st, err := clip.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer st.Close()

	epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	before := st.TombstonesScoped("entry", epoch, 500)
	fmt.Println("迁移后按 entry 作用域能查到的墓碑:", len(before))

	e, _, err := st.SaveCapture(clip.Captured{Kind: clip.KindText, Text: "migrate probe", Preview: "p", Hash: "probe-h", Size: 3})
	if err != nil {
		panic(err)
	}
	fmt.Println("新增条目 ok:", e.ID != "")
	fmt.Println("DeleteEntry:", st.DeleteEntry(e.ID))
	after := st.TombstonesScoped("entry", epoch, 500)
	fmt.Println("删除后墓碑数:", len(after), "(应比上面多 1)")
	for _, t := range after {
		if t.Hash == "probe-h" {
			fmt.Println("  找到 probe-h 墓碑:", t.DeletedAt.Format(time.RFC3339))
		}
	}
}
