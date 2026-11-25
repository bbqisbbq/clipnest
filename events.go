package main

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

// 事件从 换壳前 Wails 的 application.RegisterEvent[T] 换成 MyGo 的包级带类型事件。
//
// 名字与载荷**一字不改**：前端的 EventNames 表、订阅点、以及 e2e 脚本里对日志/行为的
// 断言都依赖这些名字（`clip:new`、`settings:changed`…）。改壳不改契约。
var (
	evClipNew       = mygo.NewEvent[clip.Entry]("clip:new")
	evClipBumped    = mygo.NewEvent[clip.Entry]("clip:bumped")
	evClipUpdated   = mygo.NewEvent[clip.Entry]("clip:updated")
	evClipDeleted   = mygo.NewEvent[string]("clip:deleted")
	evClipCleared   = mygo.NewEvent[int]("clip:cleared")
	evClipStats     = mygo.NewEvent[clip.Stats]("clip:stats")
	evSettings      = mygo.NewEvent[clip.Settings]("settings:changed")
	evSyncStatus    = mygo.NewEvent[clip.SyncStatus]("sync:status")
	evPickerOpened  = mygo.NewEvent[string]("picker:opened")
	evPickerFailed  = mygo.NewEvent[string]("picker:paste-failed")
	evCaptureOpened = mygo.NewEvent[string]("capture:opened")
	evShotOpened    = mygo.NewEvent[string]("shot:opened")
	evNavigate      = mygo.NewEvent[string]("ui:navigate")
	evUpdate        = mygo.NewEvent[clip.UpdateInfo]("update:available")
	evOCRProgress   = mygo.NewEvent[clip.OCRProgress]("ocr:progress")
	evAppHidden     = mygo.NewEvent[string]("app:hidden")
)

// emit 是给 clip.Hooks.Emit 用的：服务层只认事件名 + any，这里派发到上面那些
// 带类型的事件上。MyGo 的事件要具体类型，所以必须在这里做一次断言。
//
// 认不出来的名字不静默吞掉（Wails 那套曾经因为"没注册"而让前端收不到事件，
// 排查过一次），直接写日志。
func emit(name string, data any) {
	ok := true
	switch name {
	case "clip:new":
		_ = evClipNew.Broadcast(as[clip.Entry](data))
	case "clip:bumped":
		_ = evClipBumped.Broadcast(as[clip.Entry](data))
	case "clip:updated":
		_ = evClipUpdated.Broadcast(as[clip.Entry](data))
	case "clip:deleted":
		_ = evClipDeleted.Broadcast(as[string](data))
	case "clip:cleared":
		_ = evClipCleared.Broadcast(as[int](data))
	case "clip:stats":
		_ = evClipStats.Broadcast(as[clip.Stats](data))
	case "settings:changed":
		_ = evSettings.Broadcast(as[clip.Settings](data))
	case "sync:status":
		_ = evSyncStatus.Broadcast(as[clip.SyncStatus](data))
	case "picker:opened":
		_ = evPickerOpened.Broadcast(as[string](data))
	case "picker:paste-failed":
		_ = evPickerFailed.Broadcast(as[string](data))
	case "capture:opened":
		_ = evCaptureOpened.Broadcast(as[string](data))
	case "shot:opened":
		_ = evShotOpened.Broadcast(as[string](data))
	case "ui:navigate":
		_ = evNavigate.Broadcast(as[string](data))
	case "update:available":
		_ = evUpdate.Broadcast(as[clip.UpdateInfo](data))
	case "ocr:progress":
		_ = evOCRProgress.Broadcast(as[clip.OCRProgress](data))
	case "app:hidden":
		_ = evAppHidden.Broadcast(as[string](data))
	default:
		ok = false
	}
	if !ok {
		log.Printf("emit: 未知事件名 %q（没有对应的事件声明）", name)
	}
}

// as 把 any 收成期望的类型；类型不对时给零值（调用方本来也拿不到更细的信息）。
func as[T any](v any) T {
	typed, _ := v.(T)
	return typed
}


