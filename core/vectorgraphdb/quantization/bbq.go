package quantization

import (
	"math"
	"sort"

	"gonum.org/v1/gonum/stat"
)

type BBQEncoder struct {
	means     []float64
	scales    []float64
	dim       int
	queryBits int
	maxVal    float64
}

func NewBBQEncoder(dim int, queryBits int) *BBQEncoder {
	maxVal := float64(int(1)<<(queryBits-1) - 1)
	return &BBQEncoder{
		dim:       dim,
		queryBits: queryBits,
		maxVal:    maxVal,
		means:     make([]float64, dim),
		scales:    make([]float64, dim),
	}
}

func (b *BBQEncoder) Train(vectors [][]float32) {
	if len(vectors) == 0 || b.dim == 0 {
		return
	}
	b.deriveMeans(vectors)
	b.deriveScales(vectors)
}

func (b *BBQEncoder) deriveMeans(vectors [][]float32) {
	data := make([]float64, len(vectors))
	for d := 0; d < b.dim; d++ {
		for i, v := range vectors {
			data[i] = float64(v[d])
		}
		b.means[d] = stat.Mean(data, nil)
	}
}

func (b *BBQEncoder) deriveScales(vectors [][]float32) {
	data := make([]float64, len(vectors))
	for d := 0; d < b.dim; d++ {
		for i, v := range vectors {
			data[i] = float64(v[d])
		}
		std := stat.StdDev(data, nil)
		if std > 1e-10 {
			b.scales[d] = b.computeOptimalScale(data, std)
		} else {
			b.scales[d] = 1.0
		}
	}
}

func (b *BBQEncoder) computeOptimalScale(data []float64, std float64) float64 {
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)
	iqr := stat.Quantile(0.75, stat.Empirical, sorted, nil) - stat.Quantile(0.25, stat.Empirical, sorted, nil)
	if iqr < 1e-10 {
		iqr = std
	}
	return b.maxVal / iqr
}

func (b *BBQEncoder) EncodeDatabase(vector []float32) []byte {
	code := make([]byte, (b.dim+7)/8)
	for i, v := range vector {
		if float64(v) > b.means[i] {
			code[i/8] |= 1 << (i % 8)
		}
	}
	return code
}

func (b *BBQEncoder) EncodeQuery(vector []float32) []int8 {
	code := make([]int8, len(vector))
	minVal := -(b.maxVal + 1)
	for i, v := range vector {
		normalized := (float64(v) - b.means[i]) * b.scales[i]
		code[i] = int8(math.Max(minVal, math.Min(b.maxVal, math.Round(normalized))))
	}
	return code
}

func (b *BBQEncoder) AsymmetricDistance(queryCode []int8, dbCode []byte) int {
	var dist int
	for i := 0; i < len(queryCode); i++ {
		bit := (dbCode[i/8] >> (i % 8)) & 1
		if bit == 0 {
			dist -= int(queryCode[i])
		} else {
			dist += int(queryCode[i])
		}
	}
	return dist
}

func (b *BBQEncoder) BatchDistance(queryCode []int8, dbCodes [][]byte) []int {
	dists := make([]int, len(dbCodes))
	for i, db := range dbCodes {
		dists[i] = b.AsymmetricDistance(queryCode, db)
	}
	return dists
}
