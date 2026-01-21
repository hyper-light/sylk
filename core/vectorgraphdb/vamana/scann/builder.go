package scann

import (
	"math/rand/v2"
	"runtime"
	"sync"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type BatchBuilder struct {
	config      BatchBuildConfig
	avqConfig   AVQConfig
	vamanaConf  vamana.VamanaConfig
	partitioner *Partitioner
	codebooks   *PartitionCodebooks
}

func NewBatchBuilder(batchConf BatchBuildConfig, avqConf AVQConfig, vamanaConf vamana.VamanaConfig) *BatchBuilder {
	if batchConf.NumWorkers == 0 {
		batchConf.NumWorkers = runtime.GOMAXPROCS(0)
	}
	return &BatchBuilder{
		config:     batchConf,
		avqConfig:  avqConf,
		vamanaConf: vamanaConf,
	}
}

type BuildResult struct {
	Partitioner *Partitioner
	Codebooks   *PartitionCodebooks
	Medoid      uint32
}

// BuildIncremental adds vectors one at a time using VamanaInsert.
// Use this for streaming/interactive scenarios where vectors arrive incrementally.
// Faster for small updates but slower than batch Build for full index construction.
func (b *BatchBuilder) BuildIncremental(
	vectors [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	medoid uint32,
) error {
	for i, vec := range vectors {
		nodeID := uint32(i)
		if err := vamana.VamanaInsert(nodeID, vec, medoid, b.vamanaConf, vectorStore, graphStore, magCache); err != nil {
			return err
		}
	}
	return nil
}

func (b *BatchBuilder) Build(
	vectors [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
) (*BuildResult, error) {
	n := len(vectors)
	if n == 0 {
		return &BuildResult{}, nil
	}

	dim := len(vectors[0])
	numParts := b.avqConfig.NumPartitions
	if numParts > n {
		numParts = n
	}

	b.partitioner = NewPartitioner(numParts, dim)
	b.partitioner.Train(vectors, 20)

	b.codebooks = NewPartitionCodebooks(b.partitioner.Centroids(), b.avqConfig.AnisotropicWeight)
	b.trainCodebooks(vectors)

	b.precomputeMagnitudes(vectors, magCache)
	b.buildGraphLocalityAware(n, graphStore)
	b.refineGraph(vectors, graphStore, magCache)

	medoid := vamana.ComputeCentroidMedoid(vectorStore, magCache, nil)

	return &BuildResult{
		Partitioner: b.partitioner,
		Codebooks:   b.codebooks,
		Medoid:      medoid,
	}, nil
}

func (b *BatchBuilder) trainCodebooks(vectors [][]float32) {
	assignments := b.partitioner.Assignments()
	partitionVecs := make([][][]float32, b.partitioner.NumPartitions())

	for i, vec := range vectors {
		part := assignments[i]
		partitionVecs[part] = append(partitionVecs[part], vec)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, b.config.NumWorkers)

	for partID, vecs := range partitionVecs {
		if len(vecs) == 0 {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(pid int, pvecs [][]float32) {
			defer wg.Done()
			defer func() { <-sem }()
			b.codebooks.TrainPartition(pid, pvecs)
		}(partID, vecs)
	}

	wg.Wait()
}

func (b *BatchBuilder) precomputeMagnitudes(vectors [][]float32, magCache *vamana.MagnitudeCache) {
	n := len(vectors)
	numWorkers := b.config.NumWorkers
	if numWorkers <= 0 {
		numWorkers = 1
	}

	chunkSize := (n + numWorkers - 1) / numWorkers
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
		go func(s, e int) {
			defer wg.Done()
			for i := s; i < e; i++ {
				magCache.GetOrCompute(uint32(i), vectors[i])
			}
		}(start, end)
	}

	wg.Wait()
}

func (b *BatchBuilder) buildGraphRandom(n int, graphStore *storage.GraphStore) {
	R := b.vamanaConf.R

	for i := range n {
		neighbors := make([]uint32, 0, R)
		seen := make(map[uint32]struct{})
		seen[uint32(i)] = struct{}{}

		for len(neighbors) < R && len(neighbors) < n-1 {
			neighbor := uint32(rand.IntN(n))
			if _, exists := seen[neighbor]; !exists {
				seen[neighbor] = struct{}{}
				neighbors = append(neighbors, neighbor)
			}
		}

		graphStore.SetNeighbors(uint32(i), neighbors)
	}
}

func (b *BatchBuilder) buildGraphLocalityAware(n int, graphStore *storage.GraphStore) {
	R := b.vamanaConf.R
	assignments := b.partitioner.Assignments()
	numParts := b.partitioner.NumPartitions()

	partitionMembers := make([][]uint32, numParts)
	for i := range n {
		part := assignments[i]
		partitionMembers[part] = append(partitionMembers[part], uint32(i))
	}

	for i := range n {
		myPart := assignments[i]
		members := partitionMembers[myPart]

		neighbors := make([]uint32, 0, R)
		seen := make(map[uint32]struct{})
		seen[uint32(i)] = struct{}{}

		localTarget := (R * 3) / 4
		if localTarget > len(members)-1 {
			localTarget = len(members) - 1
		}

		perm := rand.Perm(len(members))
		for _, idx := range perm {
			if len(neighbors) >= localTarget {
				break
			}
			candidate := members[idx]
			if _, exists := seen[candidate]; !exists {
				seen[candidate] = struct{}{}
				neighbors = append(neighbors, candidate)
			}
		}

		for len(neighbors) < R && len(neighbors) < n-1 {
			neighbor := uint32(rand.IntN(n))
			if _, exists := seen[neighbor]; !exists {
				seen[neighbor] = struct{}{}
				neighbors = append(neighbors, neighbor)
			}
		}

		graphStore.SetNeighbors(uint32(i), neighbors)
	}
}

func (b *BatchBuilder) buildGraphVamana(
	vectors [][]float32,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	medoid uint32,
) {
	n := len(vectors)
	R := b.vamanaConf.R
	L := b.vamanaConf.L
	alpha := b.vamanaConf.Alpha

	distFn := func(a, b uint32) float64 {
		vecA := vectors[a]
		vecB := vectors[b]
		magA := magCache.GetOrCompute(a, vecA)
		magB := magCache.GetOrCompute(b, vecB)
		if magA == 0 || magB == 0 {
			return 2.0
		}
		dot := vamana.DotProduct(vecA, vecB)
		return 1.0 - float64(dot)/(magA*magB)
	}

	perm := rand.Perm(n)
	for i, idx := range perm {
		nodeID := uint32(idx)

		candidates := b.greedySearchForBuild(nodeID, vectors, graphStore, magCache, medoid, L)

		newNeighbors := vamana.RobustPrune(nodeID, candidates, alpha, R, distFn)
		graphStore.SetNeighbors(nodeID, newNeighbors)

		for _, neighborID := range newNeighbors {
			existingNeighbors := graphStore.GetNeighbors(neighborID)
			if len(existingNeighbors) >= R {
				candidateList := append(existingNeighbors, nodeID)
				prunedNeighbors := vamana.RobustPrune(neighborID, candidateList, alpha, R, distFn)
				graphStore.SetNeighbors(neighborID, prunedNeighbors)
			} else {
				graphStore.SetNeighbors(neighborID, append(existingNeighbors, nodeID))
			}
		}

		if b.config.ProgressCallback != nil && (i+1)%1000 == 0 {
			b.config.ProgressCallback(i+1, n)
		}
	}
}

func (b *BatchBuilder) greedySearchForBuild(
	queryID uint32,
	vectors [][]float32,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	start uint32,
	L int,
) []uint32 {
	queryVec := vectors[queryID]
	queryMag := magCache.GetOrCompute(queryID, queryVec)
	if queryMag == 0 {
		return nil
	}

	distFn := func(id uint32) float64 {
		if id == queryID {
			return 0
		}
		vec := vectors[id]
		mag := magCache.GetOrCompute(id, vec)
		if mag == 0 {
			return 2.0
		}
		dot := vamana.DotProduct(queryVec, vec)
		return 1.0 - float64(dot)/(queryMag*mag)
	}

	type candidate struct {
		id   uint32
		dist float64
	}

	visited := make(map[uint32]struct{}, L*2)
	candidates := []candidate{{id: start, dist: distFn(start)}}
	results := make([]candidate, 0, L)

	for len(candidates) > 0 {
		minIdx := 0
		for i := 1; i < len(candidates); i++ {
			if candidates[i].dist < candidates[minIdx].dist {
				minIdx = i
			}
		}
		current := candidates[minIdx]
		candidates[minIdx] = candidates[len(candidates)-1]
		candidates = candidates[:len(candidates)-1]

		if _, seen := visited[current.id]; seen {
			continue
		}
		visited[current.id] = struct{}{}

		if current.id != queryID {
			results = append(results, current)
			if len(results) > L {
				maxIdx := 0
				for i := 1; i < len(results); i++ {
					if results[i].dist > results[maxIdx].dist {
						maxIdx = i
					}
				}
				results[maxIdx] = results[len(results)-1]
				results = results[:len(results)-1]
			}
		}

		var bound float64 = 2.0
		if len(results) >= L {
			for _, r := range results {
				if r.dist > bound || bound == 2.0 {
					bound = r.dist
				}
			}
			bound = 0
			for _, r := range results {
				if r.dist > bound {
					bound = r.dist
				}
			}
		}

		neighbors := graphStore.GetNeighbors(current.id)
		for _, neighborID := range neighbors {
			if _, seen := visited[neighborID]; seen {
				continue
			}
			if neighborID == queryID {
				continue
			}
			dist := distFn(neighborID)
			if len(results) < L || dist < bound {
				candidates = append(candidates, candidate{id: neighborID, dist: dist})
			}
		}
	}

	resultIDs := make([]uint32, len(results))
	for i, r := range results {
		resultIDs[i] = r.id
	}
	return resultIDs
}

func (b *BatchBuilder) refineGraph(
	vectors [][]float32,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
) {
	n := len(vectors)
	R := b.vamanaConf.R
	alpha := b.vamanaConf.Alpha
	numWorkers := b.config.NumWorkers

	numPasses := computeRefinementPasses(n)
	for pass := range numPasses {
		order := rand.Perm(n)
		if pass%2 == 1 {
			for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
				order[i], order[j] = order[j], order[i]
			}
		}

		b.refinePassParallel(order, vectors, graphStore, magCache, R, alpha, numWorkers)

		if b.config.ProgressCallback != nil {
			b.config.ProgressCallback(pass+1, numPasses)
		}
	}
}

func computeRefinementPasses(n int) int {
	if n <= 1000 {
		return 2
	}
	if n <= 10000 {
		return 3
	}
	if n <= 100000 {
		return 4
	}
	return 5
}

type nodeUpdate struct {
	nodeID       uint32
	newNeighbors []uint32
}

func (b *BatchBuilder) refinePassParallel(
	order []int,
	vectors [][]float32,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	R int,
	alpha float64,
	numWorkers int,
) {
	n := len(order)
	if numWorkers <= 0 {
		numWorkers = 1
	}
	if numWorkers > n {
		numWorkers = n
	}

	chunkSize := (n + numWorkers - 1) / numWorkers

	updatesCh := make(chan []nodeUpdate, numWorkers)
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
		go func(chunk []int) {
			defer wg.Done()
			updates := b.computeRefinements(chunk, vectors, graphStore, magCache, R, alpha)
			updatesCh <- updates
		}(order[start:end])
	}

	go func() {
		wg.Wait()
		close(updatesCh)
	}()

	for updates := range updatesCh {
		for _, u := range updates {
			graphStore.SetNeighbors(u.nodeID, u.newNeighbors)

			for _, neighborID := range u.newNeighbors {
				existingNeighbors := graphStore.GetNeighbors(neighborID)
				hasEdge := false
				for _, en := range existingNeighbors {
					if en == u.nodeID {
						hasEdge = true
						break
					}
				}

				if !hasEdge && len(existingNeighbors) < R {
					updated := append(existingNeighbors, u.nodeID)
					graphStore.SetNeighbors(neighborID, updated)
				}
			}
		}
	}
}

func (b *BatchBuilder) computeRefinements(
	nodeIndices []int,
	vectors [][]float32,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	R int,
	alpha float64,
) []nodeUpdate {
	distFn := func(a, b uint32) float64 {
		vecA := vectors[a]
		vecB := vectors[b]
		magA := magCache.GetOrCompute(a, vecA)
		magB := magCache.GetOrCompute(b, vecB)
		if magA == 0 || magB == 0 {
			return 2.0
		}
		dot := vamana.DotProduct(vecA, vecB)
		return 1.0 - float64(dot)/(magA*magB)
	}

	updates := make([]nodeUpdate, 0, len(nodeIndices))

	for _, idx := range nodeIndices {
		nodeID := uint32(idx)

		currentNeighbors := graphStore.GetNeighbors(nodeID)
		candidates := make(map[uint32]struct{}, R*3)

		for _, neighbor := range currentNeighbors {
			candidates[neighbor] = struct{}{}
			secondHop := graphStore.GetNeighbors(neighbor)
			for _, nn := range secondHop {
				if nn != nodeID {
					candidates[nn] = struct{}{}
				}
			}
		}

		if len(candidates) <= len(currentNeighbors) {
			continue
		}

		candidateList := make([]uint32, 0, len(candidates))
		for c := range candidates {
			candidateList = append(candidateList, c)
		}

		newNeighbors := vamana.RobustPrune(nodeID, candidateList, alpha, R, distFn)
		updates = append(updates, nodeUpdate{
			nodeID:       nodeID,
			newNeighbors: newNeighbors,
		})
	}

	return updates
}
