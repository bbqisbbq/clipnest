package clip

// 语义向量的编码队列 + 引擎生命周期。
//
// 和 OCR 那套（ocr_worker.go）同构，但多了两件这里独有的事：
//
//  1. **引擎不能常驻**。实测模型会话驻留 +43MB（docs/design/SEMANTIC-SEARCH.md），
//     而应用好不容易才把内存从 753MB 压到 120~180MB。所以：
//     第一次真需要（建索引或语义查询）时才加载，之后闲置 embedIdleRelease 就释放，
//     下次用再加载（冷加载 0.25 秒，比常驻 43MB 划算）。
//
//  2. **失效靠对账而不是靠钩子**。向量是派生物，正文/OCR/标签任一变化都会让
//     hash 对不上。钩子（保存、OCR 完成、同步合并）都挂一遍太容易漏，所以
//     启动时 + 每次巡检都做一次全量对账：读 id+hash（1 万条约 5MB，十毫秒级），
//     对不上就重新入队。漏挂钩子的后果只是"晚一会儿才更新"，不是"永远搜不到"。

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bbqisbbq/clipnest/internal/embed"
)

const (
	// embedQueueSize 是队列上限。满了就丢：少编几条远好过把内存堆起来。
	embedQueueSize = 128
	// embedIdleRelease 是"最后一次用完之后多久释放会话"。
	// 5 分钟：搜一次通常会连着搜几次，太短会反复付 0.25 秒的冷加载。
	embedIdleRelease = 5 * time.Minute
	// embedBackfillDelay 是启动后多久开始补扫（刚开机界面在抢 CPU 和磁盘）。
	embedBackfillDelay = 20 * time.Second
	// embedBackfillSleep 是补扫时每条之间的小睡：建 1 万条要一分钟，
	// 别让它在用户打字时把 CPU 吃满。
	embedBackfillSleep = 12 * time.Millisecond
)

// EmbedAssetsDir 返回放语义模型（onnxruntime.dll / embed_model.onnx / embed_vocab.txt）的目录。
func (s *ClipService) EmbedAssetsDir() string {
	return filepath.Join(s.store.Dir(), "embed")
}

// embedEnabled 报告"该不该用语义搜索"：用户开启 + 这个构建带引擎。
//
// 资源在不在不在这里判断 —— 那是懒加载时的事（Ready 会给出具体缺什么）。
func (s *ClipService) embedEnabled() bool {
	if !embed.CompiledIn() {
		return false
	}
	return s.store.Settings().EmbedSearch
}

// embedEngine 返回懒加载的引擎实例（不加载模型，只是拿到对象）。
func (s *ClipService) embedEngine() *embed.Engine {
	s.embedOnce.Do(func() {
		s.embedEng = embed.New(s.EmbedAssetsDir())
	})
	return s.embedEng
}

// encodeVector 编码一段文本，并把"闲置释放"的计时器往后推。
//
// 编码 2.2ms；慢的是第一次的模型加载（0.25 秒）—— 那一次会顺带把会话建起来。
func (s *ClipService) encodeVector(text string) (embed.Vector, error) {
	eng := s.embedEngine()
	v, err := eng.Encode(text)
	s.touchEmbed()
	return v, err
}

// touchEmbed 重置闲置释放定时器。
func (s *ClipService) touchEmbed() {
	s.embedMu.Lock()
	defer s.embedMu.Unlock()
	if s.embedIdle == nil {
		s.embedIdle = time.AfterFunc(embedIdleRelease, s.releaseEmbed)
		return
	}
	s.embedIdle.Reset(embedIdleRelease)
}

// releaseEmbed 把会话销毁，内存还回去。
func (s *ClipService) releaseEmbed() {
	s.embedMu.Lock()
	eng := s.embedEng
	s.embedMu.Unlock()
	if eng != nil && eng.Loaded() {
		eng.Close()
		Logf(s.store.Dir(), "embed: 闲置 %v，模型会话已释放（+43MB 还回去了）", embedIdleRelease)
	}
}

// stopEmbedIdleTimer 停掉定时器（Shutdown 用）。
func (s *ClipService) stopEmbedIdleTimer() {
	s.embedMu.Lock()
	defer s.embedMu.Unlock()
	if s.embedIdle != nil {
		s.embedIdle.Stop()
		s.embedIdle = nil
	}
}

// startEmbedWorker 起后台 worker 并安排一次对账。由 Start 调一次。
func (s *ClipService) startEmbedWorker() {
	if !embed.CompiledIn() {
		Logf(s.store.Dir(), "embed: 这个构建没有语义引擎，检索只用 FTS")
		return
	}
	s.embedOnce.Do(func() { s.embedEng = embed.New(s.EmbedAssetsDir()) })
	s.embedQueue = make(chan string, embedQueueSize)
	s.bg.Add(1)
	go s.embedWorker()
	go s.backfillEmbed()
}

// enqueueEmbed asks the worker to (re)encode one entry (non-blocking).
func (s *ClipService) enqueueEmbed(id string) {
	if s.embedQueue == nil || id == "" {
		return
	}
	select {
	case s.embedQueue <- id:
	default:
		// 队列满了：不记日志（一次粘贴风暴会刷屏），对账那一轮会补上
	}
}

func (s *ClipService) embedWorker() {
	defer s.bg.Done()
	for {
		select {
		case <- s.bgCtx.Done():
			return
		case id, ok := <-s.embedQueue:
			if !ok {
				return
			}
			if err := s.runEmbedForEntry(id); err != nil {
				Logf(s.store.Dir(), "embed: entry %s failed: %v", id, err)
			}
		}
	}
}

