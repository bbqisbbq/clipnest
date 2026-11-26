package embed

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// realVocabPath 找真实词表（gitignore 了，本机下载过才有）。找不到就跳过那些
// "必须和真 tokenizer 对得上"的用例 —— 它们重要，但不该让没下模型的机器红。
func realVocabPath(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		os.Getenv("CLIPNEST_EMBED_VOCAB"),
		filepath.Join("..", "..", "scratch", "embed-model", "vocab.txt"),
	} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			return p
		}
	}
	t.Skip("没有真实词表（scratch/embed-model/vocab.txt），跳过与真 tokenizer 对齐的用例")
	return ""
}

// 中文逐字加空格：这是 BERT 中文分词的前提，错了后面全错。
func TestNormalizeChineseSpacing(t *testing.T) {
	got := Normalize("部署nginx")
	want := " 部  署 nginx"
	if got != want {
		t.Errorf("Normalize(%q) = %q，期望 %q", "部署nginx", got, want)
	}
	// 控制符要清掉，制表/换行归一成空格
	if got := Normalize("a\x00b\tc\nd"); got != "ab c d" {
		t.Errorf("控制符/空白处理不对：%q", got)
	}
	// 全角标点不是 CJK 表意字，不该被拆开（它后面会在预分词里单独切出来）
	if got := Normalize("好，的"); got != " 好 ， 的 " {
		t.Errorf("全角逗号不该被当 CJK 拆开：%q", got)
	}
}

// 标点单独切出来，而且 ASCII 那几段（$ + < = > ^ ` | ~）也要切 ——
// 它们不属于 Unicode 的 P* 类，只用 unicode.IsPunct 会漏。
func TestPreTokenizePunctuation(t *testing.T) {
	cases := map[string][]string{
		"a=b":            {"a", "=", "b"},
		"proxy_pass":     {"proxy", "_", "pass"},
		"a|b":            {"a", "|", "b"},
		"你好，世界":          {"你", "好", "，", "世", "界"},
		"https://x.com/": {"https", ":", "/", "/", "x", ".", "com", "/"},
	}
	for in, want := range cases {
		got := PreTokenize(Normalize(in))
		if len(got) != len(want) {
			t.Errorf("PreTokenize(%q) = %v，期望 %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("PreTokenize(%q)[%d] = %q，期望 %q", in, i, got[i], want[i])
			}
		}
	}
}

// WordPiece 的贪心最长匹配 + "##" 续接 + 整词无法切分时给 [UNK]。
func TestWordPieceMechanics(t *testing.T) {
	v := &Vocab{
		ID: map[string]int64{
			"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102,
			"hello": 10, "wor": 11, "##ld": 12, "a": 13,
		},
		Token: []string{"[PAD]", "", "", "", "", "", "", "", "", "", "hello"},
	}
	if got := v.WordPiece("hello"); len(got) != 1 || got[0] != 10 {
		t.Errorf("整词命中失败：%v", got)
	}
	if got := v.WordPiece("world"); len(got) != 2 || got[0] != 11 || got[1] != 12 {
		t.Errorf("## 续接失败：%v", got)
	}
	if got := v.WordPiece("zzz"); len(got) != 1 || got[0] != idUnk {
		t.Errorf("无法切分应给 [UNK]：%v", got)
	}
	// 超长单词直接 [UNK]（HF 的上限是 100 个字符）
	long := ""
	for i := 0; i < 101; i++ {
		long += "a"
	}
	if got := v.WordPiece(long); len(got) != 1 || got[0] != idUnk {
		t.Errorf("超长单词应给 [UNK]：%v", got)
	}
}

// 期望 id 是从真 tokenizer（HF BertTokenizer，与 tokenizer.json 同一套规则）抓下来的。
// 这条用例的价值：自己实现的 WordPiece 一旦和 HF 有偏差，向量就会整体漂移，
// 而这种错误不会报错、只是"搜得没那么准"，没有基线根本发现不了。
func TestEncodeMatchesReference(t *testing.T) {
	v, err := LoadVocab(realVocabPath(t))
	if err != nil {
		t.Fatalf("读词表: %v", err)
	}
	cases := []struct {
		text string
		want []int64
	}{
		{"记一笔草稿没有保存就关窗了", []int64{101, 6381, 671, 5011, 5770, 4943, 3766, 3300, 924, 2100, 2218, 1068, 4970, 749, 102}},
		{"docker run -d --name pg", []int64{101, 10290, 9909, 118, 146, 118, 118, 8617, 12898, 102}},
		{"部署 nginx 反向代理，proxy_pass 到 8080", []int64{101, 6956, 5392, 12576, 1353, 1403, 807, 4415, 8024, 8376, 11038, 142, 9703, 1168, 8188, 8538, 102}},
	}
	for _, c := range cases {
		got, mask := v.Encode(c.text, MaxLen)
		if len(got) != len(c.want) {
			t.Errorf("%q 长度 %d，期望 %d\n got=%v\nwant=%v", c.text, len(got), len(c.want), got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%q 第 %d 个 id = %d，期望 %d\n got=%v\nwant=%v", c.text, i, got[i], c.want[i], got, c.want)
				break
			}
		}
		if len(mask) != len(got) {
			t.Errorf("%q mask 长度不一致", c.text)
		}
	}
}

