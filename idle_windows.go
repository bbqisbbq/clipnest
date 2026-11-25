package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// idleWindows：让 picker / capture / shot 这三个弹窗"用完就销毁、下次用到再建"，
// 而不是像换壳前那样常驻隐藏。
//
// 为什么需要它：一个隐藏窗口仍然养着一个 WebView2 文档。换壳前实测 ClipNest
// 11 个进程 724MB，其中 4 个 renderer 占 369MB、均值 92MB——3 个空闲弹窗就是约 280MB。
// 代价是下次唤起要重新加载页面（React 冷启动），所以有闲置宽限期与 skip 列表。
// 换壳（MyGo）后实测：常驻 533–575MB → 448MB（全销毁）/ 销毁 capture+shot、留 picker
// 且 picker 热键 10–25ms；最终默认值是"三个都参与销毁，picker 跳过 1.5s 宽限"，
// 见 docs/design/MYGO-GATE2-REPORT.md。
//
//	CLIPNEST_IDLE_DESTROY=0     关闭（默认开）
//	CLIPNEST_IDLE_MS=1500       闲置多久后销毁（默认 1500ms，够接住"连按两次"）
//	CLIPNEST_IDLE_SKIP=capture  不参与销毁的窗口（默认 picker）
type idleWindows struct {
	enabled bool
	grace   time.Duration
	closing map[string]bool
	timers  map[string]*time.Timer
	// skip 里的窗口不参与闲置销毁（例如热路径的 picker 希望常驻）。
	// 用 CLIPNEST_IDLE_SKIP=picker,capture 指定。
	skip map[string]bool
}

func newIdleWindows() *idleWindows {
	p := &idleWindows{
		grace:   1500 * time.Millisecond,
		closing: map[string]bool{},
		timers:  map[string]*time.Timer{},
	}
	// 默认开（Gate 2.5 实测：省 215MB，picker 常驻所以热路径不变）。
	// 要退回"三窗常驻"就设 CLIPNEST_IDLE_DESTROY=0。
	p.enabled = true
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("CLIPNEST_IDLE_DESTROY"))); v == "0" || v == "false" || v == "off" {
		p.enabled = false
	}
	// picker 默认不参与销毁：它是唯一"按了就得立刻出现"的窗口，重建要重跑 React
	// （实测 132ms 首开 / 77–93ms 之后），热路径上这笔钱不值得省。
	p.skip = map[string]bool{"picker": true}
	if v := os.Getenv("CLIPNEST_IDLE_SKIP"); v != "" {
		p.skip = map[string]bool{}
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				p.skip[name] = true
			}
		}
	}
	if v := os.Getenv("CLIPNEST_IDLE_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			p.grace = time.Duration(ms) * time.Millisecond
		}
	}
	return p
}

// nilIdle 让"还没初始化"的情况也能安全调用。
func (p *idleWindows) on() bool { return p != nil && p.enabled }

// schedule 安排一次闲置销毁。alreadyVisible 用来兜底：到点时窗口又可见了就不销毁。
func (p *idleWindows) schedule(kind string, alreadyVisible func() bool, destroy func()) {
	if !p.on() || p.skip[kind] {
		return
	}
	p.cancel(kind)
	p.timers[kind] = time.AfterFunc(p.grace, func() {
		delete(p.timers, kind)
		if alreadyVisible != nil && alreadyVisible() {
			return
		}
		destroy()
	})
}

// cancel 取消待执行的销毁（窗口又被唤起了）。
func (p *idleWindows) cancel(kind string) {
	if p == nil {
		return
	}
	if t, ok := p.timers[kind]; ok {
		t.Stop()
		delete(p.timers, kind)
	}
}

// markClosing 标记"这次 Close 是我们自己发的"，好让 WindowClosing 钩子放行。
func (p *idleWindows) markClosing(kind string) {
	if p == nil {
		return
	}
	p.closing[kind] = true
}

// allowClose 报告这次关闭是否应当放行（我们自己的销毁请求），并复位标记。
func (p *idleWindows) allowClose(kind string) bool {
	if p == nil {
		return false
	}
	if p.closing[kind] {
		delete(p.closing, kind)
		return true
	}
	return false
}


