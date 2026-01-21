package hnsw

import (
	"math"
	"sync"

	"gonum.org/v1/gonum/mat"
)

// FINGERAccelerator implements the FINGER (Fast INference of Graph-based NEarest
// neighbor Retrieval) algorithm for accelerating HNSW graph traversal.
// It uses low-rank approximation to skip obviously-far candidates without
// computing exact distances.
type FINGERAccelerator struct {
	mu               sync.RWMutex
	projectionMatrix *mat.Dense
	lowRank          int
	skipThreshold    float64
	edgeResiduals    map[string][]float32
}

// NewFINGERAccelerator creates a new FINGER accelerator with the given dimension
// and low-rank approximation size. If lowRank is <= 0, it defaults to min(32, dim/4).
func NewFINGERAccelerator(dim, lowRank int) *FINGERAccelerator {
	if lowRank <= 0 {
		lowRank = min(32, dim/4)
	}
	if lowRank < 1 {
		lowRank = 1
	}
	return &FINGERAccelerator{
		lowRank:       lowRank,
		skipThreshold: 0.7,
		edgeResiduals: make(map[string][]float32),
	}
}

// PrecomputeResiduals precomputes edge residual vectors and builds the low-rank
// projection matrix using SVD. This should be called during index construction
// or after bulk insertions.
func (f *FINGERAccelerator) PrecomputeResiduals(vectors map[string][]float32, neighbors map[string][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	residuals := make([][]float64, 0)
	for fromID, neighborIDs := range neighbors {
		fromVec := vectors[fromID]
		for _, toID := range neighborIDs {
			toVec := vectors[toID]
			if len(fromVec) == 0 || len(toVec) == 0 {
				continue
			}
			key := fromID + "->" + toID
			residual := make([]float32, len(fromVec))
			residualF64 := make([]float64, len(fromVec))
			for i := range fromVec {
				residual[i] = toVec[i] - fromVec[i]
				residualF64[i] = float64(residual[i])
			}
			f.edgeResiduals[key] = residual
			residuals = append(residuals, residualF64)
		}
	}

	if len(residuals) > f.lowRank {
		f.computeProjectionMatrix(residuals)
	}
}

// computeProjectionMatrix computes the low-rank projection matrix using SVD
// of the residual vectors. The projection matrix consists of the top-k right
// singular vectors.
func (f *FINGERAccelerator) computeProjectionMatrix(residuals [][]float64) {
	if len(residuals) == 0 {
		return
	}
	dim := len(residuals[0])
	data := make([]float64, len(residuals)*dim)
	for i, r := range residuals {
		copy(data[i*dim:], r)
	}
	A := mat.NewDense(len(residuals), dim, data)

	var svd mat.SVD
	if !svd.Factorize(A, mat.SVDThin) {
		return
	}
	var vt mat.Dense
	svd.VTo(&vt)

	rows := min(f.lowRank, vt.RawMatrix().Rows)
	f.projectionMatrix = mat.NewDense(rows, dim, nil)
	for i := 0; i < rows; i++ {
		for j := 0; j < dim; j++ {
			f.projectionMatrix.Set(i, j, vt.At(i, j))
		}
	}
}

// ShouldSkipCandidate determines if a candidate should be skipped based on
// the low-rank approximation. Returns true if the candidate is likely too far
// to improve the current best result.
func (f *FINGERAccelerator) ShouldSkipCandidate(query []float32, currentBestSim float64, fromID, candidateID string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.projectionMatrix == nil {
		return false
	}

	key := fromID + "->" + candidateID
	residual, ok := f.edgeResiduals[key]
	if !ok {
		return false
	}

	queryProj := f.project(query)
	residualProj := f.project(residual)

	angle := f.estimateAngle(queryProj, residualProj)
	return angle > f.skipThreshold
}

// project projects a vector to the low-rank space using the projection matrix.
func (f *FINGERAccelerator) project(vec []float32) []float64 {
	if f.projectionMatrix == nil {
		return nil
	}
	rows, cols := f.projectionMatrix.Dims()
	result := make([]float64, rows)
	for i := 0; i < rows; i++ {
		var sum float64
		for j := 0; j < cols && j < len(vec); j++ {
			sum += f.projectionMatrix.At(i, j) * float64(vec[j])
		}
		result[i] = sum
	}
	return result
}

// estimateAngle estimates the angle between two vectors in the projected space.
// Returns a value in [0, 1] representing the normalized angle (angle/pi).
func (f *FINGERAccelerator) estimateAngle(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		if i < len(b) {
			dot += a[i] * b[i]
			magA += a[i] * a[i]
			magB += b[i] * b[i]
		}
	}
	if magA < 1e-10 || magB < 1e-10 {
		return 0
	}
	cos := dot / (math.Sqrt(magA) * math.Sqrt(magB))
	return math.Acos(math.Max(-1, math.Min(1, cos))) / math.Pi
}

// SetSkipThreshold sets the threshold for skipping candidates.
// Values closer to 0 skip more aggressively, values closer to 1 are more conservative.
func (f *FINGERAccelerator) SetSkipThreshold(threshold float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skipThreshold = threshold
}

// GetSkipThreshold returns the current skip threshold.
func (f *FINGERAccelerator) GetSkipThreshold() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.skipThreshold
}

// HasProjectionMatrix returns true if the projection matrix has been computed.
func (f *FINGERAccelerator) HasProjectionMatrix() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.projectionMatrix != nil
}

// GetEdgeResidualCount returns the number of precomputed edge residuals.
func (f *FINGERAccelerator) GetEdgeResidualCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.edgeResiduals)
}

// Clear resets the accelerator, removing all precomputed data.
func (f *FINGERAccelerator) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectionMatrix = nil
	f.edgeResiduals = make(map[string][]float32)
}
