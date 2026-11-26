package clip

// 混合检索：字面（FTS trigram）+ 语义（向量）→ 名次融合（RRF）。
//
// 为什么融合而不是"语义优先"：两条路各命中各的。
//   · 字面擅长"我记得里面有个 proxy_pass"——那种查询向量反而会漂；
//   · 语义擅长"云端传不上去"——这种查询字面一条都找不到（实测 0/39）。
// 实测里还出现过：短查询在一堆长得很像的命令行条目之间挑错人（`看服务日志`
// 把 journalctl 排到第 34 名）。保留字面那一路的名次，正是为了兜住这种时候。
//
// 为什么用 RRF 而不是分数加权：**绝对相似度不可用**。语料里根本没有答案的查询
// 最高分也到了 0.45~0.49，而正确答案里有 0.35 的（见设计文档）。分数不可比，
// 名次可比。RRF 只用名次，天然免疫分数标定问题。
//
// RRF: score(d) = Σ 1/(k + rank_i(d))，k=60 是论文里的常用值（对名次差异不敏感）。

import (
	"sort"

	"github.com/bbqisbbq/clipnest/internal/embed"
)

const (
	// rrfK 是 RRF 的平滑常数（小 => 更看重头部名次）。
	rrfK = 60
	// hybridLegLimit 是每一路各取多少候选。
	hybridLegLimit = 100
	// hybridTotalLimit 是融合后最多保留多少条。
	//
	// 上限卡在 200 是因为这个列表要变成 SQL 的 `id IN (…)`：SQLite 的
	// 绑定变量数有上限（新版 32766，老版 999），200 两边都安全。
	hybridTotalLimit = 200
)

// embedReady 报告语义这一路现在能不能用（用户开启 + 引擎在 + 资源齐全）。
//
// 只 stat 三个文件，几十微秒，所以不缓存 —— 缓存反而要处理"用户刚删了模型"
// 这种情况，得不偿失。
func (s *ClipService) embedReady() bool {
	if !s.embedEnabled() {
		return false
	}
	return embed.Ready(s.EmbedAssetsDir()) == nil
}

// searchHybrid 跑两路检索并融合，返回排好序的条目 id。
//
// 语义那一路没准备好时返回 ok=false，调用方原样走老的字面路径 —— 这就是
// "模型没下载时行为完全不变"的降级点。
func (s *ClipService) searchHybrid(text string) (ids []string, ok bool) {
	if !s.embedReady() {
		return nil, false
	}
	// 字面那一路：和 where() 里完全一样的短语查询，保证"开语义之前能搜到的，
	// 开了之后还在"。
	literal := s.store.FTSIDs(text, hybridLegLimit, 0)

	// 语义那一路：全量扫描（1 万条 4.8ms，见设计文档；不需要向量库）。
	qvec, err := s.encodeVector(text)
	if err != nil {
		Logf(s.store.Dir(), "embed: 查询编码失败，本次只用字面（%v）", err)
		return nil, false
	}
	semantic := s.store.NearestIDs(qvec, hybridLegLimit)

	if len(literal) == 0 && len(semantic) == 0 {
		return nil, true // 两路都没有：返回"确实空"，不是降级
	}
	return fuseRRF(literal, semantic), true
}

// fuseRRF 把若干"按名次排好的 id 列表"融合成一个。抽成纯函数是为了能单独测：
// 融合公式错一位，搜索结果就会莫名其妙地偏，而这种偏差很难在界面上看出来。
func fuseRRF(legs ...[]string) []string {
	score := make(map[string]float64, 128)
	for _, list := range legs {
		for rank, id := range list {
			score[id] += 1.0 / float64(rrfK+rank+1)
		}
	}
	ids := make([]string, 0, len(score))
	for id := range score {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if score[ids[i]] == score[ids[j]] {
			return ids[i] < ids[j] // 稳定：同分按 id，免得每次顺序都不同
		}
		return score[ids[i]] > score[ids[j]]
	})
	if len(ids) > hybridTotalLimit {
		ids = ids[:hybridTotalLimit]
	}
	return ids
}
