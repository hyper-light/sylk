package ivf

import (
	"math"
	"math/bits"
	"unsafe"

	"github.com/viterin/vek/vek32"
)

// BBQ: Better Binary Quantization with lookup-table acceleration.
// DB: 1-bit/dim (96 bytes for 768-dim), Query: int8 + 48KB lookup table (fits L2).
// Distance: O(codeLen) lookups vs O(dim) multiply-accumulates. Zero allocations in hot path.
type BBQ struct {
	dim     int
	codeLen int
	means   []float32
}

func NewBBQ(dim int) *BBQ {
	return &BBQ{
		dim:     dim,
		codeLen: (dim + 7) / 8,
		means:   make([]float32, dim),
	}
}

func (b *BBQ) Train(vectors [][]float32) {
	n := len(vectors)
	if n == 0 {
		return
	}

	sums := make([]float32, b.dim)
	for _, vec := range vectors {
		vek32.Add_Inplace(sums, vec)
	}

	invN := 1.0 / float32(n)
	vek32.MulNumber_Inplace(sums, invN)
	copy(b.means, sums)
}

func (b *BBQ) EncodeDB(vec []float32, code []byte) {
	for i := range code {
		code[i] = 0
	}

	for i := 0; i < b.dim; i++ {
		if vec[i] > b.means[i] {
			code[i>>3] |= 1 << (i & 7)
		}
	}
}

func (b *BBQ) EncodeDBBatch(vectors [][]float32, codes [][]byte) {
	for i, vec := range vectors {
		b.EncodeDB(vec, codes[i])
	}
}

type BBQQuery struct {
	// lookup[bytePos][byteValue] = sum of quantized query values for set bits in that byte
	lookup   [][]int16
	querySum int32
	codeLen  int
}

func (b *BBQ) EncodeQuery(vec []float32) *BBQQuery {
	quantized := make([]int8, b.dim)
	var querySum int32

	var maxAbs float32
	for i, v := range vec {
		centered := v - b.means[i]
		if centered < 0 {
			centered = -centered
		}
		if centered > maxAbs {
			maxAbs = centered
		}
	}

	maxInt8Val := float32(math.MaxInt8)
	var scale float32 = maxInt8Val
	if maxAbs > math.SmallestNonzeroFloat32 {
		scale = maxInt8Val / maxAbs
	}

	for i, v := range vec {
		centered := v - b.means[i]
		q := int8(centered * scale)
		quantized[i] = q
		querySum += int32(q)
	}

	q := &BBQQuery{
		lookup:   make([][]int16, b.codeLen),
		querySum: querySum,
		codeLen:  b.codeLen,
	}

	for bytePos := range b.codeLen {
		q.lookup[bytePos] = make([]int16, 256)
		baseIdx := bytePos * 8

		for byteVal := 0; byteVal < 256; byteVal++ {
			var sum int16
			for bit := 0; bit < 8; bit++ {
				idx := baseIdx + bit
				if idx >= b.dim {
					break
				}
				if byteVal&(1<<bit) != 0 {
					sum += int16(quantized[idx])
				}
			}
			q.lookup[bytePos][byteVal] = sum
		}
	}

	return q
}

// Distance: asymmetric distance = 2 * Σ_positive(query) - Σ_all(query). Higher = more similar.
func (q *BBQQuery) Distance(code []byte) int32 {
	var sumPositive int32
	for i, b := range code {
		sumPositive += int32(q.lookup[i][b])
	}
	return 2*sumPositive - q.querySum
}

func (q *BBQQuery) DistanceBatch(codes [][]byte, dists []int32) {
	for i, code := range codes {
		dists[i] = q.Distance(code)
	}
}

func (q *BBQQuery) DistanceFlat(codes []byte, n int, dists []int32) {
	codeLen := q.codeLen
	for i := 0; i < n; i++ {
		offset := i * codeLen
		code := codes[offset : offset+codeLen]
		var sumPositive int32
		for j, b := range code {
			sumPositive += int32(q.lookup[j][b])
		}
		dists[i] = 2*sumPositive - q.querySum
	}
}

const bytesPerUint64 = 8

// HammingDistance computes popcount(a XOR b) using hardware POPCNT.
func HammingDistance(a, b []byte) int {
	dist := 0
	n := len(a)
	nWords := n / bytesPerUint64

	if nWords > 0 {
		a64 := unsafe.Slice((*uint64)(unsafe.Pointer(&a[0])), nWords)
		b64 := unsafe.Slice((*uint64)(unsafe.Pointer(&b[0])), nWords)
		for i := 0; i < nWords; i++ {
			dist += bits.OnesCount64(a64[i] ^ b64[i])
		}
	}

	for i := nWords * bytesPerUint64; i < n; i++ {
		dist += bits.OnesCount8(a[i] ^ b[i])
	}
	return dist
}

func (b *BBQ) CodeLen() int         { return b.codeLen }
func (b *BBQ) Dim() int             { return b.dim }
func (b *BBQ) Means() []float32     { return b.means }
func (b *BBQ) SetMeans(m []float32) { copy(b.means, m) }

func NewBBQFromParams(dim, codeLen int, means []float32) *BBQ {
	bbq := &BBQ{
		dim:     dim,
		codeLen: codeLen,
		means:   make([]float32, dim),
	}
	if len(means) == dim {
		copy(bbq.means, means)
	}
	return bbq
}
