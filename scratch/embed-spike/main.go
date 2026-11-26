package main

// 语义搜索 spike：量三个数，决定要不要做。
//
//	go run ./scratch/embed-spike -cmd tokens -text "部署 nginx 反代"
//	go run ./scratch/embed-spike -cmd bench
//	go run ./scratch/embed-spike -cmd dump  -n 25
//	go run ./scratch/embed-spike -cmd search -text "记住过的那段 nginx 配置"
//
// 量的东西（对应 docs 里要写的结论）：
//   1. 单条编码耗时（剪贴板条目量级：几十到几百字）；
//   2. 10k × 512 维暴力扫描耗时（决定"要不要引向量库"这个问题是不是伪问题）；
//   3. 真实库上的检索质量：同一句查询，FTS(trigram) 与向量各自返回什么。
//
// 不连产品代码：模型、词表、库路径全从参数/环境来，量完就删。

import (
	"database/sql"
	"encoding/gob"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	ort "github.com/yalue/onnxruntime_go"
)

// holdBare 只把 onnxruntime 环境点起来（不建会话），用来量"模型的边际内存"。
// 产品里 onnxruntime.dll 本来就因为 PP-OCR 会被加载，所以只有会话+权重的增量算新增开销。
func holdBare(lib string) {
	ort.SetSharedLibraryPath(lib)
	if err := ort.InitializeEnvironment(); err != nil {
		check(err, "onnxruntime 初始化")
	}
	fmt.Printf("PID %d ready（只有 onnxruntime，没有模型；60 秒后退出）\n", os.Getpid())
	time.Sleep(60 * time.Second)
}

func main() {
	var (
		cmd   = flag.String("cmd", "bench", "tokens|bench|dump|search")
		text  = flag.String("text", "", "查询或待编码文本")
		n     = flag.Int("n", 10, "dump/search 的条数与返回条数")
		lib   = flag.String("lib", "", "onnxruntime.dll 路径（默认复用已装的 OCR 资源）")
		model = flag.String("model", "", "model_quantized.onnx 路径")
		vocab = flag.String("vocab", "", "vocab.txt 路径")
		dbDir = flag.String("db", "", "真实库目录（含 clipnest.db）")
		pool  = flag.String("pool", "cls", "池化：cls|mean")
		instr = flag.Bool("instr", false, "查询侧加 bge 官方检索指令（见 queryInstruction）")
		quant = flag.Bool("quant", false, "把向量压成 int8 再排序（验证 512B/条 是否够用）")
	)
	flag.Parse()

	here, _ := os.Getwd()
	def := func(rel string) string { return filepath.Join(here, rel) }
	if *model == "" {
		*model = def("scratch/embed-model/model_quantized.onnx")
	}
	if *vocab == "" {
		*vocab = def("scratch/embed-model/vocab.txt")
	}
	if *lib == "" {
		*lib = filepath.Join(os.Getenv("APPDATA"), "ClipNest", "ocr", "onnxruntime.dll")
	}

	v, err := LoadVocab(*vocab)
	check(err, "读词表")

	if *cmd == "holdbare" {
		holdBare(*lib)
		return
	}
	if *cmd == "tokens" {
		ids, _ := v.Encode(*text, 512)
		toks := make([]string, len(ids))
		for i, id := range ids {
			toks[i] = v.Token[id]
		}
		fmt.Printf("text   = %s\n", *text)
		fmt.Printf("norm   = %q\n", Normalize(*text))
		fmt.Printf("tokens = %v\n", PreTokenize(Normalize(*text)))
		fmt.Printf("ids    = %v\n", ids)
		fmt.Printf("pieces = %v\n", toks)
		return
	}

	eng, err := OpenEngine(*lib, *model, *vocab)
	check(err, "加载模型")
	defer eng.Close()
	fmt.Printf("模型就绪（加载 %.2fs，池化=%s）\n\n", eng.LoadSeconds, *pool)

	switch *cmd {
	case "bench":
		runBench(eng, v, *pool, *dbDir)
	case "dump":
		for i, it := range loadCorpus(*dbDir) {
			if i >= *n {
				break
			}
			fmt.Printf("[%02d] %s\n     %s\n", i, it.title(), oneLine(it.Content, 110))
		}
	case "hold":
		// 加载模型 + 跑一批编码后挂住，供外部量 PeakWorkingSet（RSS）。
		// 产品的内存预算是 120~180MB，这块要么挤进去，要么就得做懒加载/释放。
		texts := make([]string, 0, 8)
		for _, c := range evalSet {
			texts = append(texts, c.Text)
		}
		for i := 0; i < 200; i++ {
			_, err := eng.Encode(v, texts[i%len(texts)], *pool)
			check(err, "编码")
		}
		fmt.Printf("PID %d ready（编码 200 条后驻留，60 秒后退出）\n", os.Getpid())
		time.Sleep(60 * time.Second)
	case "eval":
		runEval(eng, v, *pool, *instr, *quant)
	case "search":
		runSearch(eng, v, *pool, *dbDir, *text, *n)
	default:
		fmt.Println("未知 -cmd")
	}
}

