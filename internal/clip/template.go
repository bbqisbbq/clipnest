package clip

import (
	"strings"
	"time"
)

// Snippet templates: {{date}}, {{time}}, {{datetime}}, {{date:long}},
// {{clipboard}}, {{cursor}}.
//
// The renderer returns the final text plus the rune offset where the caret should
// stop (the {{cursor}} marker); everything else is substituted in place.

// clipboardTextForTemplate is wired by the service so the template engine can
// read the current clipboard without threading a *Store through it.
var clipboardTextForTemplate func() string

// RenderTemplate substitutes template variables and reports the cursor offset
// (-1 when the template has no {{cursor}}). Exported so the UI can preview it.

func renderTemplate(body string) (string, int) {
	if !strings.Contains(body, "{{") {
		return body, -1
	}
	now := time.Now()
	cursor := -1

	for _, r := range []struct{ key, value string }{
		{"{{date}}", now.Format("2006-01-02")},
		{"{{date:long}}", now.Format("2006年01月02日")},
		{"{{time}}", now.Format("15:04")},
		{"{{datetime}}", now.Format("2006-01-02 15:04")},
	} {
		body = strings.ReplaceAll(body, r.key, r.value)
	}

	if strings.Contains(body, "{{clipboard}}") {
		text := ""
		if clipboardTextForTemplate != nil {
			text = clipboardTextForTemplate()
		}
		body = strings.ReplaceAll(body, "{{clipboard}}", text)
	}

	if i := strings.Index(body, "{{cursor}}"); i >= 0 {
		cursor = len([]rune(body[:i]))
		body = strings.ReplaceAll(body, "{{cursor}}", "")
	}
	return body, cursor
}

// leftArrows converts a cursor offset into "how many ← to send after pasting".
func leftArrows(text string, cursor int) int {
	if cursor < 0 {
		return 0
	}
	total := len([]rune(text))
	if cursor >= total {
		return 0
	}
	return total - cursor
}
