package scann

import (
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
)

type Partitioner struct {
	centroids   [][]float32
	dim         int
	numParts    int
	assignments []uint32
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

	p.initCentroidsKMeansPP(vectors, k)
	p.assignments = make([]uint32, n)

	for iter := range maxIters {
		changed := p.assignToCentroids(vectors)
		p.updateCentroids(vectors)

		if iter > 0 && changed < n/1000 {
			break
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
				nearest := p.findNearest(vectors[i])
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

func (p *Partitioner) findNearest(vec []float32) uint32 {
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
	counts := make([]int, p.numParts)
	sums := make([][]float64, p.numParts)
	for i := range p.numParts {
		sums[i] = make([]float64, p.dim)
	}

	for i, vec := range vectors {
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
	var sum float64
	for i := range a {
		d := float64(a[i] - b[i])
		sum += d * d
	}
	return sum
}

func copyVector(v []float32) []float32 {
	c := make([]float32, len(v))
	copy(c, v)
	return c
}
