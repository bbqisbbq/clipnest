package clip

import (
	"sort"
	"strings"
)

// 同步包里附件的挑选：预算之内发谁、谁留到下一轮。
//
// 为什么单独抽出来：这条逻辑原来写在 buildOwnPackage 里，按条目顺序边遍历边扣预算，
// 于是**每轮都从同一端开始扣** —— 排在后面的附件永远轮不上（预算 96MB 在测试里
// 造不出来，也就没人发现）。改成「上一轮欠的先还」之后，任何附件最多轮一两轮就能
// 出去。抽成纯函数是为了能用几十字节的预算直接测。

// blobCandidate is one payload that could ride along in this round's package.
//
// key 与 blob 是两个不同的键，别合并：key 是 zip 里的名字（entry-<条目哈希>.png，
// 也是挂账清单与指纹的键），blob 是从库里取字节用的键（memoimg:<附件哈希> 或采集
// 哈希）。第一版只留了一个键，于是拿 zip 名字去库里取字节，一取一个空。
type blobCandidate struct {
	key  string // 包内名字：entry-<hash><ext>
	blob string // 库内键：memoImageToken / 采集哈希
	size int
}

// blobPlan is what planBlobUpload decided.
type blobPlan struct {
	chosen   []blobCandidate // 本轮要发（顺序 = 候选顺序，不排序）
	deferred map[string]bool // 预算不够，下一轮优先发
	oversize int             // 单件就超过整个预算，永远发不出去
}

// planBlobUpload picks payloads that fit the budget.
//
// 优先级：上一轮欠下的（deferred）先发 —— 否则"最新的条目先扣预算"会让同一批
// 附件每轮都占满额度，老附件一辈子出不去，用户看到的是"有些图片就是同步不过来"。
// 同一优先级内保持候选顺序，保证指纹可复现。
func planBlobUpload(cands []blobCandidate, total int, deferred map[string]bool) blobPlan {
	plan := blobPlan{deferred: make(map[string]bool)}
	if total <= 0 {
		for _, c := range cands {
			plan.deferred[c.key] = true
		}
		return plan
	}

	owed := make([]blobCandidate, 0, len(cands))
	rest := make([]blobCandidate, 0, len(cands))
	for _, c := range cands {
		if deferred[c.key] {
			owed = append(owed, c)
		} else {
			rest = append(rest, c)
		}
	}

	remaining := total
	for _, group := range [][]blobCandidate{owed, rest} {
		for _, c := range group {
			switch {
			case c.size > total:
				// 单件就超过整个预算：记成永久放不下，别让它永远占着 deferred 名额。
				plan.oversize++
			case c.size > remaining:
				plan.deferred[c.key] = true
			default:
				plan.chosen = append(plan.chosen, c)
				remaining -= c.size
			}
		}
	}
	return plan
}

// deferredBlobKeys reads the payloads this machine owed the peers last round.
func deferredBlobKeys(meta string) map[string]bool {
	out := make(map[string]bool, 8)
	for _, line := range strings.Split(meta, "\n") {
		if line != "" {
			out[line] = true
		}
	}
	return out
}

// encodeDeferredBlobs serialises the keys for the meta table, capped so a library
// with tens of thousands of images cannot bloat the row indefinitely.
func encodeDeferredBlobs(keys map[string]bool) string {
	const maxKeys = 20000
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > maxKeys {
		out = out[:maxKeys]
	}
	return strings.Join(out, "\n")
}
