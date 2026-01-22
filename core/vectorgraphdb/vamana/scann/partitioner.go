package scann

import (
	"math"
	"math/bits"
	"math/rand/v2"
	"runtime"
	"sync"

	"github.com/viterin/vek/vek32"
)

type Partitioner struct {
	centroids       [][]float32
	centroidsFlat   []float32
	centroidNormsSq []float64
	dim             int
	numParts        int
	assignments     []uint32
}

func NewPartitioner(numPartitions, dim int) *Partitioner {
	return &Partitioner{
		centroids: make([][]float32, numPartitions),
		dim:       dim,
		numParts:  numPartitions,
	}
}

func (p *Partitioner) Train(vectors [][]float32, maxIters int) {
	n := len(vectors)
	if n == 0 || p.numParts == 0 {
		return
	}

	k := p.numParts
	if k > n {
		k = n
	}

	vectorNormsSq := make([]float64, n)
	for i, vec := range vectors {
		vectorNormsSq[i] = float64(vek32.Dot(vec, vec))
	}

	p.initCentroidsKMeansPP(vectors, k)
	p.prepareCentroidsBatch()
	p.assignments = make([]uint32, n)

	for iter := range maxIters {
		changed := p.assignToCentroidsWithNorms(vectors, vectorNormsSq)
		p.updateCentroids(vectors)
		p.prepareCentroidsBatch()

		if iter > 0 && changed < n/1000 {
			break
		}
	}
}

func (p *Partitioner) TrainFast(vectors [][]float32, maxIters int) {
	n := len(vectors)
	if n == 0 || p.numParts == 0 {
		return
	}

	k := p.numParts
	if k > n {
		k = n
	}

	sampleSize := bits.Len(uint(n)) * k
	if sampleSize > n {
		sampleSize = n
	}

	indices := rand.Perm(n)[:sampleSize]
	sample := make([][]float32, sampleSize)
	for i, idx := range indices {
		sample[i] = vectors[idx]
	}

	sampleNormsSq := make([]float64, sampleSize)
	for i, vec := range sample {
		sampleNormsSq[i] = float64(vek32.Dot(vec, vec))
	}

	p.initCentroidsKMeansPPSample(sample, k)
	p.prepareCentroidsBatch()

	sampleAssignments := make([]uint32, sampleSize)
	origAssignments := p.assignments
	p.assignments = sampleAssignments

	for iter := range maxIters {
		changed := p.assignToCentroidsWithNorms(sample, sampleNormsSq)
		p.updateCentroidsSample(sample)
		p.prepareCentroidsBatch()

		if iter > 0 && changed < k {
			break
		}
	}

	p.assignments = origAssignments
	if p.assignments == nil || len(p.assignments) != n {
		p.assignments = make([]uint32, n)
	}

	fullNormsSq := make([]float64, n)
	for i, vec := range vectors {
		fullNormsSq[i] = float64(vek32.Dot(vec, vec))
	}
	p.assignToCentroidsWithNorms(vectors, fullNormsSq)
}

func (p *Partitioner) initCentroidsKMeansPPSample(sample [][]float32, k int) {
	n := len(sample)

	first := rand.IntN(n)
	p.centroids[0] = copyVector(sample[first])

	distances := make([]float64, n)
	for i := range n {
		distances[i] = math.MaxFloat64
	}

	for c := 1; c < k; c++ {
		var totalDist float64
		for i, vec := range sample {
			d := squaredL2(vec, p.centroids[c-1])
			if d < distances[i] {
				distances[i] = d
			}
			totalDist += distances[i]
		}

		if totalDist == 0 {
			p.centroids[c] = copyVector(sample[rand.IntN(n)])
			continue
		}

		threshold := rand.Float64() * totalDist
		cumulative := 0.0
		selected := n - 1
		for i, d := range distances {
			cumulative += d
			if cumulative >= threshold {
				selected = i
				break
			}
		}
		p.centroids[c] = copyVector(sample[selected])
	}
}