// ---- 引擎 ---------------------------------------------------------------

// Engine 是一个 bge-small-zh 推理会话（512 维，CLS 池化 + L2 归一）。
type Engine struct {
	session     *ort.DynamicAdvancedSession
	hasTypeIDs  bool
	dim         int
	LoadSeconds float64
}

// OpenEngine 初始化 onnxruntime 并打开模型。
//
// 与 ppocr 共用同一个 onnxruntime.dll：**环境是进程级全局的，只能初始化一次**，
// 所以这里不销毁环境（ppocr 那边有同样的约束，见 internal/ppocr/engine_cgo.go）。
func OpenEngine(lib, model, vocabPath string) (*Engine, error) {
	if _, err := os.Stat(lib); err != nil {
		return nil, fmt.Errorf("找不到 onnxruntime.dll：%s", lib)
	}
	if _, err := os.Stat(model); err != nil {
		return nil, fmt.Errorf("找不到模型：%s", model)
	}
	start := time.Now()
	ort.SetSharedLibraryPath(lib)
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("onnxruntime 初始化失败: %w", err)
	}
	infos, outs, err := ort.GetInputOutputInfo(model)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, 3)
	hasTypeIDs := false
	for _, in := range infos {
		names = append(names, in.Name)
		if in.Name == "token_type_ids" {
			hasTypeIDs = true
		}
	}
	if len(outs) == 0 {
		return nil, fmt.Errorf("模型没有输出")
	}
	outNames := make([]string, len(outs))
	for i, o := range outs {
		outNames[i] = o.Name
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	s, err := ort.NewDynamicAdvancedSession(model, names, outNames, opts)
	if err != nil {
		return nil, err
	}
	return &Engine{session: s, hasTypeIDs: hasTypeIDs, dim: 512, LoadSeconds: time.Since(start).Seconds()}, nil
}

// Close 释放会话。
func (e *Engine) Close() { _ = e.session.Destroy() }

