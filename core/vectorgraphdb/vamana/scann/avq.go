package scann

import (
	"math"
)

type AVQCodebook struct {
	weights    []float32
	scales     []float32
	dim        int
	anisotropy float64
}

func NewAVQCodebook(dim int, anisotropy float64) *AVQCodebook {
	return &AVQCodebook{
		weights:    make([]float32, dim),
		scales:     make([]float32, dim),
		dim:        dim,
		anisotropy: anisotropy,
	}
}

func (c *AVQCodebook) Train(residuals [][]float32) {
	if len(residuals) == 0 {
		return
	}

	variances := make([]float64, c.dim)
	means := make([]float64, c.dim)
	n := float64(len(residuals))

	for _, r := range residuals {
		for j, v := range r {
			means[j] += float64(v)
		}
	}
	for j := range means {
		means[j] /= n
	}

	for _, r := range residuals {
		for j, v := range r {
			d := float64(v) - means[j]
			variances[j] += d * d
		}
	}
	for j := range variances {
		variances[j] /= n
	}

	var totalVar float64
	for _, v := range variances {
		totalVar += v
	}
	if totalVar == 0 {
		totalVar = 1
	}

	for j := range c.dim {
		importance := variances[j] / totalVar
		c.weights[j] = float32(c.anisotropy*importance + (1-c.anisotropy)/float64(c.dim))

		if variances[j] > 0 {
			c.scales[j] = float32(1.0 / math.Sqrt(variances[j]))
		} else {
			c.scales[j] = 1.0
		}
	}
}

func (c *AVQCodebook) Encode(residual []float32) []float32 {
	encoded := make([]float32, c.dim)
	for j, v := range residual {
		encoded[j] = v * c.scales[j]
	}
	return encoded
}

func (c *AVQCodebook) WeightedDot(query, encoded []float32) float64 {
	var sum float64
	for j := range query {
		sum += float64(query[j]) * float64(encoded[j]) * float64(c.weights[j])
	}
	return sum
}

func (c *AVQCodebook) Weights() []float32 {
	return c.weights
}

func (c *AVQCodebook) Scales() []float32 {
	return c.scales
}

type PartitionCodebooks struct {
	codebooks []*AVQCodebook
	centroids [][]float32
}

func NewPartitionCodebooks(centroids [][]float32, anisotropy float64) *PartitionCodebooks {
	dim := 0
	if len(centroids) > 0 && len(centroids[0]) > 0 {
		dim = len(centroids[0])
	}

	codebooks := make([]*AVQCodebook, len(centroids))
	for i := range codebooks {
		codebooks[i] = NewAVQCodebook(dim, anisotropy)
	}

	return &PartitionCodebooks{
		codebooks: codebooks,
		centroids: centroids,
	}
}

func (pc *PartitionCodebooks) TrainPartition(partitionID int, vectors [][]float32) {
	if partitionID >= len(pc.codebooks) || len(vectors) == 0 {
		return
	}

	centroid := pc.centroids[partitionID]
	residuals := make([][]float32, len(vectors))

	for i, vec := range vectors {
		residuals[i] = computeResidual(vec, centroid)
	}

	pc.codebooks[partitionID].Train(residuals)
}

func (pc *PartitionCodebooks) EncodeVector(partitionID int, vec []float32) []float32 {
	if partitionID >= len(pc.codebooks) {
		return vec
	}

	residual := computeResidual(vec, pc.centroids[partitionID])
	return pc.codebooks[partitionID].Encode(residual)
}

func (pc *PartitionCodebooks) AsymmetricDistance(query []float32, partitionID int, encoded []float32) float64 {
	if partitionID >= len(pc.codebooks) {
		return 0
	}

	queryResidual := computeResidual(query, pc.centroids[partitionID])
	return -pc.codebooks[partitionID].WeightedDot(queryResidual, encoded)
}

func computeResidual(vec, centroid []float32) []float32 {
	residual := make([]float32, len(vec))
	for i := range vec {
		residual[i] = vec[i] - centroid[i]
	}
	return residual
}
