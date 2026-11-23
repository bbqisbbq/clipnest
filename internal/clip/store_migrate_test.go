package clip

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// 老库的 tombstones 表主键只有 hash，而写墓碑用的是 `ON CONFLICT(hash, scope)`：
// SQLite 会整条拒绝这条语句，当年的代码又把错误吞了 —— 于是"界面上删了 17 条，
// tombstones 一条都没多"，删除永远不传播（2026-11-23 在一台真机上原样复现）。
//
// 这个用例锁两件事：Open 必须把表迁到 PRIMARY KEY (hash, scope)；迁移之后删除必须
// 真的写得出墓碑，老行也必须从 scope='item' 归到 'entry'。
func TestOpenMigratesLegacyTombstoneSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, dbFileName)

	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tombstones (
			hash TEXT PRIMARY KEY,
			deleted_at TEXT NOT NULL,
			device TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT 'item')`,
		`CREATE INDEX idx_tomb_deleted ON tombstones(deleted_at)`,
		`INSERT INTO tombstones (hash, deleted_at, device, scope)
		 VALUES ('legacy-item', '2026-11-18T08:41:16.000000000Z', 'PC', 'item')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	raw.Close()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	tombs := st.TombstonesScoped("entry", epoch, 10)
	if len(tombs) != 1 || tombs[0].Hash != "legacy-item" {
		t.Fatalf("老墓碑没有迁到 entry 作用域（同步侧会查不到）: %+v", tombs)
	}

	// 迁移之后：删除必须写得出墓碑。老 schema 上这条会静默失败。
	e, _, err := st.SaveCapture(Captured{Kind: KindText, Text: "after migration", Preview: "p", Hash: "h-after", Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !st.DeleteEntry(e.ID) {
		t.Fatal("删除失败")
	}
	tombs = st.TombstonesScoped("entry", epoch, 10)
	if len(tombs) != 2 {
		t.Fatalf("迁移后删除仍然没写墓碑: %+v", tombs)
	}

	// 同一份内容删两次：更新那条墓碑的时间，而不是报错/插入第二条。
	first := time.Time{}
	for _, tm := range tombs {
		if tm.Hash == "h-after" {
			first = tm.DeletedAt
		}
	}
	if first.IsZero() {
		t.Fatalf("找不到 h-after 的墓碑: %+v", tombs)
	}
	st.AddTombstoneScoped("entry", "h-after", first.Add(time.Hour), "PC")
	tombs = st.TombstonesScoped("entry", epoch, 10)
	if len(tombs) != 2 {
		t.Fatalf("重复写墓碑应该覆盖而不是新增: %+v", tombs)
	}
}