func (p *Partitioner) updateCentroidsSample(sample [][]float32) {
	counts := make([]int, p.numParts)
	sums := make([][]float64, p.numParts)
	for i := range p.numParts {
		sums[i] = make([]float64, p.dim)
	}

	for i, vec := range sample {
		part := p.assignments[i]
		counts[part]++
		for j, v := range vec {
			sums[part][j] += float64(v)
		}
	}

	for i := range p.numParts {
		if counts[i] == 0 {
			continue
		}
		if p.centroids[i] == nil {
			p.centroids[i] = make([]float32, p.dim)
		}
		invCount := 1.0 / float64(counts[i])
		for j := range p.dim {
			p.centroids[i][j] = float32(sums[i][j] * invCount)
		}
	}
}

func (p *Partitioner) initCentroidsKMeansPP(vectors [][]float32, k int) {
	n := len(vectors)

	first := rand.IntN(n)
	p.centroids[0] = copyVector(vectors[first])

	distances := make([]float64, n)
	for i := range n {
		distances[i] = math.MaxFloat64
	}

	for c := 1; c < k; c++ {
		var totalDist float64
		for i, vec := range vectors {
			d := squaredL2(vec, p.centroids[c-1])
			if d < distances[i] {
				distances[i] = d
			}
			totalDist += distances[i]
		}

		if totalDist == 0 {
			p.centroids[c] = copyVector(vectors[rand.IntN(n)])
			continue
		}

		threshold := rand.Float64() * totalDist
		cumulative := 0.0
		selected := n - 1
		for i, d := range distances {
			cumulative += d
			if cumulative >= threshold {
				selected = i
				break
			}
		}
		p.centroids[c] = copyVector(vectors[selected])
	}
}

func (p *Partitioner) assignToCentroids(vectors [][]float32) int {
	vectorNormsSq := make([]float64, len(vectors))
	for i, vec := range vectors {
		var normSq float64
		for _, v := range vec {
			normSq += float64(v) * float64(v)
		}
		vectorNormsSq[i] = normSq
	}
	return p.assignToCentroidsWithNorms(vectors, vectorNormsSq)
}

func (p *Partitioner) assignToCentroidsWithNorms(vectors [][]float32, vectorNormsSq []float64) int {
	n := len(vectors)
	numWorkers := runtime.GOMAXPROCS(0)
	chunkSize := (n + numWorkers - 1) / numWorkers

	var changed int64
	var mu sync.Mutex
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			localChanged := 0

			for i := start; i < end; i++ {
				nearest := p.findNearestWithNorm(vectors[i], vectorNormsSq[i])
				if p.assignments[i] != nearest {
					p.assignments[i] = nearest
					localChanged++
				}
			}

			mu.Lock()
			changed += int64(localChanged)
			mu.Unlock()
		}(start, end)
	}

	wg.Wait()
	return int(changed)
}

func (p *Partitioner) prepareCentroidsBatch() {
	if p.centroidsFlat == nil || len(p.centroidsFlat) != p.numParts*p.dim {
		p.centroidsFlat = make([]float32, p.numParts*p.dim)
		p.centroidNormsSq = make([]float64, p.numParts)
	}

	for i, c := range p.centroids {
		if c == nil {
			continue
		}
		copy(p.centroidsFlat[i*p.dim:], c)
		var normSq float64
		for _, v := range c {
			normSq += float64(v) * float64(v)
		}
		p.centroidNormsSq[i] = normSq
	}
}

func (p *Partitioner) findNearest(vec []float32) uint32 {
	var vecNormSq float64
	for _, v := range vec {
		vecNormSq += float64(v) * float64(v)
	}
	return p.findNearestWithNorm(vec, vecNormSq)
}

func (p *Partitioner) findNearestWithNorm(vec []float32, vecNormSq float64) uint32 {
	if p.centroidsFlat == nil {
		return p.findNearestScalar(vec)
	}

	var bestIdx uint32
	bestDist := math.MaxFloat64
	dim := p.dim
	for i := range p.numParts {
		centroid := p.centroidsFlat[i*dim : (i+1)*dim]
		dot := vek32.Dot(vec, centroid)
		d := vecNormSq + p.centroidNormsSq[i] - 2*float64(dot)
		if d < bestDist {
			bestDist = d
			bestIdx = uint32(i)
		}
	}
	return bestIdx
}