// Encode 把一段文本变成 512 维单位向量。
func (e *Engine) Encode(v *Vocab, text, pool string) ([]float32, error) {
	ids, mask := v.Encode(text, 512)
	seq := int64(len(ids))
	shape := ort.NewShape(1, seq)
	idT, err := ort.NewTensor(shape, ids)
	if err != nil {
		return nil, err
	}
	defer idT.Destroy()
	maskT, err := ort.NewTensor(shape, mask)
	if err != nil {
		return nil, err
	}
	defer maskT.Destroy()

	inputs := []ort.Value{idT, maskT}
	if e.hasTypeIDs {
		types := make([]int64, len(ids))
		ttT, err := ort.NewTensor(shape, types)
		if err != nil {
			return nil, err
		}
		defer ttT.Destroy()
		inputs = append(inputs, ttT)
	}

	outs := []ort.Value{nil}
	if err := e.session.Run(inputs, outs); err != nil {
		return nil, err
	}
	out, ok := outs[0].(*ort.Tensor[float32])
	if !ok || out == nil {
		return nil, fmt.Errorf("输出类型异常")
	}
	defer out.Destroy()
	sh := []int64(out.GetShape())
	if len(sh) != 3 {
		return nil, fmt.Errorf("输出维度异常: %v", sh)
	}
	hidden := int(sh[2])
	data := out.GetData()

	vec := make([]float32, hidden)
	if pool == "mean" {
		var cnt float32
		for t := 0; t < len(ids); t++ {
			if mask[t] == 0 {
				continue
			}
			cnt++
			row := data[t*hidden : (t+1)*hidden]
			for i := 0; i < hidden; i++ {
				vec[i] += row[i]
			}
		}
		if cnt > 0 {
			for i := range vec {
				vec[i] /= cnt
			}
		}
	} else {
		copy(vec, data[:hidden]) // CLS
	}
	normalize(vec)
	return vec, nil
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum <= 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// ---- 真实语料 ------------------------------------------------------------

type entry struct {
	ID      string
	Content string
	Ocr     string
	Kind    string
	Source  string
	Tags    string
	Created string
}

func (e entry) title() string {
	if e.Created == "" {
		return e.ID[:8]
	}
	return e.Created[:19] + "  " + e.Kind
}

// loadCorpus 读真实库（**先复制到临时目录再读**：原库是 WAL，直接只读打开会漏
// 掉还没 checkpoint 的内容，而复制 db+wal+shm 三件套能拿到最新状态）。
func loadCorpus(dbDir string) []entry {
	if dbDir == "" {
		dbDir = filepath.Join(os.Getenv("APPDATA"), "ClipNest")
	}
	tmp, err := os.MkdirTemp("", "clipnest-corpus-*")
	check(err, "建临时目录")
	for _, suf := range []string{"", "-wal", "-shm"} {
		src := filepath.Join(dbDir, "clipnest.db"+suf)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		raw, err := os.ReadFile(src)
		check(err, "复制库")
		check(os.WriteFile(filepath.Join(tmp, "clipnest.db"+suf), raw, 0o644), "写临时库")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(tmp, "clipnest.db"))+"?mode=ro")
	check(err, "打开临时库")
	defer db.Close()
	rows, err := db.Query(`SELECT id, content, COALESCE(ocr_text,''), kind, source, tags, created_at
		FROM entries WHERE archived = 0 ORDER BY created_at DESC`)
	check(err, "查条目")
	defer rows.Close()
	out := make([]entry, 0, 512)
	for rows.Next() {
		var e entry
		if rows.Scan(&e.ID, &e.Content, &e.Ocr, &e.Kind, &e.Source, &e.Tags, &e.Created) == nil {
			out = append(out, e)
		}
	}
	return out
}

// searchable 是被编码的正文：图片条目带上 OCR 文字，标签也拼进去。
func (e entry) searchable() string {
	parts := make([]string, 0, 3)
	if e.Content != "" {
		parts = append(parts, e.Content)
	}
	if e.Ocr != "" {
		parts = append(parts, e.Ocr)
	}
	if e.Tags != "" {
		parts = append(parts, strings.ReplaceAll(e.Tags, ",", " "))
	}
	return strings.Join(parts, "\n")
}

// ---- 基准 ---------------------------------------------------------------