// backfillEmbed 启动后把历史条目对一遍账（后台，慢点没关系）。
func (s *ClipService) backfillEmbed() {
	if !sleepCtx(s.bgCtx, embedBackfillDelay) {
		return
	}
	ids := s.reconcileEmbed(0)
	if len(ids) == 0 {
		return
	}
	Logf(s.store.Dir(), "embed: backfilling %d entries", len(ids))
	for _, id := range ids {
		if s.bgCtx.Err() != nil {
			return
		}
		s.enqueueEmbed(id)
		// 给用户的操作让路：建索引不值得把 CPU 打满
		if !sleepCtx(s.bgCtx, embedBackfillSleep) {
			return
		}
	}
}

// reconcileEmbed 找出"向量缺失/过期"的条目，返回需要（重新）编码的 id。
//
// limit<=0 表示不设上限。它只读 entries 与 entry_vectors，不做任何计算密集的事
// —— 哈希是 sha256，1 万条在十毫秒级。
//
// **资源不齐时直接返回**：没有模型的时候把几百条排进队列，只会得到几百行
// `failed` 和几百条日志（2.13.0 实测：一次补扫刷了 15 行"资源不全"）。
// 资源齐了自然会被下一轮对账（启动 / 同步后 / 刚下载完）捡起来。
func (s *ClipService) reconcileEmbed(limit int) []string {
	if embed.Ready(s.EmbedAssetsDir()) != nil {
		return nil
	}
	have := s.store.SettledVectorHashes()
	out := make([]string, 0, 64)
	// 遍历活着的条目（归档的不进检索，不必编码）
	rows, err := s.store.db.Query(`SELECT ` + entryColumns + ` FROM entries WHERE archived = 0`)
	if err != nil {
		Logf(s.store.Dir(), "embed reconcile: query entries failed: %v", err)
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			continue
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		// 只看 hash，**不看正文是否为空**：正文为空但有旧向量的条目（比如图片令牌
		// 还没被剥掉时编出来的那些）正是最需要重算的 —— 它们必须被改成 skipped、
		// 把垃圾向量清掉。以前这里有一句"text == \"\" 就 continue"，于是这类条目
		// 永远停在 done + 垃圾向量的状态，继续霸占语义结果（2.13.1 的实测）。
		if have[e.ID] == entryVectorHashFields(e) {
			continue
		}
		out = append(out, e.ID)
	}
	return out
}

// runEmbedForEntry 编码一条并落库。hash 没变就只把状态修一下，不重算。
func (s *ClipService) runEmbedForEntry(id string) error {
	e, ok := s.store.GetEntry(id)
	if !ok {
		// 条目已经没了：把向量一并清掉
		s.store.DeleteEntryVector(id)
		return nil
	}
	text := entryVectorText(e)
	hash := entryVectorHashFields(e)
	if text == "" {
		return s.store.SaveEntryVector(id, hash, VectorStateSkipped, nil)
	}
	if cur, ok := s.store.EntryVector(id); ok && cur.State == VectorStateDone && cur.Hash == hash {
		return nil // 内容没变，别浪费 2ms
	}
	if !s.embedEnabled() {
		return s.store.SaveEntryVector(id, hash, VectorStateUnsupported, nil)
	}
	// 资源不齐不算"这条条目失败"：那是全局状态，界面上有一行提示就够了。
	// 所以标 unsupported 而不是 failed，也不往外抛错（不刷日志）。
	if err := embed.Ready(s.EmbedAssetsDir()); err != nil {
		return s.store.SaveEntryVector(id, hash, VectorStateUnsupported, nil)
	}
	if err := s.store.SaveEntryVector(id, hash, VectorStatePending, nil); err != nil {
		// 这个错误以前被 `_ =` 吞掉，结果 2.13.0 的 NOT NULL bug 藏了整整一版。
		// 写不进状态就是存储出了问题，必须冒出来。
		return fmt.Errorf("标记 pending 失败: %w", err)
	}
	v, err := s.encodeVector(text)
	if err != nil {
		_ = s.store.SaveEntryVector(id, hash, VectorStateFailed, nil)
		return err
	}
	return s.store.SaveEntryVector(id, hash, VectorStateDone, v.Blob())
}

// memoImgRE 匹配正文里的图片令牌（`![](memoimg:abc)` 与 `<img src="memoimg:abc">`）。
//
// 为什么要把它们抠掉：图片条目的正文**只有**这个令牌，剥掉之后就什么都不剩了
// （真正的内容在 OCR 文字里）。不抠的话每条图片都被编码成"一串哈希字符"的向量，
// 它们彼此高度相似、和任何查询都半像不像，于是霸占语义结果的头部 —— 实测用户库里
// 一次「草稿丢了怎么办」返回的前五条里四条是这种空图。
var memoImgRE = regexp.MustCompile(`!\[[^\]]*\]\(\s*memoimg:[^)]*\)|<img[^>]*src\s*=\s*["']memoimg:[^"']*["'][^>]*>`)

// entryVectorText 是"会被编码的那段正文"：正文 + OCR 文字 + 标签。
//
// 图片令牌剥掉；剥完什么都不剩的条目返回空串 —— 调用方据此记 skipped，
// 别拿噪声去污染向量空间（OCR 认出字之后它自然会变成可编码的）。
func entryVectorText(e Entry) string {
	parts := make([]string, 0, 3)
	if t := strings.TrimSpace(memoImgRE.ReplaceAllString(e.Content, " ")); t != "" {
		parts = append(parts, t)
	}
	if t := strings.TrimSpace(e.OcrText); t != "" {
		parts = append(parts, t)
	}
	if len(e.Tags) > 0 {
		parts = append(parts, strings.Join(e.Tags, " "))
	}
	return strings.Join(parts, "\n")
}
