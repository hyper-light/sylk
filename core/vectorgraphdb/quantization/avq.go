package quantization

import (
	"math"
	"sync"

	"gonum.org/v1/gonum/mat"
)

type AnisotropicVQ struct {
	mu               sync.RWMutex
	parallelWeight   float64
	queryCovariance  *mat.SymDense
	dim              int
	observationCount int
}

func NewAnisotropicVQ(dim int) *AnisotropicVQ {
	if dim <= 0 {
		dim = 1
	}

	cov := mat.NewSymDense(dim, nil)
	for i := 0; i < dim; i++ {
		cov.SetSym(i, i, 1.0)
	}
	return &AnisotropicVQ{
		dim:             dim,
		parallelWeight:  10.0,
		queryCovariance: cov,
	}
}

// AnisotropicLoss computes loss = parallelWeight*parallelError² + orthogonalError²
// where parallelError is the projection of (original-reconstructed) onto queryDir.
func (a *AnisotropicVQ) AnisotropicLoss(original, reconstructed, queryDir []float32) float64 {
	if len(original) == 0 || len(reconstructed) == 0 {
		return 0
	}

	diff := make([]float64, len(original))
	for i := range original {
		diff[i] = float64(original[i] - reconstructed[i])
	}

	var parallelError float64
	for i := range diff {
		if i < len(queryDir) {
			parallelError += diff[i] * float64(queryDir[i])
		}
	}

	var totalError float64
	for _, d := range diff {
		totalError += d * d
	}

	orthogonalErrorSq := math.Max(0, totalError-parallelError*parallelError)

	a.mu.RLock()
	weight := a.parallelWeight
	a.mu.RUnlock()

	return weight*parallelError*parallelError + orthogonalErrorSq
}

// UpdateQueryDistribution updates covariance with exponential moving average:
// C = (1 - alpha) * C + alpha * (query ⊗ query)
func (a *AnisotropicVQ) UpdateQueryDistribution(query []float32) {
	if len(query) == 0 {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.observationCount++

	alpha := 0.01
	if a.observationCount < 100 {
		alpha = 1.0 / float64(a.observationCount)
	}

	for i := 0; i < a.dim && i < len(query); i++ {
		for j := i; j < a.dim && j < len(query); j++ {
			old := a.queryCovariance.At(i, j)
			update := float64(query[i]) * float64(query[j])
			a.queryCovariance.SetSym(i, j, (1-alpha)*old+alpha*update)
		}
	}
}

// GetPrincipalQueryDirection returns the eigenvector for the largest eigenvalue
// of the query covariance matrix. Falls back to uniform direction on failure.
func (a *AnisotropicVQ) GetPrincipalQueryDirection() []float32 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var eig mat.EigenSym
	ok := eig.Factorize(a.queryCovariance, true)
	if !ok {
		result := make([]float32, a.dim)
		uniformVal := float32(1.0 / math.Sqrt(float64(a.dim)))
		for i := range result {
			result[i] = uniformVal
		}
		return result
	}

	var vectors mat.Dense
	eig.VectorsTo(&vectors)

	result := make([]float32, a.dim)
	for i := 0; i < a.dim; i++ {
		result[i] = float32(vectors.At(i, a.dim-1))
	}
	return result
}

func (a *AnisotropicVQ) SetParallelWeight(weight float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.parallelWeight = weight
}

func (a *AnisotropicVQ) GetParallelWeight() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.parallelWeight
}

// DeriveParallelWeight computes weight = 10/avgVariance, clamped to [1.0, 100.0].
func (a *AnisotropicVQ) DeriveParallelWeight(samples [][]float32) float64 {
	if len(samples) < 10 {
		return 10.0
	}

	var totalVar float64
	for d := 0; d < a.dim; d++ {
		var sum, sumSq float64
		count := 0
		for _, s := range samples {
			if d < len(s) {
				v := float64(s[d])
				sum += v
				sumSq += v * v
				count++
			}
		}
		if count > 0 {
			n := float64(count)
			mean := sum / n
			variance := sumSq/n - mean*mean
			totalVar += variance
		}
	}

	avgVar := totalVar / float64(a.dim)
	weight := 10.0 / math.Max(0.01, avgVar)
	return math.Max(1.0, math.Min(100.0, weight))
}

func (a *AnisotropicVQ) ObservationCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.observationCount
}

func (a *AnisotropicVQ) Dim() int {
	return a.dim
}

func (a *AnisotropicVQ) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.observationCount = 0
	for i := 0; i < a.dim; i++ {
		for j := 0; j < a.dim; j++ {
			if i == j {
				a.queryCovariance.SetSym(i, j, 1.0)
			} else {
				a.queryCovariance.SetSym(i, j, 0.0)
			}
		}
	}
}

func (a *AnisotropicVQ) GetQueryCovariance() *mat.SymDense {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.queryCovariance == nil {
		return nil
	}

	n := a.dim
	data := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			val := a.queryCovariance.At(i, j)
			data[i*n+j] = val
			data[j*n+i] = val
		}
	}
	return mat.NewSymDense(n, data)
}
