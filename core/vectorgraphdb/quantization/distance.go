package quantization

import (
	"sort"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas32"
)

type DistanceIndex struct {
	flat  []float32
	norms []float32
	dim   int
	n     int
}

func NewDistanceIndex(vectors [][]float32) *DistanceIndex {
	n := len(vectors)
	if n == 0 {
		return &DistanceIndex{}
	}
	dim := len(vectors[0])
	flat := make([]float32, n*dim)
	norms := make([]float32, n)

	for i, v := range vectors {
		copy(flat[i*dim:], v)
		vec := blas32.Vector{N: dim, Inc: 1, Data: v}
		norms[i] = blas32.Dot(vec, vec)
	}
	return &DistanceIndex{flat: flat, norms: norms, dim: dim, n: n}
}

func (idx *DistanceIndex) SquaredL2(query []float32, out []float32) {
	if idx.n == 0 {
		return
	}
	queryVec := blas32.Vector{N: idx.dim, Inc: 1, Data: query}
	queryNorm := blas32.Dot(queryVec, queryVec)

	blas32.Gemv(
		blas.NoTrans, 1.0,
		blas32.General{Rows: idx.n, Cols: idx.dim, Stride: idx.dim, Data: idx.flat},
		queryVec, 0.0,
		blas32.Vector{N: idx.n, Inc: 1, Data: out},
	)

	for i := range idx.n {
		d := queryNorm + idx.norms[i] - 2*out[i]
		if d < 0 {
			d = 0
		}
		out[i] = d
	}
}

func (idx *DistanceIndex) KNN(query []float32, k int, distBuf []float32) []int {
	if idx.n == 0 {
		return nil
	}
	idx.SquaredL2(query, distBuf)

	type distIdx struct {
		dist float32
		idx  int
	}
	dists := make([]distIdx, idx.n)
	for i := range idx.n {
		dists[i] = distIdx{dist: distBuf[i], idx: i}
	}

	sort.Slice(dists, func(i, j int) bool {
		return dists[i].dist < dists[j].dist
	})

	result := make([]int, min(k, idx.n))
	for i := range result {
		result[i] = dists[i].idx
	}
	return result
}

func SquaredL2Single(a, b []float32) float32 {
	n := len(a)
	if n == 0 || n != len(b) {
		return 0
	}
	vecA := blas32.Vector{N: n, Inc: 1, Data: a}
	vecB := blas32.Vector{N: n, Inc: 1, Data: b}
	aNorm := blas32.Dot(vecA, vecA)
	bNorm := blas32.Dot(vecB, vecB)
	aDotB := blas32.Dot(vecA, vecB)
	d := aNorm + bNorm - 2*aDotB
	if d < 0 {
		return 0
	}
	return d
}

func VectorNormSquared(v []float32) float32 {
	if len(v) == 0 {
		return 0
	}
	vec := blas32.Vector{N: len(v), Inc: 1, Data: v}
	return blas32.Dot(vec, vec)
}
