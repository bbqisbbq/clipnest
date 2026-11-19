package clip

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 待办（清单项级）：正文里的 GFM 任务列表就是真源。
//
//	- [ ] 跑一遍发版检查
//	- [x] 已经做完的事
//
// 为什么要从正文解析而不是建一张 todos 表：正文已经是唯一真相，导出/同步/搜索都
// 直接可用；再建表就有了第二个真相，勾选时要保证两处一致，还得自己处理墓碑。
//
// 不做的事：不提醒、不排期、不做子任务树。删掉那行任务，它就消失。
//
// 与 tag 的关系：完成状态走 entries.todo_state 字段（派生列），**不往正文里塞 tag** ——
// 项目铁律是「tag 纯用户语义，系统不往里塞东西」（见 entry.go 顶部注释）。

const (
	todoStateOpen = "open" // 有未完成项
	todoStateDone = "done" // 全部完成
)

// TodoItem is one task line inside an entry's content.
type TodoItem struct {
	// Key 在「文本不变」时稳定：勾选只改 [ ]/[x]，不动文本，所以能拿它定位。
	Key    string `json:"key"`
	Text   string `json:"text"`
	Done   bool   `json:"done"`
	Line   int    `json:"line"`   // 0-based 行号：改正文时按行改，比按 key 搜更准
	Indent int    `json:"indent"` // 缩进层级（每 2 空格一层，最多 3 层）
}

// 任务行：- / * / + / 1. / 1) 起头，紧跟 [ ]、[x] 或 [X]。
var todoLinePattern = regexp.MustCompile(`^(\s*)(?:[-*+]|\d{1,9}[.)])\s+\[([ xX])\]\s+(.*)$`)

// 围栏代码块的边界（``` 或 ~~~，可带语言）。代码块里的 "- [ ]" 不是任务。
var fencePattern = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})")

// ParseTodos pulls the task list out of a piece of Markdown.
//
// entryID 参与 Key 的计算：不同条目里的同一句话不该撞 key（收集页是跨条目的）。
func ParseTodos(entryID, content string) []TodoItem {
	if !strings.Contains(content, "[") {
		return nil // 绝大多数条目没有任务，快速跳过
	}
	if strings.Contains(content, "\r") {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}

	lines := strings.Split(content, "\n")
	out := make([]TodoItem, 0, 4)
	fence := ""       // 当前所在代码块的围栏标记（空 = 不在代码块里）
	seen := map[string]int{} // 归一化文本 → 已出现次数，用于区分重复文本

	for i, raw := range lines {
		if m := fencePattern.FindStringSubmatch(raw); m != nil {
			if fence == "" {
				fence = m[1]
			} else if strings.HasPrefix(m[1], string(fence[0])) && len(m[1]) >= len(fence) {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}

		m := todoLinePattern.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[3])
		if text == "" {
			continue
		}
		norm := normalizeTodoText(text)
		dup := seen[norm]
		seen[norm] = dup + 1

		indent := len(m[1]) / 2
		if indent > 3 {
			indent = 3
		}
		out = append(out, TodoItem{
			Key:    todoKey(entryID, norm, dup),
			Text:   text,
			Done:   m[2] == "x" || m[2] == "X",
			Line:   i,
			Indent: indent,
		})
	}
	return out
}

// normalizeTodoText 把空白折叠掉：纯空白改动不算"改了文字"，key 才不会漂。
func normalizeTodoText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func todoKey(entryID, normText string, dup int) string {
	sum := sha1.Sum([]byte(entryID + "\x00" + normText + "\x00" + strconv.Itoa(dup)))
	return hex.EncodeToString(sum[:])[:12]
}

// todoCounts returns (total, open) for a parsed task list.
func todoCounts(items []TodoItem) (total, open int) {
	for _, it := range items {
		if !it.Done {
			open++
		}
	}
	return len(items), open
}

// todoStateOf maps counts to the derived column value.
func todoStateOf(total, open int) string {
	switch {
	case total == 0:
		return ""
	case open > 0:
		return todoStateOpen
	default:
		return todoStateDone
	}
}

