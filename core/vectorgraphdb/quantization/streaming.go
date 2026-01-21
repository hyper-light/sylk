package quantization

import (
	"math"
	"sync"
)

// StreamingQuantizer maintains codebook consistency under streaming updates.
// It implements CoDEQ-style drift detection and local centroid updates
// without requiring global retraining.
type StreamingQuantizer struct {
	mu              sync.RWMutex
	centroids       [][][]float32 // [subspace][centroid][dim]
	numSubspaces    int
	centroidsPerSub int
	subspaceDim     int

	// Per-centroid statistics for drift detection
	centroidCounts [][]int       // [subspace][centroid] -> assignment count
	centroidSums   [][][]float64 // [subspace][centroid][dim] -> vector sum

	// Drift detection configuration
	driftThreshold float64
	checkInterval  int
	insertCount    int
}

// NewStreamingQuantizer creates a new StreamingQuantizer from existing centroids.
// The centroids should be in [numSubspaces][centroidsPerSubspace][subspaceDim] format.
// driftThreshold controls when centroids are updated; use 0 for default (0.01).
func NewStreamingQuantizer(centroids [][][]float32, driftThreshold float64) *StreamingQuantizer {
	numSubspaces := len(centroids)
	centroidsPerSub := 0
	subspaceDim := 0
	if numSubspaces > 0 && len(centroids[0]) > 0 {
		centroidsPerSub = len(centroids[0])
		if len(centroids[0][0]) > 0 {
			subspaceDim = len(centroids[0][0])
		}
	}

	counts := make([][]int, numSubspaces)
	sums := make([][][]float64, numSubspaces)
	for m := 0; m < numSubspaces; m++ {
		counts[m] = make([]int, centroidsPerSub)
		sums[m] = make([][]float64, centroidsPerSub)
		for c := 0; c < centroidsPerSub; c++ {
			sums[m][c] = make([]float64, subspaceDim)
		}
	}

	if driftThreshold <= 0 {
		driftThreshold = 0.01
	}

	return &StreamingQuantizer{
		centroids:       centroids,
		numSubspaces:    numSubspaces,
		centroidsPerSub: centroidsPerSub,
		subspaceDim:     subspaceDim,
		centroidCounts:  counts,
		centroidSums:    sums,
		driftThreshold:  driftThreshold,
		checkInterval:   100,
	}
}

// OnInsert processes a new vector insertion, updating centroid statistics
// and returning the PQ code for the vector.
func (s *StreamingQuantizer) OnInsert(vector []float32) []uint8 {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := s.encode(vector)

	// Update centroid statistics
	for m, c := range code {
		if m >= s.numSubspaces || int(c) >= s.centroidsPerSub {
			continue
		}
		s.centroidCounts[m][c]++
		start := m * s.subspaceDim
		for d := 0; d < s.subspaceDim; d++ {
			if start+d < len(vector) {
				s.centroidSums[m][c][d] += float64(vector[start+d])
			}
		}
	}

	s.insertCount++
	if s.insertCount%s.checkInterval == 0 {
		s.maybeUpdateCentroids()
	}

	return code
}

// encode finds the nearest centroid in each subspace for the given vector.
func (s *StreamingQuantizer) encode(vector []float32) []uint8 {
	code := make([]uint8, s.numSubspaces)
	for m := 0; m < s.numSubspaces; m++ {
		start := m * s.subspaceDim
		end := start + s.subspaceDim
		if end > len(vector) {
			end = len(vector)
		}
		subvec := vector[start:end]

		bestC := 0
		bestDist := float32(math.MaxFloat32)
		for c := 0; c < s.centroidsPerSub && c < len(s.centroids[m]); c++ {
			dist := s.squaredL2(subvec, s.centroids[m][c])
			if dist < bestDist {
				bestDist = dist
				bestC = c
			}
		}
		code[m] = uint8(bestC)
	}
	return code
}

