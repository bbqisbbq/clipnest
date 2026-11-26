package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/bbqisbbq/clipnest/internal/embed"
)

// 语义向量：**旁表**，不是 entries 的一列。
//
// 为什么另开一张表：
//   · `entries` 是同步与哈希的单一真源（entryColumns / scanEntry / INSERT / 合并 /
//     指纹全围着它转）。往里加三列要同时改五六处，每处漏一个就是"存住了但读不出来"，
//     而收益只是省一次 JOIN。
//   · 向量是**本机派生物**：它不进同步包、不参与内容哈希、换台机器重算 6ms/条。
//     放进旁表之后，"不同步它"这件事天然成立，不需要额外白名单。
//
// 失效靠 hash：存的是"编码时那段正文的哈希"，正文/OCR/标签任何一处变了就对不上，
// 下一次扫描会发现并重编（见 embed_worker.go 的 reconcileEmbed）。

// vectorSchema 只建表建索引，不碰 entries。
const vectorSchema = `
CREATE TABLE IF NOT EXISTS entry_vectors (
  id    TEXT PRIMARY KEY,
  -- 编码时那段可搜索正文的哈希（含 content + ocr_text + tags）
  hash  TEXT NOT NULL DEFAULT '',
  -- '' | pending | done | failed | skipped
  state TEXT NOT NULL DEFAULT '',
  -- embed.BlobLen 字节（4 字节 scale + 512 字节 int8）
  vec   BLOB NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_entry_vectors_state ON entry_vectors(state);
`

// 向量状态取值（镜像 OcrState* 那套，界面用同一套措辞）。
const (
	// VectorStatePending 已入队，还没结果。
	VectorStatePending = "pending"
	// VectorStateDone 编码完成。
	VectorStateDone = "done"
	// VectorStateFailed 试过了但失败（模型报错/资源被删）。
	VectorStateFailed = "failed"
	// VectorStateUnsupported 这台机器没有可用引擎（没下模型 / 没 cgo）。
	VectorStateUnsupported = "unsupported"
	// VectorStateSkipped 没有可编码的正文（纯图片且没 OCR 出字）。
	VectorStateSkipped = "skipped"
)

// VectorRow 是一条向量记录。
type VectorRow struct {
	Hash  string
	State string
	Vec   []byte
}

// SaveEntryVector 写入（或覆盖）一条向量。
//
// **vec 传 nil 时按空 BLOB 存**：`vec` 是 `BLOB NOT NULL`，而 Go 的 nil []byte 绑到
// SQLite 上是 NULL，会被 NOT NULL 拒掉——pending/failed/skipped 这些"还没有向量"的
// 状态就一条都写不进去。2.13.0 就这么翻的车：设置页永远显示「已索引 0 条」，
// 而编码路径上那次被忽略的错误把真相吞掉了（见 embed_vector_nil_test.go）。
func (s *Store) SaveEntryVector(id, hash, state string, vec []byte) error {
	if vec == nil {
		vec = []byte{}
	}
	_, err := s.db.Exec(
		`INSERT INTO entry_vectors (id, hash, state, vec) VALUES (?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET hash=excluded.hash, state=excluded.state, vec=excluded.vec`,
		id, hash, state, vec)
	if err != nil {
		Logf(s.dir, "save entry vector(%s) failed: %v", id, err)
	}
	return err
}

// EntryVector 读一条向量记录。
func (s *Store) EntryVector(id string) (VectorRow, bool) {
	var r VectorRow
	err := s.db.QueryRow(`SELECT hash, state, vec FROM entry_vectors WHERE id = ?`, id).
		Scan(&r.Hash, &r.State, &r.Vec)
	if err != nil {
		return VectorRow{}, false
	}
	return r, true
}

// DeleteEntryVector 删一条向量（条目被删时调；不存在是幂等空操作）。
func (s *Store) DeleteEntryVector(id string) {
	if _, err := s.db.Exec(`DELETE FROM entry_vectors WHERE id = ?`, id); err != nil {
		Logf(s.dir, "delete entry vector(%s) failed: %v", id, err)
	}
}

