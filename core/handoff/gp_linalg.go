package handoff

import (
	"fmt"
	"math"
	"sync"

	"github.com/viterin/vek"
)

// FlatMatrix is a row-major flat-packed matrix for cache-coherent SIMD access.
// All data lives in a single contiguous []float64 slice, enabling CPU cache
// prefetching and SIMD-friendly memory layout.
type FlatMatrix struct {
	Data []float64
	Rows int
	Cols int
}

// NewFlatMatrix allocates a zero-initialized flat matrix.
func NewFlatMatrix(rows, cols int) *FlatMatrix {
	return &FlatMatrix{
		Data: make([]float64, rows*cols),
		Rows: rows,
		Cols: cols,
	}
}

// At returns the element at (i, j).
func (m *FlatMatrix) At(i, j int) float64 {
	return m.Data[i*m.Cols+j]
}

// Set writes a value at (i, j).
func (m *FlatMatrix) Set(i, j int, v float64) {
	m.Data[i*m.Cols+j] = v
}

// Row returns a zero-copy slice into the underlying data for row i.
func (m *FlatMatrix) Row(i int) []float64 {
	start := i * m.Cols
	return m.Data[start : start+m.Cols]
}

// AddToDiagonal adds v to every diagonal element.
func (m *FlatMatrix) AddToDiagonal(v float64) {
	n := m.Rows
	if m.Cols < n {
		n = m.Cols
	}
	for i := range n {
		m.Data[i*m.Cols+i] += v
	}
}

// Pool for temporary float64 slices used in kernel/solve operations.
var vecPool = sync.Pool{New: func() any { return &pooledVec{} }}

type pooledVec struct{ data []float64 }

func getPooledVec(n int) *pooledVec {
	pv := vecPool.Get().(*pooledVec)
	if cap(pv.data) < n {
		pv.data = make([]float64, n)
	} else {
		pv.data = pv.data[:n]
		// Zero out reused memory.
		clear(pv.data)
	}
	return pv
}

func putPooledVec(v *pooledVec) {
	vecPool.Put(v)
}

// jitterLevels are successive jitter values tried when Cholesky fails.
// Each level is 10x the previous, starting from a very small perturbation.
var jitterLevels = [...]float64{1e-8, 1e-6, 1e-4}

// CholeskyFlat computes L such that A = L*Lᵀ, in-place on the lower triangle.
// Uses vek.Dot for the inner-loop dot products. If the matrix is not positive
// definite, adds progressively larger jitter to the diagonal and retries.
func CholeskyFlat(A *FlatMatrix) (*FlatMatrix, error) {
	if A.Rows != A.Cols {
		return nil, fmt.Errorf("cholesky: matrix must be square (%d x %d)", A.Rows, A.Cols)
	}
	n := A.Rows
	if n == 0 {
		return A, nil
	}

	L := NewFlatMatrix(n, n)

	if err := choleskyInto(A, L); err == nil {
		return L, nil
	}

	// Retry with increasing jitter.
	for _, jitter := range jitterLevels {
		A.AddToDiagonal(jitter)
		if err := choleskyInto(A, L); err == nil {
			return L, nil
		}
	}

	return nil, fmt.Errorf("cholesky: matrix not positive definite after jitter")
}

// choleskyInto performs the Cholesky decomposition of A into L.
func choleskyInto(A, L *FlatMatrix) error {
	n := A.Rows
	// Clear L.
	clear(L.Data)

	for i := range n {
		rowI := L.Row(i)

		for j := 0; j <= i; j++ {
			rowJ := L.Row(j)

			// Inner dot product: sum of L[i][0..j-1] * L[j][0..j-1]
			var dot float64
			if j > 0 {
				dot = vek.Dot(rowI[:j], rowJ[:j])
			}

			if i == j {
				val := A.At(i, i) - dot
				if val <= 0 {
					return fmt.Errorf("not positive definite at index %d", i)
				}
				L.Set(i, j, math.Sqrt(val))
			} else {
				diag := L.At(j, j)
				if diag == 0 {
					return fmt.Errorf("zero diagonal at index %d", j)
				}
				L.Set(i, j, (A.At(i, j)-dot)/diag)
			}
		}
	}
	return nil
}

