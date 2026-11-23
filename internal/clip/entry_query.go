package clip

import (
	"database/sql"
	"sort"
	"strings"
	"time"
)

// 查询侧：EntryQuery（侧栏筛选、日历、搜索都编译成它）、分页列表、日历日界，
// 以及统计计数与标签/缩写索引查询。
// 不负责：写库与行映射（entry_store.go）、界面 API（entry_api.go）。

// EntryQuery is the single filter language: the sidebar's saved filters, the
// calendar's day selection and the search box all compile down to this.
type EntryQuery struct {
	Text     string `json:"text"`
	Tag      string `json:"tag"`
	Source   string `json:"source"` // manual | clipboard | import
	Action   string `json:"action"` // clipboard | snippet
	Kind     string `json:"kind"`
	Snippets bool   `json:"snippets"` // 只看有缩写的
	Pinned   bool   `json:"pinned"`
	Archived bool   `json:"archived"`
	// Todo 按派生列过滤：open（有未完成项）/ done（全完成）/ none（没有任务行）
	Todo   string `json:"todo"`
	Day    string `json:"day"`  // YYYY-MM-DD：日历点某天
	From   string `json:"from"` // RFC3339（含）
	To     string `json:"to"`   // RFC3339（不含）
	Limit  int    `json:"limit"`
	Before string `json:"before"` // 游标：created_at < 此值
	Tail   bool   `json:"tail"`   // true = 取最新 N 条
	// Rule 是自定义筛选器的规则树（and/or/not）。与上面的扁平字段是 AND 关系，
	// 所以内置筛选器保持简单、老数据（没有 rule）行为完全不变。
	Rule *FilterGroup `json:"rule,omitempty"`
}

