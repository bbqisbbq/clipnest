package clip

// 跨文件共用的小工具：时间戳编解码、id 生成、路径/文件引用、占位文案。
// （时间戳必须定宽 —— 见 HISTORY：所有排序与游标都基于字符串比较。）

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ---- 小工具 -------------------------------------------------------------

func encodeTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func decodeTime(v string) time.Time {
	t, err := time.Parse(timeLayout, v)
	if err != nil {
		// 兼容早期写入的变宽格式
		if t2, err2 := time.Parse(time.RFC3339Nano, v); err2 == nil {
			return t2.UTC()
		}
		return time.Time{}
	}
	return t.UTC()
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func encodeFiles(files []FileRef) string {
	if len(files) == 0 {
		return "[]"
	}
	b, err := json.Marshal(files)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// statFile refreshes the volatile metadata of one file reference.
func statFile(ref FileRef) FileRef {
	fi, err := os.Stat(ref.Path)
	if err != nil {
		ref.Exists = false
		return ref
	}
	ref.Exists = true
	ref.Size = fi.Size()
	ref.IsDir = fi.IsDir()
	return ref
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// placeholderForKind gives a text stand-in for captures with no text body.
func placeholderForKind(k Kind) string {
	switch k {
	case KindImage:
		return "（图片）"
	case KindFiles:
		return "（文件）"
	case KindRichText:
		return "（富文本）"
	case KindCustom:
		return "（自定义格式）"
	default:
		return ""
	}
}
