package quantization

import "math"

// SAQRefiner implements Stochastic Additive Quantization refinement.
// It performs post-quantization coordinate descent to reduce reconstruction error
// by iteratively adjusting centroid assignments across subspaces.
type SAQRefiner struct {
	numSubspaces         int
	centroidsPerSubspace int
	subspaceDim          int
	centroids            [][][]float32
	maxIterations        int
	tolerance            float32
}

// NewSAQRefiner creates a new SAQ refiner with the given centroids.
// centroids is a 3D array: [numSubspaces][centroidsPerSubspace][subspaceDim]
// maxIterations defaults to 5 if <= 0
// tolerance defaults to 1e-6 if <= 0
func NewSAQRefiner(centroids [][][]float32, maxIterations int, tolerance float32) *SAQRefiner {
	if maxIterations <= 0 {
		maxIterations = 5
	}
	if tolerance <= 0 {
		tolerance = 1e-6
	}
	numSubspaces := len(centroids)
	centroidsPerSubspace := 0
	subspaceDim := 0
	if numSubspaces > 0 && len(centroids[0]) > 0 {
		centroidsPerSubspace = len(centroids[0])
		subspaceDim = len(centroids[0][0])
	}
	return &SAQRefiner{
		numSubspaces:         numSubspaces,
		centroidsPerSubspace: centroidsPerSubspace,
		subspaceDim:          subspaceDim,
		centroids:            centroids,
		maxIterations:        maxIterations,
		tolerance:            tolerance,
	}
}

// Refine performs coordinate descent refinement on the initial PQ code.
// For each subspace, it tries neighboring centroids (within delta of -2 to +2)
// and keeps the assignment that minimizes reconstruction error.
// The process iterates until no improvement is found or maxIterations is reached.
func (s *SAQRefiner) Refine(vector []float32, initialCode []uint8) []uint8 {
	if len(initialCode) != s.numSubspaces {
		return initialCode
	}

	code := make([]uint8, len(initialCode))
	copy(code, initialCode)

	for iter := 0; iter < s.maxIterations; iter++ {
		improved := false

		for m := 0; m < s.numSubspaces; m++ {
			bestDist := s.reconstructionError(vector, code)
			bestC := code[m]

			currentC := int(code[m])
			for delta := -2; delta <= 2; delta++ {
				newC := currentC + delta
				if newC < 0 || newC >= s.centroidsPerSubspace || newC == currentC {
					continue
				}

				testCode := make([]uint8, len(code))
				copy(testCode, code)
				testCode[m] = uint8(newC)

				dist := s.reconstructionError(vector, testCode)
				if dist < bestDist-s.tolerance {
					bestDist = dist
					bestC = uint8(newC)
					improved = true
				}
			}
			code[m] = bestC
		}

		if !improved {
			break
		}
	}

	return code
}

// reconstructionError computes the squared L2 distance between the original
// vector and the reconstructed vector from the given code.
func (s *SAQRefiner) reconstructionError(vector []float32, code []uint8) float32 {
	reconstructed := s.reconstruct(code)
	var err float32
	for i := range vector {
		if i < len(reconstructed) {
			d := vector[i] - reconstructed[i]
			err += d * d
		}
	}
	return err
}

// reconstruct builds a vector from the centroid assignments in the code.
func (s *SAQRefiner) reconstruct(code []uint8) []float32 {
	result := make([]float32, s.numSubspaces*s.subspaceDim)
	for m := 0; m < s.numSubspaces && m < len(code); m++ {
		c := int(code[m])
		if c >= s.centroidsPerSubspace {
			c = 0
		}
		start := m * s.subspaceDim
		for d := 0; d < s.subspaceDim; d++ {
			if start+d < len(result) && m < len(s.centroids) && c < len(s.centroids[m]) && d < len(s.centroids[m][c]) {
				result[start+d] = s.centroids[m][c][d]
			}
		}
	}
	return result
}

// BatchRefine applies refinement to multiple vectors in batch.
func (s *SAQRefiner) BatchRefine(vectors [][]float32, codes [][]uint8) [][]uint8 {
	result := make([][]uint8, len(vectors))
	for i := range vectors {
		if i < len(codes) {
			result[i] = s.Refine(vectors[i], codes[i])
		}
	}
	return result
}

// ComputeAverageImprovement calculates the average relative improvement
// in reconstruction error achieved by refinement.
// Returns a value between 0 and 1, where higher means more improvement.
func (s *SAQRefiner) ComputeAverageImprovement(vectors [][]float32, codes [][]uint8) float64 {
	if len(vectors) == 0 {
		return 0
	}
	var totalBefore, totalAfter float64
	for i := range vectors {
		if i < len(codes) {
			before := s.reconstructionError(vectors[i], codes[i])
			refined := s.Refine(vectors[i], codes[i])
			after := s.reconstructionError(vectors[i], refined)
			totalBefore += float64(before)
			totalAfter += float64(after)
		}
	}
	if totalBefore < 1e-10 {
		return 0
	}
	return (totalBefore - totalAfter) / totalBefore
}

// DeriveToleranceFromData computes an appropriate tolerance value based
// on the variance of the input data. This helps scale the tolerance
// to the magnitude of the data being quantized.
func (s *SAQRefiner) DeriveToleranceFromData(vectors [][]float32) float32 {
	if len(vectors) == 0 {
		return 1e-6
	}
	var totalVar float64
	dim := len(vectors[0])
	for d := 0; d < dim; d++ {
		var sum, sumSq float64
		for _, v := range vectors {
			if d < len(v) {
				val := float64(v[d])
				sum += val
				sumSq += val * val
			}
		}
		n := float64(len(vectors))
		mean := sum / n
		variance := sumSq/n - mean*mean
		totalVar += variance
	}
	avgVar := totalVar / float64(dim)
	return float32(math.Max(1e-8, avgVar*1e-4))
}
