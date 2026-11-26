package embed

// 向量的量化表示与点积。
//
// 为什么存 int8 而不是 fp32：512 字节/条 vs 2048 字节/条，1 万条差 15MB。
// 实测（39 条评测集）量化重排序 R@5 64% vs 67%，在样本量内是噪声 —— 见
// docs/design/SEMANTIC-SEARCH.md。
//
// **但别指望它提速**：Go 里朴素 int8 点积每元素都要转 int32，实测 1000 条时
// int8 与 fp32 都是 0.54ms。量化只买空间。

import (
	"encoding/binary"
	"math"
)

// Vector 是一条 512 维单位向量的 int8 量化形式。
//
// 每条向量带自己的 scale（按最大绝对值定标），因为不同文本的向量绝对值分布差很多。
// 因为原向量已 L2 归一，反量化点积乘两次 scale 就是近似余弦。
type Vector struct {
	Q     []int8
	Scale float32
}

// BlobLen 是 Vector 序列化后的固定字节数：4 字节 scale + Dim 字节分量。
const BlobLen = 4 + Dim

// Quantize 把单位向量压成 int8。
func Quantize(v []float32) Vector {
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
		return Vector{Q: make([]int8, len(v)), Scale: 1}
	}
	scale := maxAbs / 127
	q := make([]int8, len(v))
	for i, x := range v {
		q[i] = int8(math.Round(float64(x / scale)))
	}
	return Vector{Q: q, Scale: scale}
}

// Dequantize 还原成 float32（测试与排障用，产品路径用不到）。
func (v Vector) Dequantize() []float32 {
	out := make([]float32, len(v.Q))
	for i, x := range v.Q {
		out[i] = float32(x) * v.Scale
	}
	return out
}

// Blob 序列化成存库用的字节（小端：scale 4 字节 + 分量）。
func (v Vector) Blob() []byte {
	out := make([]byte, BlobLen)
	binary.LittleEndian.PutUint32(out[0:4], math.Float32bits(v.Scale))
	for i := 0; i < Dim && i < len(v.Q); i++ {
		out[4+i] = byte(v.Q[i])
	}
	return out
}

// VectorFromBlob 反序列化；长度不对或维度不符时返回 false（老版本/坏数据一律当没有）。
func VectorFromBlob(b []byte) (Vector, bool) {
	if len(b) != BlobLen {
		return Vector{}, false
	}
	scale := math.Float32frombits(binary.LittleEndian.Uint32(b[0:4]))
	if scale <= 0 || math.IsNaN(float64(scale)) || math.IsInf(float64(scale), 0) {
		return Vector{}, false
	}
	q := make([]int8, Dim)
	for i := 0; i < Dim; i++ {
		q[i] = int8(b[4+i])
	}
	return Vector{Q: q, Scale: scale}, true
}

// Dot 是两个量化向量的近似余弦（都按 L2 归一过的向量算）。
func Dot(a, b Vector) float32 {
	n := len(a.Q)
	if len(b.Q) < n {
		n = len(b.Q)
	}
	var s int32
	for i := 0; i < n; i++ {
		s += int32(a.Q[i]) * int32(b.Q[i])
	}
	return float32(s) * a.Scale * b.Scale
}

// DotBlob 直接对**序列化**的向量做点积，不反序列化。
//
// 为什么非要绕这一下：全量扫描时每条都 Blob→[]int8 会分配一块 512 字节的切片，
// 1 万条就是每次搜索 5MB 垃圾（每秒搜几次就该听见 GC 了）。这里直接按字节读。
func DotBlob(blob []byte, q Vector) (float32, bool) {
	if len(blob) != BlobLen || len(q.Q) != Dim {
		return 0, false
	}
	scale := math.Float32frombits(binary.LittleEndian.Uint32(blob[0:4]))
	if scale <= 0 {
		return 0, false
	}
	var s int32
	for i := 0; i < Dim; i++ {
		s += int32(int8(blob[4+i])) * int32(q.Q[i])
	}
	return float32(s) * scale * q.Scale, true
}