// SolveTriangularFlat solves L*x = b (lower=true) or Lᵀ*x = b (lower=false).
// Uses vek.Dot for the accumulation step. Writes result into dst.
func SolveTriangularFlat(L *FlatMatrix, b []float64, lower bool, dst []float64) {
	n := len(b)

	if lower {
		// Forward substitution: L*x = b
		for i := range n {
			row := L.Row(i)
			var dot float64
			if i > 0 {
				dot = vek.Dot(row[:i], dst[:i])
			}
			diag := L.At(i, i)
			if diag != 0 {
				dst[i] = (b[i] - dot) / diag
			}
		}
	} else {
		// Back substitution: Lᵀ*x = b
		// L is stored as lower triangular, so Lᵀ[i][j] = L[j][i].
		// We iterate backwards and accumulate column-wise.
		copy(dst, b)
		for i := n - 1; i >= 0; i-- {
			diag := L.At(i, i)
			if diag != 0 {
				dst[i] /= diag
			}
			// Update earlier entries: dst[j] -= L[i][j] * dst[i] for j < i
			if i > 0 {
				row := L.Row(i)
				xi := dst[i]
				for j := range i {
					dst[j] -= row[j] * xi
				}
			}
		}
	}
}

// ScaledDistanceVek computes sqrt(Σ((x_d - x'_d)² / l_d²)) using vek ops.
// Uses a pooled temporary vector for the diff computation.
func ScaledDistanceVek(x1, x2, lengthscales []float64) float64 {
	n := len(x1)
	if len(x2) < n {
		n = len(x2)
	}
	nL := len(lengthscales)
	if n == 0 || nL == 0 {
		return 0
	}

	pv := getPooledVec(n)
	defer putPooledVec(pv)
	diff := pv.data

	// Compute (x1 - x2) / l element-wise.
	for d := range n {
		l := lengthscales[nL-1]
		if d < nL {
			l = lengthscales[d]
		}
		diff[d] = (x1[d] - x2[d]) / l
	}

	// Sum of squares via dot product with itself, then sqrt.
	return math.Sqrt(vek.Dot(diff, diff))
}

// KernelMatrixFlat computes the N×N kernel matrix K[i,j] = matern25(obs[i], obs[j])
// plus noise variance on the diagonal. Returns a flat-packed symmetric matrix.
func KernelMatrixFlat(obs []*GPObservation, hp *GPHyperparams) *FlatMatrix {
	n := len(obs)
	K := NewFlatMatrix(n, n)

	for i := range n {
		fi := obs[i].Features()
		for j := i; j < n; j++ {
			fj := obs[j].Features()
			val := matern25KernelVek(fi, fj, hp)
			K.Set(i, j, val)
			if i != j {
				K.Set(j, i, val)
			}
		}
		// Add noise to diagonal.
		K.Set(i, i, K.At(i, i)+hp.NoiseVariance)
	}

	return K
}

// KernelVectorFlat computes k* = [kernel(x*, obs[0]), ..., kernel(x*, obs[N-1])].
func KernelVectorFlat(query []float64, obs []*GPObservation, hp *GPHyperparams) []float64 {
	n := len(obs)
	kStar := make([]float64, n)
	for i := range n {
		kStar[i] = matern25KernelVek(query, obs[i].Features(), hp)
	}
	return kStar
}

// DotVek computes the dot product of two slices using SIMD via vek.Dot.
func DotVek(a, b []float64) float64 {
	return vek.Dot(a, b)
}

// matern25KernelVek computes the Matern 2.5 kernel between two feature vectors
// using SIMD-accelerated scaled distance.
func matern25KernelVek(x1, x2 []float64, hp *GPHyperparams) float64 {
	r := ScaledDistanceVek(x1, x2, hp.Lengthscales)
	sqrt5 := math.Sqrt(5.0)
	sqrt5r := sqrt5 * r
	return hp.SignalVariance * (1.0 + sqrt5r + 5.0*r*r/3.0) * math.Exp(-sqrt5r)
}