func runBench(eng *Engine, v *Vocab, pool, dbDir string) {
	corpus := loadCorpus(dbDir)
	fmt.Printf("真实语料：%d 条\n\n", len(corpus))

	samples := []string{
		"https://dav.jianguoyun.com/dav/",
		"记一笔草稿没有保存就关窗了",
		"docker run -d --name pg -e POSTGRES_PASSWORD=secret -p 5432:5432 postgres:16",
		"明天下午三点开会，讨论同步包的增量方案",
		"坚果云 WebDAV 同步失败，检查服务器地址是否一字不差",
	}
	fmt.Println("== 1. 单条编码延迟（含分词）==")
	for _, s := range samples {
		t0 := time.Now()
		vec, err := eng.Encode(v, s, pool)
		el := time.Since(t0)
		check(err, "编码")
		ids, _ := v.Encode(s, 512)
		fmt.Printf("  %5.1f ms  %3d tokens  dim=%d  %s\n", float64(el.Microseconds())/1000, len(ids), len(vec), oneLine(s, 46))
	}

	// 稳态：预热后跑 30 次取中位数
	for i := 0; i < 5; i++ {
		_, _ = eng.Encode(v, samples[i%len(samples)], pool)
	}
	durs := make([]time.Duration, 0, 30)
	for i := 0; i < 30; i++ {
		t0 := time.Now()
		_, err := eng.Encode(v, samples[i%len(samples)], pool)
		check(err, "编码")
		durs = append(durs, time.Since(t0))
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	fmt.Printf("  稳态中位数 %.1f ms（p90 %.1f ms）\n\n",
		float64(durs[len(durs)/2].Microseconds())/1000,
		float64(durs[len(durs)*9/10].Microseconds())/1000)

	// 分词成本单独量（和推理分开，才知道慢在哪）
	t0 := time.Now()
	for i := 0; i < 1000; i++ {
		v.Encode(samples[i%len(samples)], 512)
	}
	fmt.Printf("  分词：1000 次 %.1f ms（每千字约 %.2f ms）\n\n",
		float64(time.Since(t0).Microseconds())/1000, float64(time.Since(t0).Microseconds())/1000/1000)

	fmt.Println("== 2. 建索引：真实语料全量编码 ==")
	if len(corpus) > 0 {
		limit := len(corpus)
		if limit > 400 {
			limit = 400 // 采样，够估总时间
		}
		t0 := time.Now()
		for i := 0; i < limit; i++ {
			_, err := eng.Encode(v, corpus[i].searchable(), pool)
			check(err, "编码")
		}
		el := time.Since(t0)
		per := el / time.Duration(limit)
		fmt.Printf("  %d 条耗时 %v（每条 %v）→ 1 万条约 %v\n\n", limit, el.Round(time.Millisecond),
			per.Round(time.Millisecond), (per * 10000).Round(time.Second))
	}

	fmt.Println("== 3. 暴力扫描（决定要不要引向量库）==")
	for _, count := range []int{1000, 10000, 50000} {
		// int8 量化
		mat := make([][]int8, count)
		for i := range mat {
			row := make([]int8, 512)
			for j := range row {
				row[j] = int8((i*31 + j*17) % 127)
			}
			mat[i] = row
		}
		q := make([]int8, 512)
		for j := range q {
			q[j] = int8(j % 101)
		}
		t0 := time.Now()
		best := int32(math.MinInt32)
		for _, row := range mat {
			var dot int32
			for j, x := range row {
				dot += int32(x) * int32(q[j])
			}
			if dot > best {
				best = dot
			}
		}
		int8ms := float64(time.Since(t0).Microseconds()) / 1000
		sinkInt32 = best // 别让编译器把整个循环当死代码删掉（否则量到 0.00ms）
		// fp32
		fmat := make([][]float32, count)
		for i := range fmat {
			row := make([]float32, 512)
			for j := range row {
				row[j] = float32(i%97) * 0.01
			}
			fmat[i] = row
		}
		fq := make([]float32, 512)
		for j := range fq {
			fq[j] = 0.01
		}
		t0 = time.Now()
		var fbest float32
		for _, row := range fmat {
			var dot float32
			for j, x := range row {
				dot += x * fq[j]
			}
			if dot > fbest {
				fbest = dot
			}
		}
		f32ms := float64(time.Since(t0).Microseconds()) / 1000
		sinkFloat32 = fbest
		fmt.Printf("  %6d 条 × 512 维：int8 %6.2f ms / fp32 %6.2f ms   （%d 字节/条 vs %d 字节/条）\n",
			count, int8ms, f32ms, 512, 512*4)
	}
	fmt.Printf("  （sink 防止优化：%d / %v）\n", sinkInt32, sinkFloat32)
	fmt.Println()
}

// ---- 检索对比 -----------------------------------------------------------

type scored struct {
	e     entry
	score float32
}

func runSearch(eng *Engine, v *Vocab, pool, dbDir, query string, topN int) {
	corpus := loadCorpus(dbDir)
	fmt.Printf("语料 %d 条；查询：%s\n\n", len(corpus), query)

	qv, err := eng.Encode(v, query, pool)
	check(err, "编码查询")

	vecs := make([][]float32, len(corpus))
	for i := range corpus {
		vecs[i], err = eng.Encode(v, corpus[i].searchable(), pool)
		check(err, "编码语料")
	}
	ranked := make([]scored, len(corpus))
	for i := range corpus {
		ranked[i] = scored{e: corpus[i], score: dot(qv, vecs[i])}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

	fmt.Printf("-- 向量 top-%d --\n", topN)
	for i := 0; i < topN && i < len(ranked); i++ {
		fmt.Printf("  %2d. %.4f  %s\n", i+1, ranked[i].score, oneLine(ranked[i].e.searchable(), 96))
	}

	fmt.Printf("\n-- FTS(trigram) 字面命中 --\n")
	fts := ftsSearch(dbDir, query, topN)
	if len(fts) == 0 {
		fmt.Println("  （一条都没有）")
	}
	for i, line := range fts {
		fmt.Printf("  %2d. %s\n", i+1, oneLine(line, 96))
	}

	// 交集/差集：这正是"语义搜索值不值得做"的核心指标
	vecTop := map[string]bool{}
	for i := 0; i < topN && i < len(ranked); i++ {
		vecTop[ranked[i].e.ID] = true
	}
	overlap := 0
	for _, id := range ftsIDs(dbDir, query, topN) {
		if vecTop[id] {
			overlap++
		}
	}
	fmt.Printf("\n向量 top-%d 与 FTS top-%d 的重合：%d 条（重合越少，说明两套各命中各的）\n",
		topN, topN, overlap)
}

func ftsSearch(dbDir, query string, n int) []string {
	tmp, err := os.MkdirTemp("", "clipnest-fts-*")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(dbDir, "clipnest.db")
	if dbDir == "" {
		src = filepath.Join(os.Getenv("APPDATA"), "ClipNest", "clipnest.db")
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		raw, err := os.ReadFile(src + suf)
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(tmp, "clipnest.db"+suf), raw, 0o644)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(tmp, "clipnest.db"))+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT content FROM entries_fts WHERE entries_fts MATCH ? LIMIT ?`,
		`"`+strings.ReplaceAll(query, `"`, `""`)+`"`, n)
	if err != nil {
		return []string{"（FTS 报错：" + err.Error() + "）"}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			out = append(out, c)
		}
	}
	return out
}

