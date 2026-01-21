package quantization

import (
	"math"
	"sync"
)

// FastScanPQ implements 4-bit Product Quantization optimized for SIMD scanning.
// Uses 16 centroids per subspace (4-bit codes) so LUTs fit in SIMD registers.
// Based on FAISS FastScan approach for 3-6x speedup over 8-bit PQ.
type FastScanPQ struct {
	mu                   sync.RWMutex
	centroids            [][][]float32 // [numSubspaces][16][subspaceDim]
	numSubspaces         int
	subspaceDim          int
	centroidsPerSubspace int // Always 16 for 4-bit codes
}

// NewFastScanPQ creates a new 4-bit FastScan Product Quantizer.
// dim is the vector dimension, numSubspaces is how many subspaces to divide into.
func NewFastScanPQ(dim, numSubspaces int) *FastScanPQ {
	subspaceDim := dim / numSubspaces
	centroids := make([][][]float32, numSubspaces)
	for m := range centroids {
		centroids[m] = make([][]float32, 16)
		for c := range centroids[m] {
			centroids[m][c] = make([]float32, subspaceDim)
		}
	}
	return &FastScanPQ{
		centroids:            centroids,
		numSubspaces:         numSubspaces,
		subspaceDim:          subspaceDim,
		centroidsPerSubspace: 16,
	}
}

// Train learns centroids for each subspace using k-means clustering.
func (f *FastScanPQ) Train(vectors [][]float32) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for m := 0; m < f.numSubspaces; m++ {
		subvectors := make([][]float32, len(vectors))
		for i, v := range vectors {
			start := m * f.subspaceDim
			end := start + f.subspaceDim
			if end > len(v) {
				end = len(v)
			}
			subvectors[i] = v[start:end]
		}
		f.trainSubspace(m, subvectors)
	}
}

// trainSubspace runs k-means for a single subspace with 16 centroids.
func (f *FastScanPQ) trainSubspace(m int, subvectors [][]float32) {
	if len(subvectors) < 16 {
		return
	}
	assignments := make([]int, len(subvectors))
	for i := range assignments {
		assignments[i] = i % 16
	}

	for iter := 0; iter < 10; iter++ {
		for c := 0; c < 16; c++ {
			count := 0
			for d := 0; d < f.subspaceDim; d++ {
				f.centroids[m][c][d] = 0
			}
			for i, a := range assignments {
				if a == c {
					count++
					for d := 0; d < f.subspaceDim && d < len(subvectors[i]); d++ {
						f.centroids[m][c][d] += subvectors[i][d]
					}
				}
			}
			if count > 0 {
				for d := 0; d < f.subspaceDim; d++ {
					f.centroids[m][c][d] /= float32(count)
				}
			}
		}

		for i, sv := range subvectors {
			bestC := 0
			bestDist := float32(math.MaxFloat32)
			for c := 0; c < 16; c++ {
				dist := f.squaredL2(sv, f.centroids[m][c])
				if dist < bestDist {
					bestDist = dist
					bestC = c
				}
			}
			assignments[i] = bestC
		}
	}
}

// Encode quantizes a vector into packed 4-bit codes (2 subspaces per byte).
func (f *FastScanPQ) Encode(vector []float32) []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()

	codes := make([]byte, (f.numSubspaces+1)/2)
	for m := 0; m < f.numSubspaces; m++ {
		start := m * f.subspaceDim
		end := start + f.subspaceDim
		if end > len(vector) {
			end = len(vector)
		}
		subvec := vector[start:end]

		bestC := 0
		bestDist := float32(math.MaxFloat32)
		for c := 0; c < 16; c++ {
			dist := f.squaredL2(subvec, f.centroids[m][c])
			if dist < bestDist {
				bestDist = dist
				bestC = c
			}
		}

		byteIdx := m / 2
		if m%2 == 0 {
			codes[byteIdx] |= byte(bestC)
		} else {
			codes[byteIdx] |= byte(bestC << 4)
		}
	}
	return codes
}

// ComputeDistanceTable precomputes squared L2 distances from query to all centroids.
// Returns 16-entry LUTs per subspace that fit in SIMD registers.
func (f *FastScanPQ) ComputeDistanceTable(query []float32) [][]float32 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	tables := make([][]float32, f.numSubspaces)
	for m := 0; m < f.numSubspaces; m++ {
		tables[m] = make([]float32, 16)
		start := m * f.subspaceDim
		end := start + f.subspaceDim
		if end > len(query) {
			end = len(query)
		}
		subquery := query[start:end]
		for c := 0; c < 16; c++ {
			tables[m][c] = f.squaredL2(subquery, f.centroids[m][c])
		}
	}
	return tables
}

// AsymmetricDistance computes approximate squared L2 distance using precomputed tables.
// This is the core operation that benefits from SIMD when LUTs fit in registers.
func (f *FastScanPQ) AsymmetricDistance(tables [][]float32, code []byte) float32 {
	var dist float32
	for m := 0; m < f.numSubspaces; m++ {
		byteIdx := m / 2
		var c int
		if m%2 == 0 {
			c = int(code[byteIdx] & 0x0F)
		} else {
			c = int(code[byteIdx] >> 4)
		}
		if m < len(tables) && c < len(tables[m]) {
			dist += tables[m][c]
		}
	}
	return dist
}

func (f *FastScanPQ) squaredL2(a, b []float32) float32 {
	return SquaredL2Single(a, b)
}