// deriveTodo refreshes the derived todo fields after the content changed.
//
// 只在写入路径（SaveEntry / mergeEntry）调用：读取路径不该改动 todo_done_at。
func deriveTodo(e *Entry, now time.Time) {
	items := ParseTodos(e.ID, e.Content)
	total, open := todoCounts(items)
	e.TodoTotal, e.TodoOpen = total, open
	state := todoStateOf(total, open)
	if state == todoStateDone {
		// 已经是完成态且有时间就保留：避免每次保存都刷新完成时刻
		if e.TodoState != todoStateDone || e.TodoDoneAt == nil {
			at := now
			e.TodoDoneAt = &at
		}
	} else {
		e.TodoDoneAt = nil
	}
	e.TodoState = state
}

// ---- 勾选 ----------------------------------------------------------------

// SetTodoDone flips one task line and saves the entry back.
//
// 只改那一个字符：别的行、tags、trigger、hash 全部原样保留。前端传回来的只有
// (entryID, key, done)，不接受整条 Entry —— 那会静默重置用户的其他设置。
func (s *Store) SetTodoDone(entryID, key string, done bool) (Entry, error) {
	e, ok := s.GetEntry(entryID)
	if !ok {
		return Entry{}, errors.New("条目不存在")
	}
	items := ParseTodos(e.ID, e.Content)
	var target *TodoItem
	for i := range items {
		if items[i].Key == key {
			target = &items[i]
			break
		}
	}
	if target == nil {
		return Entry{}, errors.New("这条待办已经不在了，刷新一下列表")
	}
	if target.Done == done {
		return e, nil
	}

	lines := strings.Split(e.Content, "\n")
	if target.Line < 0 || target.Line >= len(lines) {
		return Entry{}, errors.New("这条待办已经不在了，刷新一下列表")
	}
	lines[target.Line] = replaceTodoMark(lines[target.Line], done)
	e.Content = strings.Join(lines, "\n")
	return s.SaveEntry(e)
}

// replaceTodoMark rewrites the first "[ ]"/"[x]" marker on a task line.
func replaceTodoMark(line string, done bool) string {
	mark := "[ ]"
	if done {
		mark = "[x]"
	}
	idx := strings.Index(line, "[")
	if idx < 0 {
		return line
	}
	end := strings.Index(line[idx:], "]")
	if end < 0 {
		return line
	}
	return line[:idx] + mark + line[idx+end+1:]
}

// ---- 收集页 --------------------------------------------------------------

// TodoRef is one row of the todo collection page: a task plus where it lives.
type TodoRef struct {
	EntryID        string    `json:"entryId"`
	EntryTitle     string    `json:"entryTitle,omitempty"`
	EntryPreview   string    `json:"entryPreview"`
	EntrySource    Source    `json:"entrySource"`
	EntryCreatedAt time.Time `json:"entryCreatedAt"`
	Item           TodoItem  `json:"item"`
}

// ListTodos expands the entries matching q into individual task rows.
//
// q 里的 Todo 条件会被忽略：这一页本来就要同时看未完成和已完成两类。
func (s *Store) ListTodos(q EntryQuery) []TodoRef {
	q.Todo = ""
	q.Day, q.From, q.To = "", "", ""
	q.Before = ""
	q.Limit = 500
	q.Tail = true // 最新的条目先看

	entries := s.ListEntries(q)
	out := make([]TodoRef, 0, 32)
	for _, e := range entries {
		for _, item := range ParseTodos(e.ID, e.Content) {
			out = append(out, TodoRef{
				EntryID:        e.ID,
				EntryTitle:     e.Title,
				EntryPreview:   e.Preview(),
				EntrySource:    e.Source,
				EntryCreatedAt: e.CreatedAt,
				Item:           item,
			})
		}
	}
	// 未完成在前；同状态里最新的条目在前。
	// 注意 ListEntries 的 Tail 语义是"取最新 N 条再按时间正序返回"，
	// 所以这里显式按 EntryCreatedAt 倒序，不依赖上游顺序。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Item.Done != out[j].Item.Done {
			return !out[i].Item.Done
		}
		return out[i].EntryCreatedAt.After(out[j].EntryCreatedAt)
	})
	return out
}