func ftsIDs(dbDir, query string, n int) []string {
	tmp, err := os.MkdirTemp("", "clipnest-ftsi-*")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(dbDir, "clipnest.db")
	if dbDir == "" {
		src = filepath.Join(os.Getenv("APPDATA"), "ClipNest", "clipnest.db")
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		raw, err := os.ReadFile(src + suf)
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(tmp, "clipnest.db"+suf), raw, 0o644)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(tmp, "clipnest.db"))+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id FROM entries_fts WHERE entries_fts MATCH ? LIMIT ?`,
		`"`+strings.ReplaceAll(query, `"`, `""`)+`"`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// ---- 检索评测 -----------------------------------------------------------

// queryInstruction 是 bge 中文版官方推荐的检索指令：**只加在查询侧**，
// 文档那侧不加。它把"短查询 ↔ 长文档"的不对称显式告诉模型。
// BAAI/bge-small-zh-v1.5 的模型卡写的就是这一句。
const queryInstruction = "为这个句子生成表示以用于检索相关文章："

// runEval 是"语义搜索到底补了什么"的回答。
//
// 关键对照：同一句查询
//   · 向量：能不能把目标条目排到前面；
//   · FTS(trigram)：能不能**找到**目标条目（trigram 的短语匹配 ≈ 子串匹配，
//     所以这里用 strings.Contains 近似它，等价且不用起 sqlite）。
//
// 如果 FTS 已经能命中，那这条查询不说明问题；真正有价值的是
// "FTS 命中 0 条，向量把目标排进前 5" 那一格。
func runEval(eng *Engine, v *Vocab, pool string, withInstr, useInt8 bool) {
	vecs := make([][]float32, len(evalSet))
	for i := range evalSet {
		var err error
		vecs[i], err = eng.Encode(v, evalSet[i].Text, pool)
		check(err, "编码语料")
	}
	// int8 版本：每条向量按自己的最大绝对值定标（512 字节/条，比 fp32 省 4 倍）
	var c8 [][]int8
	var c8s []float32
	if useInt8 {
		c8 = make([][]int8, len(vecs))
		c8s = make([]float32, len(vecs))
		for i := range vecs {
			c8[i], c8s[i] = quantize(vecs[i])
		}
	}

	fmt.Printf("评测集：%d 条（池化=%s，%d 维，查询指令=%v，int8=%v）\n",
		len(evalSet), pool, len(vecs[0]), withInstr, useInt8)
	fmt.Printf("%-6s %-28s %5s %6s %5s %-4s\n", "领域", "查询", "目标名次", "目标分", "最高分", "字面")
	fmt.Println(strings.Repeat("-", 74))

	var r1, r5, r10, ftsHit, literal int
	var sumTop float32
	for qi := range evalSet {
		c := evalSet[qi]
		qv, err := eng.Encode(v, queryText(c.Query, withInstr), pool)
		check(err, "编码查询")
		ranked := make([]scored, len(vecs))
		if useInt8 {
			q8, qs := quantize(qv)
			for i := range vecs {
				ranked[i] = scored{e: entry{ID: fmt.Sprint(i)}, score: dotInt8(q8, c8[i]) * qs * c8s[i]}
			}
		} else {
			for i := range vecs {
				ranked[i] = scored{e: entry{ID: fmt.Sprint(i)}, score: dot(qv, vecs[i])}
			}
		}
		sort.Slice(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
		rank, targetScore := -1, float32(0)
		for i := range ranked {
			if ranked[i].e.ID == fmt.Sprint(qi) {
				rank, targetScore = i+1, ranked[i].score
				break
			}
		}
		// FTS 近似：查询整体是否作为子串出现在目标里（trigram 短语匹配的行为）
		hit := strings.Contains(strings.ToLower(c.Text), strings.ToLower(c.Query))
		lcs := longestCommonSubstring(c.Query, c.Text)
		if hit || lcs >= 3 {
			literal++
		}
		switch {
		case rank == 1:
			r1++
		}
		if rank >= 1 && rank <= 5 {
			r5++
		}
		if rank >= 1 && rank <= 10 {
			r10++
		}
		if hit {
			ftsHit++
		}
		sumTop += ranked[0].score
		mark := ""
		if !hit && rank <= 5 {
			mark = "  ← 只有向量能找到"
		}
		fmt.Printf("%-6s %-28s %5d %6.3f %5.3f %-4s%s\n",
			c.Domain, oneLine(c.Query, 28), rank, targetScore, ranked[0].score,
			map[bool]string{true: "命中", false: "无"}[hit], mark)
	}
	n := len(evalSet)
	fmt.Println(strings.Repeat("-", 74))
	fmt.Printf("Recall@1 = %d/%d = %.0f%%   @5 = %d/%d = %.0f%%   @10 = %d/%d = %.0f%%\n",
		r1, n, 100*float64(r1)/float64(n), r5, n, 100*float64(r5)/float64(n), r10, n, 100*float64(r10)/float64(n))
	fmt.Printf("FTS 能字面命中的查询：%d/%d = %.0f%%（其余 %.0f%% 只能靠语义）\n",
		ftsHit, n, 100*float64(ftsHit)/float64(n), 100*float64(n-ftsHit)/float64(n))
	fmt.Printf("查询与目标共享 ≥3 字子串的：%d/%d = %.0f%%；平均 top1 相似度 %.3f\n\n",
		literal, n, 100*float64(literal)/float64(n), sumTop/float32(n))

	fmt.Println("-- 语料里没有答案的查询（看阈值下限）--")
	for _, q := range negCases {
		qv, err := eng.Encode(v, queryText(q, withInstr), pool)
		check(err, "编码查询")
		best, bestIdx := float32(-1), 0
		for i := range vecs {
			if s := dot(qv, vecs[i]); s > best {
				best, bestIdx = s, i
			}
		}
		fmt.Printf("  %.3f  %-22s → %s\n", best, q, oneLine(evalSet[bestIdx].Text, 40))
	}
	fmt.Println()
}

// queryText 按需给查询加上检索指令（文档侧永远不加）。
func queryText(q string, withInstr bool) string {
	if withInstr {
		return queryInstruction + q
	}
	return q
}

// longestCommonSubstring 返回两个字符串最长的公共子串长度（按 rune 算）。
// 用它判断"这条查询到底有多字面"，比数共享词更能说明问题。
func longestCommonSubstring(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	best := 0
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			if ra[i-1] == rb[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
	}
	return best
}

// ---- 小工具 -------------------------------------------------------------

// sinkInt32 / sinkFloat32 接住基准里算出来的极值：Go 编译器会删掉"结果没人用"的
// 纯计算循环，第一版量到的 `int8 0.00 ms` 就是这么来的（假的）。
var (
	sinkInt32   int32
	sinkFloat32 float32
)

// quantize 把单位向量压成 int8 + 一个标量。因为向量已经是单位长度，
// 反量化后的点积乘两次 scale 就等于近似余弦，排序用它足够。
func quantize(v []float32) ([]int8, float32) {
	var maxAbs float32
	for _, x := range v {
		if x < 0 {
			x = -x
		}
		if x > maxAbs {
			maxAbs = x
		}
	}
	if maxAbs == 0 {
		return make([]int8, len(v)), 1
	}
	scale := maxAbs / 127
	q := make([]int8, len(v))
	for i, x := range v {
		q[i] = int8(math.Round(float64(x / scale)))
	}
	return q, scale
}

// dotInt8 是量化点积（int32 累加，不溢出：512×127×127 ≈ 8.3e6）。
func dotInt8(a, b []int8) float32 {
	var s int32
	for i := range a {
		s += int32(a[i]) * int32(b[i])
	}
	return float32(s)
}

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func check(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}

var _ = gob.NewEncoder // 预留：下一步把向量缓存进文件，省掉每次重算