// 截断：超长文本要按 MaxLen 截，并且最后一个必须是 [SEP]（不是被硬砍掉）。
func TestEncodeTruncates(t *testing.T) {
	v := &Vocab{ID: map[string]int64{"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102, "a": 13}}
	long := ""
	for i := 0; i < 50; i++ {
		long += "a "
	}
	ids, _ := v.Encode(long, 10)
	if len(ids) != 10 {
		t.Fatalf("截断后长度 = %d，期望 10", len(ids))
	}
	if ids[0] != idCls || ids[len(ids)-1] != idSep {
		t.Errorf("截断后首尾不是 [CLS]/[SEP]：%v", ids)
	}
}

// 空文本也要能编（给 [CLS][SEP]，不是报错）：条目可能只有图片/只有标签。
func TestEncodeEmpty(t *testing.T) {
	v := &Vocab{ID: map[string]int64{"[PAD]": 0, "[UNK]": 100, "[CLS]": 101, "[SEP]": 102}}
	ids, mask := v.Encode("   \n\t ", MaxLen)
	if len(ids) != 2 || ids[0] != idCls || ids[1] != idSep {
		t.Errorf("空文本应给 [CLS][SEP]，得到 %v", ids)
	}
	if len(mask) != 2 || mask[0] != 1 || mask[1] != 1 {
		t.Errorf("mask 不对：%v", mask)
	}
}

// 量化 + 序列化的往返：存进库里的就是这几个字节，长度对不上就整条当没有。
func TestVectorBlobRoundTrip(t *testing.T) {
	src := make([]float32, Dim)
	for i := range src {
		src[i] = float32(math.Sin(float64(i) * 0.1))
	}
	v := Quantize(normalize(append([]float32(nil), src...)))
	blob := v.Blob()
	if len(blob) != BlobLen {
		t.Fatalf("Blob 长度 = %d，期望 %d", len(blob), BlobLen)
	}
	back, ok := VectorFromBlob(blob)
	if !ok {
		t.Fatal("反序列化失败")
	}
	if d := Dot(v, back); d < 0.999 {
		t.Errorf("量化往返后自相似度 = %.4f，期望 ≈1", d)
	}
	// 坏数据一律当没有，绝不让它把整条检索搞崩
	if _, ok := VectorFromBlob(blob[:len(blob)-1]); ok {
		t.Error("长度不对应该失败")
	}
	bad := append([]byte(nil), blob...)
	bad[0], bad[1], bad[2], bad[3] = 0, 0, 0, 0 // scale = 0
	if _, ok := VectorFromBlob(bad); ok {
		t.Error("scale=0 应该失败")
	}
}

// 量化后点积要能近似原来 fp32 的余弦（这是"存 int8 不掉质量"的前提）。
func TestQuantizedDotCloseToFloat(t *testing.T) {
	a := make([]float32, Dim)
	b := make([]float32, Dim)
	for i := range a {
		a[i] = float32(math.Cos(float64(i) * 0.05))
		b[i] = float32(math.Sin(float64(i) * 0.05))
	}
	na, nb := normalize(a), normalize(b)
	var want float32
	for i := range na {
		want += na[i] * nb[i]
	}
	got := Dot(Quantize(na), Quantize(nb))
	if diff := float32(math.Abs(float64(got - want))); diff > 0.01 {
		t.Errorf("量化点积 %.4f 与 fp32 %.4f 差 %.4f，超过 0.01", got, want, diff)
	}
}

// 资源不全时要给出"缺什么"，界面上那句话就是从这来的。
func TestReadyReportsMissing(t *testing.T) {
	dir := t.TempDir()
	err := Ready(dir)
	if err == nil {
		t.Fatal("空目录不该 Ready")
	}
	if CompiledIn() {
		missing, ok := err.(*ErrAssetsMissing)
		if !ok {
			t.Fatalf("期望 ErrAssetsMissing，得到 %T", err)
		}
		if len(missing.Missing) == 0 {
			t.Error("应该报出缺哪些文件")
		}
	}
}