func (p *Partitioner) findNearestScalar(vec []float32) uint32 {
	var bestIdx uint32
	bestDist := math.MaxFloat64

	for i, centroid := range p.centroids {
		if centroid == nil {
			continue
		}
		d := squaredL2(vec, centroid)
		if d < bestDist {
			bestDist = d
			bestIdx = uint32(i)
		}
	}
	return bestIdx
}

func (p *Partitioner) updateCentroids(vectors [][]float32) {
	n := len(vectors)
	numWorkers := runtime.GOMAXPROCS(0)
	chunkSize := (n + numWorkers - 1) / numWorkers

	type localAccum struct {
		counts []int
		sums   [][]float64
	}

	accums := make([]localAccum, numWorkers)
	var wg sync.WaitGroup

	for w := range numWorkers {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= end {
			continue
		}

		accums[w] = localAccum{
			counts: make([]int, p.numParts),
			sums:   make([][]float64, p.numParts),
		}
		for i := range p.numParts {
			accums[w].sums[i] = make([]float64, p.dim)
		}

		wg.Add(1)
		go func(w, start, end int) {
			defer wg.Done()
			local := &accums[w]
			for i := start; i < end; i++ {
				part := p.assignments[i]
				local.counts[part]++
				vec := vectors[i]
				sum := local.sums[part]
				for j, v := range vec {
					sum[j] += float64(v)
				}
			}
		}(w, start, end)
	}

	wg.Wait()

	counts := make([]int, p.numParts)
	sums := make([][]float64, p.numParts)
	for i := range p.numParts {
		sums[i] = make([]float64, p.dim)
	}

	for w := range numWorkers {
		if accums[w].counts == nil {
			continue
		}
		for i := range p.numParts {
			counts[i] += accums[w].counts[i]
			for j := range p.dim {
				sums[i][j] += accums[w].sums[i][j]
			}
		}
	}

	for i := range p.numParts {
		if counts[i] == 0 {
			continue
		}
		if p.centroids[i] == nil {
			p.centroids[i] = make([]float32, p.dim)
		}
		invCount := 1.0 / float64(counts[i])
		for j := range p.dim {
			p.centroids[i][j] = float32(sums[i][j] * invCount)
		}
	}
}

func (p *Partitioner) Assign(vec []float32) uint32 {
	return p.findNearest(vec)
}

func (p *Partitioner) AssignTopK(vec []float32, k int) []uint32 {
	type scored struct {
		idx  uint32
		dist float64
	}

	scores := make([]scored, 0, p.numParts)
	for i, centroid := range p.centroids {
		if centroid == nil {
			continue
		}
		scores = append(scores, scored{uint32(i), squaredL2(vec, centroid)})
	}

	for i := 0; i < k && i < len(scores); i++ {
		minIdx := i
		for j := i + 1; j < len(scores); j++ {
			if scores[j].dist < scores[minIdx].dist {
				minIdx = j
			}
		}
		scores[i], scores[minIdx] = scores[minIdx], scores[i]
	}

	result := make([]uint32, min(k, len(scores)))
	for i := range result {
		result[i] = scores[i].idx
	}
	return result
}

func (p *Partitioner) Centroids() [][]float32 {
	return p.centroids
}

func (p *Partitioner) Assignments() []uint32 {
	return p.assignments
}

func (p *Partitioner) NumPartitions() int {
	return p.numParts
}

func squaredL2(a, b []float32) float64 {
	aNorm := float64(vek32.Dot(a, a))
	bNorm := float64(vek32.Dot(b, b))
	aDotB := float64(vek32.Dot(a, b))
	d := aNorm + bNorm - 2*aDotB
	if d < 0 {
		return 0
	}
	return d
}

func copyVector(v []float32) []float32 {
	c := make([]float32, len(v))
	copy(c, v)
	return c
}
