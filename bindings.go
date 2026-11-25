package main

import (
	"errors"

	"github.com/bbqisbbq/clipnest/internal/clip"
	"github.com/bbqisbbq/clipnest/internal/expand"
)

// clipAPI 是给 MyGo 绑定用的外观。
//
// MyGo 的 Bind 要求方法形如 `func(...) (T, error)`（或没有返回值）：一绑定就会
// 在启动时 panic —— "the second result must be an error"。而 ClipNest 有五个方法
// 返回 2–4 个值：
//
//	CaptureSize() (int, int)
//	ShotBounds() (left, top, width, height int)
//	OcrEngineStatus() (chosen string, ppocrReady bool, reason string)
//	Match(buf string) (expand.Trigger, int, bool)
//	Render(t expand.Trigger) (string, int)
//
// 这五个里，前三个只有 Go 侧在用、后两个只有测试在用（已 grep 确认前端 0 处调用），
// 所以在这里**遮蔽**掉即可：内嵌的服务把其余 40 多个 (T, error) 方法原样提升，
// internal/clip 一行都不用改。
type clipAPI struct{ *clip.ClipService }

// CaptureSize 是「记一笔」窗口的持久尺寸。
type CaptureSize struct {
	W int
	H int
}

// ShotRect 是截图遮罩要铺的那块显示器的物理像素矩形。
type ShotRect struct {
	Left   int
	Top    int
	Width  int
	Height int
}

// OcrStatus 是 OCR 引擎的当前选择与可用性。
type OcrStatus struct {
	Chosen     string
	PPOCRReady bool
	Reason     string
}

// TriggerMatch 是内联展开器的一次命中。
type TriggerMatch struct {
	Trigger     expand.Trigger
	ConsumedLen int
	Ok          bool
}

func (c clipAPI) CaptureSize() (CaptureSize, error) {
	w, h := c.ClipService.CaptureSize()
	return CaptureSize{W: w, H: h}, nil
}

func (c clipAPI) ShotBounds() (ShotRect, error) {
	left, top, w, h := c.ClipService.ShotBounds()
	return ShotRect{Left: left, Top: top, Width: w, Height: h}, nil
}

func (c clipAPI) OcrEngineStatus() (OcrStatus, error) {
	chosen, ready, reason := c.ClipService.OcrEngineStatus()
	return OcrStatus{Chosen: chosen, PPOCRReady: ready, Reason: reason}, nil
}

func (c clipAPI) Match(buf string) (TriggerMatch, error) {
	t, n, ok := c.ClipService.Match(buf)
	return TriggerMatch{Trigger: t, ConsumedLen: n, Ok: ok}, nil
}

// RenderResult 对应原来的 (string, int)：文本 + 光标偏移（-1 表示"别管光标"）。
type RenderResult struct {
	Text  string
	Caret int
}

func (c clipAPI) Render(t expand.Trigger) (RenderResult, error) {
	s, n := c.ClipService.Render(t)
	return RenderResult{Text: s, Caret: n}, nil
}
// SetOwnWindowCheck 是宿主注入"前台窗口是不是自己"的判断，参数是 func——MyGo 没法
// 绑定（JSON 编码不了函数）。真服务上由 main 直接调用，页面这边遮蔽成一个会报错的
// 空实现即可。
func (c clipAPI) SetOwnWindowCheck() error {
	return errors.New("SetOwnWindowCheck 只能在 Go 侧调用")
}

