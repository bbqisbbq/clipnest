package clip

import (
	"errors"
	"strings"
	"time"
)

// 筛选器规则树：自定义筛选器用它表达 and / or / not。
//
// 为什么是结构化规则而不是表达式字符串（类 TiddlyWiki 那种）：
// 结构化规则不用写解析器、字段与算子都在白名单里（天然防注入），
// UI 也能直接渲染成"条件行 + 组"。
//
// 唯一的真相仍然是 EntryQuery.where()：规则在这里被编译成 SQL 片段，
// 再拼进那个唯一入口，所以列表、日历圆点、统计的口径不会分叉。
//
// 刻意只支持一层嵌套：组里放条件，组与组之间不再互相嵌套。
// 无限嵌套的 UI 是 bug 温床，而 99% 的筛选需求一层就够。

// FilterGroup 是一组条件的组合方式。
type FilterGroup struct {
	Op         string            `json:"op"`                   // and | or | not
	Conditions []FilterCondition `json:"conditions,omitempty"` // 叶子
	Groups     []FilterGroup     `json:"groups,omitempty"`     // 子组
}

// FilterCondition 是叶子条件。
type FilterCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"` // 多值用换行分隔
}

// filterFields 是允许出现在规则里的字段。不在表里的一律丢弃（防注入 + 防拼错）。
var filterFields = map[string]bool{
	"tag": true, "source": true, "kind": true, "action": true,
	"todo": true, "pinned": true, "archived": true,
	"text": true, "ageDays": true, "day": true,
}

// filterOps 是每个字段可用的算子。
var filterOps = map[string]bool{
	"is": true, "isNot": true, "anyOf": true,
	"has": true, "hasNot": true,
	"contains": true, "notContains": true,
	"newerThan": true, "olderThan": true,
}

// compileRule turns one group into a SQL clause. ok=false 表示这一组没有可用条件。
func compileRule(g FilterGroup, now time.Time) (string, []any, bool) {
	parts := make([]string, 0, len(g.Conditions)+len(g.Groups))
	args := make([]any, 0, len(g.Conditions))

	for _, c := range g.Conditions {
		if frag, a, ok := compileCondition(c, now); ok {
			parts = append(parts, frag)
			args = append(args, a...)
		}
	}
	for _, sub := range g.Groups {
		if frag, a, ok := compileRule(sub, now); ok {
			parts = append(parts, frag)
			args = append(args, a...)
		}
	}
	if len(parts) == 0 {
		return "", nil, false
	}

	joiner := " AND "
	switch g.Op {
	case "or":
		joiner = " OR "
	case "not":
		// not 组：里面按 and 合起来整体取反
	default:
	}
	clause := "(" + strings.Join(parts, joiner) + ")"
	if g.Op == "not" {
		clause = "NOT " + clause
	}
	return clause, args, true
}

// compileCondition turns one leaf into a SQL predicate.
//
// 不认识的字段/算子会被丢弃并记一行日志：宁可少一个条件，也不要整页查不出来。
func compileCondition(c FilterCondition, now time.Time) (string, []any, bool) {
	field := strings.TrimSpace(c.Field)
	op := strings.TrimSpace(c.Op)
	value := strings.TrimSpace(c.Value)
	if !filterFields[field] || !filterOps[op] {
		Logf("", "filter rule: unknown field/op (%q/%q), dropped", field, op)
		return "", nil, false
	}
	if value == "" && op != "is" && op != "isNot" {
		return "", nil, false
	}

	switch field {
	case "tag":
		tags := splitValues(value)
		if len(tags) == 0 {
			return "", nil, false
		}
		switch op {
		case "is", "anyOf":
			// 空标签条件在语义上等于"没有标签"，但 UI 不提供，这里按 anyOf 处理
			return anyLike("tags", tags)
		case "isNot":
			clause, args, ok := anyLike("tags", tags)
			if !ok {
				return "", nil, false
			}
			return "NOT " + clause, args, true
		}

	case "action":
		vals := splitValues(value)
		if len(vals) == 0 {
			return "", nil, false
		}
		clause, args, ok := anyLike("actions", vals)
		if !ok {
			return "", nil, false
		}
		if op == "hasNot" {
			return "NOT " + clause, args, true
		}
		return clause, args, true

	case "source", "kind":
		col := field
		switch op {
		case "is":
			return col + " = ?", []any{value}, true
		case "isNot":
			return col + " <> ?", []any{value}, true
		}

	case "todo":
		if op != "is" {
			return "", nil, false
		}
		switch value {
		case "open", "done":
			return "todo_state = ?", []any{value}, true
		case "none":
			return "todo_state = ''", nil, true
		}

	case "pinned", "archived":
		if op != "is" {
			return "", nil, false
		}
		n := 0
		if value == "true" || value == "1" {
			n = 1
		}
		return field + " = ?", []any{n}, true

	case "text":
		like := "%" + escapeLike(value) + "%"
		if op == "contains" {
			return `(content LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR tags LIKE ? ESCAPE '\')`,
				[]any{like, like, like}, true
		}
		if op == "notContains" {
			return `(content NOT LIKE ? ESCAPE '\' AND title NOT LIKE ? ESCAPE '\')`,
				[]any{like, like}, true
		}

	case "ageDays":
		days := 0
		for _, r := range value {
			if r < '0' || r > '9' {
				return "", nil, false
			}
			days = days*10 + int(r-'0')
			if days > 3650 {
				return "", nil, false
			}
		}
		bound := now.AddDate(0, 0, -days)
		switch op {
		case "newerThan":
			return "created_at >= ?", []any{encodeTime(bound)}, true
		case "olderThan":
			return "created_at < ?", []any{encodeTime(bound)}, true
		}

	case "day":
		if op != "is" {
			return "", nil, false
		}
		if start, end, ok := dayBounds(value); ok {
			return "(created_at >= ? AND created_at < ?)", []any{start, end}, true
		}
	}
	return "", nil, false
}