// ListEntries applies the query. Ordered chronologically (chat style).
func (s *Store) ListEntries(q EntryQuery) []Entry {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where, args := q.where()
	order := "ASC"
	if q.Tail {
		order = "DESC"
	}
	sqlText := `SELECT ` + entryColumns + ` FROM entries WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at ` + order + `, updated_at ` + order + ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		Logf(s.dir, "entry list failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]Entry, 0, 32)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return out
		}
		out = append(out, e)
	}
	if q.Tail {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// AllEntries returns every entry in the store, oldest first, with no page limit.
//
// ListEntries 有一条 500 的硬上限（那是对界面的保护：谁也不能一次拉一万张卡片）。
// 但同步包要的是**全量**——用 ListEntries 组包等于把库截断，老条目永远上不了云。
// 所以这里单独开一条不带 LIMIT 的读，只给同步用。
func (s *Store) AllEntries() []Entry {
	rows, err := s.db.Query(
		`SELECT ` + entryColumns + ` FROM entries ORDER BY created_at ASC, updated_at ASC`)
	if err != nil {
		Logf(s.dir, "all entries failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]Entry, 0, 256)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return out
		}
		out = append(out, e)
	}
	return out
}

// where compiles the query into SQL predicates. Both the list and the calendar
// use it, so a heat dot can never disagree with what clicking that day shows.
func (q EntryQuery) where() ([]string, []any) {
	where := make([]string, 0, 8)
	var args []any

	// 规则里显式提到 archived 时，让规则说了算（否则 "archived is true" 永远查不到东西）
	archiveByRule := q.Rule != nil && ruleTouches(*q.Rule, "archived")
	if !archiveByRule {
		where = append(where, "archived = ?")
		args = append(args, boolInt(q.Archived))
	}
	if q.Rule != nil {
		if clause, ruleArgs, ok := compileRule(*q.Rule, time.Now()); ok {
			where = append(where, clause)
			args = append(args, ruleArgs...)
		}
	}

	if v := strings.TrimSpace(q.Source); v != "" {
		where = append(where, "source = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.Action); v != "" {
		where = append(where, "actions LIKE ?")
		args = append(args, "%,"+v+",%")
	}
	if v := strings.TrimSpace(q.Tag); v != "" {
		where = append(where, "tags LIKE ?")
		args = append(args, "%,"+v+",%")
	}
	if v := strings.TrimSpace(q.Kind); v != "" {
		where = append(where, "kind = ?")
		args = append(args, v)
	}
	if q.Snippets {
		where = append(where, "trigger_key <> ''")
	}
	if v := strings.TrimSpace(q.Todo); v != "" {
		// none = 正文里没有任务行；open/done 直接比派生列（内容变化时由 SaveEntry 维护）
		if v == "none" {
			where = append(where, "todo_state = ''")
		} else {
			where = append(where, "todo_state = ?")
			args = append(args, v)
		}
	}
	if q.Pinned {
		where = append(where, "pinned = 1")
	}
	if v := strings.TrimSpace(q.Day); v != "" {
		if start, end, ok := dayBounds(v); ok {
			where = append(where, "created_at >= ? AND created_at < ?")
			args = append(args, start, end)
		}
	}
	if v := strings.TrimSpace(q.From); v != "" {
		where = append(where, "created_at >= ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.To); v != "" {
		where = append(where, "created_at < ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.Before); v != "" {
		// created_at 是固定宽度 RFC3339（见 timeLayout），字符串序 == 时间序
		where = append(where, "created_at < ?")
		args = append(args, v)
	}

	text := strings.TrimSpace(q.Text)
	if text != "" {
		if len([]rune(text)) >= 3 {
			where = append(where, `id IN (SELECT id FROM entries_fts WHERE entries_fts MATCH ?)`)
			args = append(args, `"`+strings.ReplaceAll(text, `"`, `""`)+`"`)
		} else {
			like := "%" + escapeLike(text) + "%"
			// 少于 3 个字用不了 trigram（FTS5 的 trigram 分词至少 3 个字），退回 LIKE。
			// ocr_text 也要一起扫：图片的识别文字同样该被两字词搜到。
			where = append(where, `(content LIKE ? ESCAPE '\' OR tags LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR ocr_text LIKE ? ESCAPE '\')`)
			args = append(args, like, like, like, like)
		}
	}
	// 规则可能一个条件都没剩下（全被丢弃）：给个恒真条件，别拼出空 WHERE
	if len(where) == 0 {
		where = append(where, "1 = 1")
	}
	return where, args
}

// CountEntriesMatching counts the entries matching q. 筛选器弹窗用它做"命中 N 条"预览。
// （不带条件的 CountEntries 在下面，被同步与统计用着，别混。）
func (s *Store) CountEntriesMatching(q EntryQuery) int {
	where, args := q.where()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM entries WHERE `+strings.Join(where, " AND "), args...).Scan(&n); err != nil {
		Logf(s.dir, "count entries failed: %v", err)
		return 0
	}
	return n
}

// CountByDay counts entries per local calendar day in [from, to), using the same
// predicates as ListEntries (minus the day/paging part, which the month range
// replaces). This is what feeds the calendar's heat dots.
//
// 必须有 archived 这层：日历点某天要显示的条目 = 数出来的那批条目，
// 否则会出现"这一天有 3 条"却没有一条能点开的情况。
func (s *Store) CountByDay(q EntryQuery, from, to string) []DayCount {
	// 日历自己给时间范围，游标/分页/按天这些条件必须忽略
	q.Day, q.From, q.To, q.Before = "", "", "", ""
	q.Limit, q.Tail = 0, false

	where, args := q.where()
	where = append(where, "created_at >= ? AND created_at < ?")
	args = append(args, from, to)

	rows, err := s.db.Query(
		`SELECT created_at FROM entries WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		Logf(s.dir, "calendar count failed: %v", err)
		return nil
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		t := decodeTime(raw)
		if t.IsZero() {
			continue
		}
		counts[t.Local().Format("2006-01-02")]++
	}

	out := make([]DayCount, 0, len(counts))
	for day, n := range counts {
		out = append(out, DayCount{Day: day, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

// dayBounds turns "YYYY-MM-DD" (local) into a UTC [start, end) timestamp pair.
func dayBounds(day string) (string, string, bool) {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return "", "", false
	}
	return encodeTime(t), encodeTime(t.AddDate(0, 0, 1)), true
}

// EntryStats summarises counts for the sidebar / filters.
type EntryStats struct {
	Total     int `json:"total"`
	Manual    int `json:"manual"`
	Clipboard int `json:"clipboard"`
	Snippets  int `json:"snippets"`
	Pinned    int `json:"pinned"`
	Archived  int `json:"archived"`
	Today     int `json:"today"`
	// 待办按「清单项」计数（不是条目数）：侧栏角标要显示"还有几件事没做"
	TodoOpen int `json:"todoOpen"`
	TodoDone int `json:"todoDone"`
}

// EntryStats counts entries per dimension.
//
// **数字必须与"点进去能看到的条数"一致**，所以这里不再手写 SQL，而是直接复用
// BuiltinFilters() 里那些查询、走同一个 `where()` 编译器：筛选器改了，角标自动跟着改。
//
// 以前两边各写一套：角标用 `actions LIKE '%,clipboard,%'`（"有这个能力"），
// 而「剪贴板」筛选器后来按 `action` 也是同一列，于是**老数据 actions 为空的行两边一起漏**，
// 手写却带 clipboard 能力的行又被算进"剪贴板" —— 实测「我写的 3 + 剪贴板 2」凑不出「全部 6」。
func (s *Store) EntryStats() EntryStats {
	var st EntryStats
	byID := map[string]EntryQuery{}
	for _, f := range BuiltinFilters() {
		byID[f.ID] = f.Query
	}
	st.Total = s.CountEntriesMatching(byID["all"])
	st.Manual = s.CountEntriesMatching(byID["mine"])
	st.Clipboard = s.CountEntriesMatching(byID["clipboard"])
	st.Snippets = s.CountEntriesMatching(byID["snippets"])
	st.Pinned = s.CountEntriesMatching(byID["pinned"])
	st.Archived = s.CountEntriesMatching(byID["archived"])
	st.Today = s.CountEntriesMatching(EntryQuery{Day: time.Now().Format("2006-01-02")})

	// 待办要按清单项数，只能扫正文（有任务行的条目通常极少，代价可接受）
	rows, err := s.db.Query(`SELECT content FROM entries WHERE archived = 0 AND todo_state <> ''`)
	if err == nil {
		for rows.Next() {
			var content string
			if rows.Scan(&content) != nil {
				continue
			}
			total, open := todoCounts(ParseTodos("", content))
			st.TodoOpen += open
			st.TodoDone += total - open
		}
		rows.Close()
	}
	return st
}

// EntryTags lists the distinct tags in use, most used first.
func (s *Store) EntryTags() []string {
	rows, err := s.db.Query(`SELECT tags FROM entries WHERE archived = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var tags string
		if rows.Scan(&tags) != nil {
			continue
		}
		for _, t := range splitTagSpec(tags) {
			counts[t]++
		}
	}
	type kv struct {
		tag string
		n   int
	}
	list := make([]kv, 0, len(counts))
	for t, n := range counts {
		list = append(list, kv{t, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n == list[j].n {
			return list[i].tag < list[j].tag
		}
		return list[i].n > list[j].n
	})
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.tag)
	}
	return out
}

// EntryTriggers returns entries that participate in abbreviation expansion.
func (s *Store) EntryTriggers() []Entry {
	rows, err := s.db.Query(`SELECT ` + entryColumns + ` FROM entries
		WHERE trigger_key <> '' AND archived = 0 ORDER BY LENGTH(trigger_key) DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return out
		}
		out = append(out, e)
	}
	return out
}

// FindEntryByTrigger looks up a snippet entry by its abbreviation.
func (s *Store) FindEntryByTrigger(key string, caseSensitive bool) (Entry, bool) {
	var row *sql.Row
	if caseSensitive {
		row = s.db.QueryRow(`SELECT `+entryColumns+` FROM entries
			WHERE trigger_key = ? AND case_sensitive = 1 AND archived = 0 LIMIT 1`, key)
	} else {
		row = s.db.QueryRow(`SELECT `+entryColumns+` FROM entries
			WHERE LOWER(trigger_key) = LOWER(?) AND archived = 0 LIMIT 1`, key)
	}
	e, err := scanEntry(row)
	if err != nil {
		return Entry{}, false
	}
	return e, true
}

// CountEntries is the number of live (non-archived) entries.
func (s *Store) CountEntries() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE archived = 0`).Scan(&n)
	return n
}

// CountAllEntries counts every row, archived included.
//
// 和 CountEntries 的区别要紧：那个是"界面上看得到的条数"（不含归档），而同步包带
// **全量**（归档条目也要同步出去，它只是被收起来，不是被删）。两处用混就会出现
// "全都归档了 ⇒ 判定本地为空 ⇒ 拒绝上传"。
func (s *Store) CountAllEntries() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&n)
	return n
}