// maybeUpdateCentroids checks each centroid for drift and updates if needed.
func (s *StreamingQuantizer) maybeUpdateCentroids() {
	for m := 0; m < s.numSubspaces; m++ {
		for c := 0; c < s.centroidsPerSub; c++ {
			count := s.centroidCounts[m][c]
			if count < 10 {
				continue // Not enough data
			}

			// Compute new centroid from running mean
			newCentroid := make([]float32, s.subspaceDim)
			for d := 0; d < s.subspaceDim; d++ {
				newCentroid[d] = float32(s.centroidSums[m][c][d] / float64(count))
			}

			// Check drift from old centroid
			drift := s.squaredL2(newCentroid, s.centroids[m][c])
			if drift > float32(s.driftThreshold) {
				// Update centroid (no global retrain!)
				copy(s.centroids[m][c], newCentroid)
			}
		}
	}
}

func (s *StreamingQuantizer) squaredL2(a, b []float32) float32 {
	return SquaredL2Single(a, b)
}

// GetDriftStatistics returns statistics about centroid drift.
func (s *StreamingQuantizer) GetDriftStatistics() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var totalDrift, maxDrift float64
	count := 0
	for m := 0; m < s.numSubspaces; m++ {
		for c := 0; c < s.centroidsPerSub; c++ {
			if s.centroidCounts[m][c] < 10 {
				continue
			}
			newCentroid := make([]float32, s.subspaceDim)
			for d := 0; d < s.subspaceDim; d++ {
				newCentroid[d] = float32(s.centroidSums[m][c][d] / float64(s.centroidCounts[m][c]))
			}
			drift := float64(s.squaredL2(newCentroid, s.centroids[m][c]))
			totalDrift += drift
			if drift > maxDrift {
				maxDrift = drift
			}
			count++
		}
	}

	avgDrift := 0.0
	if count > 0 {
		avgDrift = totalDrift / float64(count)
	}

	return map[string]float64{
		"avgDrift":    avgDrift,
		"maxDrift":    maxDrift,
		"insertCount": float64(s.insertCount),
	}
}

// Reset clears all accumulated statistics while preserving centroids.
func (s *StreamingQuantizer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for m := 0; m < s.numSubspaces; m++ {
		for c := 0; c < s.centroidsPerSub; c++ {
			s.centroidCounts[m][c] = 0
			for d := range s.centroidSums[m][c] {
				s.centroidSums[m][c][d] = 0
			}
		}
	}
	s.insertCount = 0
}

// DeriveDriftThreshold computes an appropriate drift threshold from sample data.
// It estimates threshold based on data variance (10% of average variance).
func (s *StreamingQuantizer) DeriveDriftThreshold(samples [][]float32) float64 {
	if len(samples) < 100 {
		return 0.01
	}
	var totalVar float64
	dim := s.numSubspaces * s.subspaceDim
	for d := 0; d < dim; d++ {
		var sum, sumSq float64
		for _, v := range samples {
			if d < len(v) {
				sum += float64(v[d])
				sumSq += float64(v[d]) * float64(v[d])
			}
		}
		n := float64(len(samples))
		variance := sumSq/n - (sum/n)*(sum/n)
		totalVar += variance
	}
	avgVar := totalVar / float64(dim)
	return math.Max(0.001, avgVar*0.1)
}

// SetDriftThreshold updates the drift threshold.
func (s *StreamingQuantizer) SetDriftThreshold(threshold float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if threshold > 0 {
		s.driftThreshold = threshold
	}
}

// SetCheckInterval updates how often drift is checked (every N inserts).
func (s *StreamingQuantizer) SetCheckInterval(interval int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if interval > 0 {
		s.checkInterval = interval
	}
}

// GetCentroids returns a copy of the current centroids.
func (s *StreamingQuantizer) GetCentroids() [][][]float32 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([][][]float32, s.numSubspaces)
	for m := 0; m < s.numSubspaces; m++ {
		result[m] = make([][]float32, s.centroidsPerSub)
		for c := 0; c < s.centroidsPerSub; c++ {
			result[m][c] = make([]float32, s.subspaceDim)
			copy(result[m][c], s.centroids[m][c])
		}
	}
	return result
}

// InsertCount returns the number of vectors inserted since last reset.
func (s *StreamingQuantizer) InsertCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.insertCount
}