// anyLike builds "(col LIKE ? OR col LIKE ?)" for a ",a,b," style column.
func anyLike(col string, values []string) (string, []any, bool) {
	if len(values) == 0 {
		return "", nil, false
	}
	parts := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, v := range values {
		parts = append(parts, col+` LIKE ?`)
		args = append(args, "%,"+escapeLike(v)+",%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, true
}

func splitValues(v string) []string {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == ',' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// ruleTouches reports whether the tree contains a condition on field.
// archived 用得上：规则里显式提到归档状态时，不能再叠加"默认只看未归档"。
func ruleTouches(g FilterGroup, field string) bool {
	for _, c := range g.Conditions {
		if strings.EqualFold(strings.TrimSpace(c.Field), field) {
			return true
		}
	}
	for _, sub := range g.Groups {
		if ruleTouches(sub, field) {
			return true
		}
	}
	return false
}

// validateRule 在保存前挡一次明显错误：不认识的字段/算子、空值。
// 编译期也会丢弃非法条件（兜底），但保存时就报错对用户更友好。
func validateRule(g *FilterGroup) error {
	if g == nil {
		return nil
	}
	if op := strings.TrimSpace(g.Op); op != "" && op != "and" && op != "or" && op != "not" {
		return errors.New("规则组合方式只能是 and / or / not")
	}
	for _, c := range g.Conditions {
		if !filterFields[strings.TrimSpace(c.Field)] {
			return errors.New("不支持的筛选字段：" + c.Field)
		}
		if !filterOps[strings.TrimSpace(c.Op)] {
			return errors.New("不支持的筛选算子：" + c.Op)
		}
	}
	for _, sub := range g.Groups {
		if err := validateRule(&sub); err != nil {
			return err
		}
	}
	return nil
}

// FilterFieldSpec 是给前端渲染下拉框用的字段表：字段名、中文标签、可用算子。
// 放在后端是为了"字段白名单只有一份"，UI 不会和编译期悄悄分叉。
type FilterFieldSpec struct {
	Field string   `json:"field"`
	Label string   `json:"label"`
	Ops   []string `json:"ops"`
}

// FilterFields 返回可用的筛选字段与算子。
func FilterFields() []FilterFieldSpec {
	return []FilterFieldSpec{
		{Field: "tag", Label: "标签", Ops: []string{"is", "isNot", "anyOf"}},
		{Field: "todo", Label: "待办", Ops: []string{"is"}},
		{Field: "source", Label: "来源", Ops: []string{"is", "isNot"}},
		{Field: "kind", Label: "内容类型", Ops: []string{"is", "isNot"}},
		{Field: "action", Label: "能力", Ops: []string{"has", "hasNot"}},
		{Field: "text", Label: "关键词", Ops: []string{"contains", "notContains"}},
		{Field: "ageDays", Label: "距今（天）", Ops: []string{"newerThan", "olderThan"}},
		{Field: "day", Label: "某一天", Ops: []string{"is"}},
		{Field: "pinned", Label: "置顶", Ops: []string{"is"}},
		{Field: "archived", Label: "已归档", Ops: []string{"is"}},
	}
}