// SettledVectorHashes 返回所有"已经有结论"的 id → hash（done + skipped）。
//
// 为什么把 skipped 也算进来：没有可编码正文的条目（纯图片且 OCR 没认出字）会被记成
// skipped，它的 hash 同样有效。只算 done 的话，这类条目每一轮对账都会重新入队一次
// —— 白白刷一遍日志与 CPU。更要紧的是：**已经带着旧向量的 done 行必须能被重算**，
// 所以调用方不能"看到正文为空就跳过"，那是两回事（见 reconcileEmbed）。
func (s *Store) SettledVectorHashes() map[string]string {
	rows, err := s.db.Query(
		`SELECT id, hash FROM entry_vectors WHERE state IN (?, ?)`, VectorStateDone, VectorStateSkipped)
	if err != nil {
		Logf(s.dir, "list vector hashes failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make(map[string]string, 256)
	for rows.Next() {
		var id, hash string
		if rows.Scan(&id, &hash) == nil {
			out[id] = hash
		}
	}
	return out
}

// LiveVectors 返回所有可参与检索的（条目还活着、非归档）向量。
//
// 一次全量读：1 万条约 5MB，读+反序列化在十毫秒级；比"先查 FTS 再反查向量"
// 少一次往返，也避免了候选集不够的问题（见 docs/design/SEMANTIC-SEARCH.md 的扫描实测）。
func (s *Store) LiveVectors() []VectorHit {
	rows, err := s.db.Query(`
		SELECT v.id, v.vec
		  FROM entry_vectors v JOIN entries e ON e.id = v.id
		 WHERE v.state = ? AND e.archived = 0`, VectorStateDone)
	if err != nil {
		Logf(s.dir, "load vectors failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]VectorHit, 0, 1024)
	for rows.Next() {
		var h VectorHit
		if rows.Scan(&h.ID, &h.Blob) == nil && len(h.Blob) > 0 {
			out = append(out, h)
		}
	}
	return out
}

// VectorHit 是检索用的一条（id + 序列化向量）。
type VectorHit struct {
	ID   string
	Blob []byte
}

// VectorStats 给设置页的进度用。
func (s *Store) VectorStats() (done, pending, failed int) {
	rows, err := s.db.Query(`SELECT state, COUNT(*) FROM entry_vectors GROUP BY state`)
	if err != nil {
		return 0, 0, 0
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int
		if rows.Scan(&state, &n) != nil {
			continue
		}
		switch state {
		case VectorStateDone:
			done = n
		case VectorStatePending:
			pending = n
		case VectorStateFailed:
			failed = n
		}
	}
	return done, pending, failed
}

// entryVectorHashFields 是"什么变了要重编"的等价物。
//
// **哈希算的是"将被编码的那段正文"（entryVectorText），不是原始 content**：
// 这样连"提取规则本身改了"（比如这次把图片令牌剥掉）也会让 hash 对不上，
// 老库里的垃圾向量会被自动重算 —— 否则规则一改，历史数据永远停在旧口径上。
func entryVectorHashFields(e Entry) string {
	sum := sha256.Sum256([]byte("vec\x00" + entryVectorText(e)))
	return hex.EncodeToString(sum[:])
}

// FTSIDs 是字面那一路：与 where() 里完全相同的短语匹配，按 bm25 名次排。
func (s *Store) FTSIDs(text string, limit, offset int) []string {
	if limit <= 0 {
		limit = 100
	}
	phrase := `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
	rows, err := s.db.Query(
		`SELECT id FROM entries_fts WHERE entries_fts MATCH ? ORDER BY rank LIMIT ? OFFSET ?`,
		phrase, limit, offset)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// NearestIDs 是语义那一路：全量扫描取最近的 limit 条。
//
// 全量而不是"先粗筛"：1 万条 4.8ms（int8 点积，见设计文档实测），
// 比任何索引/近似最近邻的维护成本都低。5 万条也才 25ms。
func (s *Store) NearestIDs(q embed.Vector, limit int) []string {
	if limit <= 0 {
		limit = 100
	}
	hits := s.LiveVectors()
	type pair struct {
		id    string
		score float32
	}
	best := make([]pair, 0, len(hits))
	for _, h := range hits {
		if score, ok := embed.DotBlob(h.Blob, q); ok {
			best = append(best, pair{id: h.ID, score: score})
		}
	}
	sort.Slice(best, func(i, j int) bool {
		if best[i].score == best[j].score {
			return best[i].id < best[j].id
		}
		return best[i].score > best[j].score
	})
	if len(best) > limit {
		best = best[:limit]
	}
	out := make([]string, len(best))
	for i, b := range best {
		out[i] = b.id
	}
	return out
}
