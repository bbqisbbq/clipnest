package clip

// 诊断日志：一行一条、只追加、超限砍半。
//
// 为什么这么土：用户机器上没有调试器，出事时唯一能看的就是这个文件。所以它的写路径
// 绝不能因为磁盘/权限问题把调用方带崩 —— 所有错误都吞掉（宁可少一条日志）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// ---- 日志 ---------------------------------------------------------------

var logDirValue atomic.Value

func logDir() string {
	if v, ok := logDirValue.Load().(string); ok {
		return v
	}
	return ""
}

// Logf appends one diagnostic line to <dir>/clipnest.log.
func Logf(dir, format string, args ...any) {
	if dir == "" {
		dir = logDir()
	}
	if dir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "clipnest.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

// TrimLog keeps the log under limit bytes.
func TrimLog(dir string, limit int64) {
	p := filepath.Join(dir, "clipnest.log")
	fi, err := os.Stat(p)
	if err != nil || fi.Size() <= limit {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	keep := data[len(data)/2:]
	_ = os.WriteFile(p, keep, 0o644)
}

