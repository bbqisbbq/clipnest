package main

// BERT 预分词 + WordPiece，逐条对齐 HuggingFace 的
// BertNormalizer → BertPreTokenizer → WordPiece（见 tokenizer.json 里的配置）。
//
// 为什么要自己实现：Go 里没有现成的 WordPiece，而这几步加起来不到 200 行；
// 引进一个 tokenizer 库反而要多背一个依赖（clipnest 的依赖表已经清到只剩 5 个）。
//
// 与 tokenizer.json 的对应关系：
//
//	normalizer    BertNormalizer{clean_text:true, handle_chinese_chars:true,
//	                             strip_accents:null, lowercase:false}
//	pre_tokenizer BertPreTokenizer
//	decoder       WordPiece{prefix:"##"}
//
// strip_accents 为 null 时 HF 会取 lowercase 的值（false），所以这里不做重音还原；
// lowercase 也是 false —— bge-small-zh 的词表里大小写是分开的，别自作主张转小写。

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

// SpecialIDs 是词表里的固定 id（见 tokenizer.json 的 added_tokens）。
const (
	idPad  = int64(0)
	idUnk  = int64(100)
	idCls  = int64(101)
	idSep  = int64(102)
	idMask = int64(103)
)

// maxCharsPerWord 是 WordPiece 的单词长度上限（HF 的 max_input_chars_per_word）。
const maxCharsPerWord = 100

// Vocab 是词表与反查表。
type Vocab struct {
	ID    map[string]int64
	Token []string
}

// LoadVocab 读 vocab.txt：一行一个 token，行号就是 id。
func LoadVocab(path string) (*Vocab, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	// 文件末尾的换行会产生一个空串，只丢"末尾那一个"（词表里没有空 token，但别顺手 Trim 掉真实行）
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 1000 {
		return nil, fmt.Errorf("词表太小（%d 行）：%s", len(lines), path)
	}
	v := &Vocab{ID: make(map[string]int64, len(lines)), Token: lines}
	for i, t := range lines {
		if _, dup := v.ID[t]; !dup {
			v.ID[t] = int64(i)
		}
	}
	if v.ID["[CLS]"] != idCls || v.ID["[UNK]"] != idUnk {
		return nil, fmt.Errorf("[CLS]/[UNK] 的 id 不是 101/100，词表可能不是 BERT 的（%s）", path)
	}
	return v, nil
}

// isWhitespace 对齐 HF 的 _is_whitespace：空格/制表/换行/回车，或 Unicode 空白。
func isWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return unicode.IsSpace(r)
}

// isControl 对齐 HF 的 _is_control：Cc/Cf 类，但 \t \n \r 不算（它们是排版不是控制）。
func isControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

// isChineseChar 对齐 HF 的 _is_chinese_char（CJK 统一表意文字及其扩展、兼容区）。
func isChineseChar(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x20000 && r <= 0x2A6DF,
		r >= 0x2A700 && r <= 0x2B73F,
		r >= 0x2B740 && r <= 0x2B81F,
		r >= 0x2B820 && r <= 0x2CEAF,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0x2F800 && r <= 0x2FA1F:
		return true
	}
	return false
}

// isPunctuation 对齐 HF 的 _is_punctuation：ASCII 里那些标点段 + Unicode 的 P* 类。
//
// 不能只用 unicode.IsPunct：ASCII 的 $ + < = > ^ ` | ~ 在 Unicode 里是 Sc/Sm/Sk，
// 不是 P*，而 BERT 把它们当标点切开。
func isPunctuation(r rune) bool {
	cp := int(r)
	if (cp >= 33 && cp <= 47) || (cp >= 58 && cp <= 64) || (cp >= 91 && cp <= 96) || (cp >= 123 && cp <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}

// Normalize 是 BertNormalizer：清控制符 → 空白归一 → 给 CJK 逐字加空格。
func Normalize(text string) string {
	var sb strings.Builder
	sb.Grow(len(text) + len(text)/4)
	for _, r := range text {
		if isControl(r) {
			continue
		}
		if isWhitespace(r) {
			sb.WriteRune(' ')
			continue
		}
		if isChineseChar(r) {
			sb.WriteRune(' ')
			sb.WriteRune(r)
			sb.WriteRune(' ')
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// PreTokenize 是 BertPreTokenizer：按空白切，再把标点各自切出来。
func PreTokenize(normalized string) []string {
	out := make([]string, 0, 32)
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range normalized {
		if isWhitespace(r) {
			flush()
			continue
		}
		if isPunctuation(r) {
			flush()
			out = append(out, string(r))
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// WordPiece 把一个预分词结果切成子词 id（贪心最长匹配）。
func (v *Vocab) WordPiece(word string) []int64 {
	runes := []rune(word)
	if len(runes) > maxCharsPerWord {
		return []int64{idUnk}
	}
	ids := make([]int64, 0, 4)
	start := 0
	for start < len(runes) {
		end := len(runes)
		found := int64(-1)
		for start < end {
			piece := string(runes[start:end])
			if start > 0 {
				piece = "##" + piece
			}
			if id, ok := v.ID[piece]; ok {
				found = id
				break
			}
			end--
		}
		if found < 0 {
			return []int64{idUnk}
		}
		ids = append(ids, found)
		start = end
	}
	return ids
}

// Encode 是完整流程：归一 → 预分词 → WordPiece → 包上 [CLS]/[SEP] 并按 maxLen 截断。
//
// 返回 ids 与 attention mask（全是 1，因为没有 padding —— 一次只喂一条）。
func (v *Vocab) Encode(text string, maxLen int) (ids []int64, mask []int64) {
	tokens := PreTokenize(Normalize(text))
	body := make([]int64, 0, len(tokens)+2)
	body = append(body, idCls)
	for _, tok := range tokens {
		body = append(body, v.WordPiece(tok)...)
		if len(body) >= maxLen-1 {
			break
		}
	}
	body = append(body, idSep)
	if len(body) > maxLen {
		body = body[:maxLen]
		body[len(body)-1] = idSep
	}
	mask = make([]int64, len(body))
	for i := range mask {
		mask[i] = 1
	}
	return body, mask
}
