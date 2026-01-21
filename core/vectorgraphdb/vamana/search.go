package vamana

import (
	"container/heap"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type searchCandidate struct {
	id   uint32
	dist float64
}

// candidateHeap is a min-heap ordered by distance (closest first).
type candidateHeap []searchCandidate

func (h candidateHeap) Len() int           { return len(h) }
func (h candidateHeap) Less(i, j int) bool { return h[i].dist < h[j].dist }
func (h candidateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *candidateHeap) Push(x any) {
	*h = append(*h, x.(searchCandidate))
}

func (h *candidateHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// resultHeap is a max-heap ordered by distance (furthest first for eviction).
type resultHeap []searchCandidate

func (h resultHeap) Len() int           { return len(h) }
func (h resultHeap) Less(i, j int) bool { return h[i].dist > h[j].dist }
func (h resultHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *resultHeap) Push(x any) {
	*h = append(*h, x.(searchCandidate))
}

func (h *resultHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// GreedySearch performs Vamana beam search to find K nearest neighbors.
//
// Algorithm (DiskANN paper):
//  1. Initialize candidates min-heap with start node
//  2. While candidates not empty:
//     - Pop closest unvisited candidate
//     - Add to results max-heap (evict furthest if > L)
//     - For each neighbor: add to candidates if within beam bound
//  3. Return top K results sorted by similarity
//
// Parameters:
//   - L: beam width (search list size), larger = better recall, higher latency
//   - K: results to return, must be <= L
//
// Complexity: O(L * R * log(L)) where R is average degree.
func GreedySearch(
	query []float32,
	start uint32,
	L, K int,
	vectors *storage.VectorStore,
	graph *storage.GraphStore,
	magCache *MagnitudeCache,
) []SearchResult {
	if vectors == nil || graph == nil || L <= 0 || K <= 0 {
		return nil
	}

	if K > L {
		K = L
	}

	queryMag := Magnitude(query)
	if queryMag == 0 {
		return nil
	}

	distFn := func(id uint32) float64 {
		vec := vectors.Get(id)
		if vec == nil {
			return 2.0
		}
		vecMag := magCache.GetOrCompute(id, vec)
		if vecMag == 0 {
			return 2.0
		}
		dot := DotProduct(query, vec)
		similarity := float64(dot) / (queryMag * vecMag)
		return 1.0 - similarity
	}

	visited := make(map[uint32]struct{}, L*2)

	candidates := &candidateHeap{}
	heap.Init(candidates)

	startVec := vectors.Get(start)
	if startVec == nil {
		return nil
	}
	startDist := distFn(start)
	heap.Push(candidates, searchCandidate{id: start, dist: startDist})

	results := &resultHeap{}
	heap.Init(results)

	for candidates.Len() > 0 {
		current := heap.Pop(candidates).(searchCandidate)

		if _, seen := visited[current.id]; seen {
			continue
		}
		visited[current.id] = struct{}{}

		heap.Push(results, current)

		if results.Len() > L {
			heap.Pop(results)
		}

		var bound float64
		if results.Len() > 0 {
			bound = (*results)[0].dist
		} else {
			bound = 2.0
		}

		neighbors := graph.GetNeighbors(current.id)
		for _, neighborID := range neighbors {
			if _, seen := visited[neighborID]; seen {
				continue
			}

			dist := distFn(neighborID)

			if results.Len() < L || dist < bound {
				heap.Push(candidates, searchCandidate{id: neighborID, dist: dist})
			}
		}
	}

	n := results.Len()
	if n > K {
		n = K
	}

	all := make([]searchCandidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		all[i] = heap.Pop(results).(searchCandidate)
	}

	output := make([]SearchResult, n)
	for i := 0; i < n; i++ {
		c := all[i]
		output[i] = SearchResult{
			InternalID: InternalID(c.id),
			Similarity: 1.0 - c.dist,
		}
	}

	return output
}

// GreedySearchFast performs lock-free beam search using pre-captured snapshots.
// Use this for high-throughput search when no concurrent writes are happening.
func GreedySearchFast(
	query []float32,
	start uint32,
	L, K int,
	vectors *storage.VectorSnapshot,
	graph *storage.GraphStore,
	magCache *MagnitudeCache,
) []SearchResult {
	if vectors == nil || graph == nil || L <= 0 || K <= 0 {
		return nil
	}

	if K > L {
		K = L
	}

	queryMag := Magnitude(query)
	if queryMag == 0 {
		return nil
	}

	n := vectors.Len()
	if n == 0 {
		return nil
	}

	distFn := func(id uint32) float64 {
		vec := vectors.Get(id)
		if vec == nil {
			return 2.0
		}
		vecMag := magCache.GetOrCompute(id, vec)
		if vecMag == 0 {
			return 2.0
		}
		dot := DotProduct(query, vec)
		similarity := float64(dot) / (queryMag * vecMag)
		return 1.0 - similarity
	}

	visited := make([]bool, n)

	candidates := &candidateHeap{}
	heap.Init(candidates)

	startVec := vectors.Get(start)
	if startVec == nil {
		return nil
	}
	startDist := distFn(start)
	heap.Push(candidates, searchCandidate{id: start, dist: startDist})

	results := &resultHeap{}
	heap.Init(results)

	for candidates.Len() > 0 {
		current := heap.Pop(candidates).(searchCandidate)

		if visited[current.id] {
			continue
		}
		visited[current.id] = true

		heap.Push(results, current)

		if results.Len() > L {
			heap.Pop(results)
		}

		var bound float64
		if results.Len() > 0 {
			bound = (*results)[0].dist
		} else {
			bound = 2.0
		}

		neighbors := graph.GetNeighbors(current.id)
		for _, neighborID := range neighbors {
			if visited[neighborID] {
				continue
			}

			dist := distFn(neighborID)

			if results.Len() < L || dist < bound {
				heap.Push(candidates, searchCandidate{id: neighborID, dist: dist})
			}
		}
	}

	resultCount := results.Len()
	if resultCount > K {
		resultCount = K
	}

	all := make([]searchCandidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		all[i] = heap.Pop(results).(searchCandidate)
	}

	output := make([]SearchResult, resultCount)
	for i := 0; i < resultCount; i++ {
		c := all[i]
		output[i] = SearchResult{
			InternalID: InternalID(c.id),
			Similarity: 1.0 - c.dist,
		}
	}

	return output
}

// GreedySearchWithFilter performs beam search with post-filtering by domain/nodeType.
// Retrieves L*overFetch candidates before filtering to find K matching results.
func GreedySearchWithFilter(
	query []float32,
	start uint32,
	L, K, overFetch int,
	vectors *storage.VectorStore,
	graph *storage.GraphStore,
	labels *storage.LabelStore,
	magCache *MagnitudeCache,
	filter *SearchFilter,
) []SearchResult {
	if overFetch < 1 {
		overFetch = 1
	}

	fetchK := K * overFetch
	if fetchK < L {
		fetchK = L
	}

	candidates := GreedySearch(query, start, fetchK, fetchK, vectors, graph, magCache)

	if filter == nil || labels == nil {
		if len(candidates) > K {
			return candidates[:K]
		}
		return candidates
	}

	var domainFilter []uint8
	var typeFilter []uint16

	for _, d := range filter.Domains {
		domainFilter = append(domainFilter, uint8(d))
	}
	for _, t := range filter.NodeTypes {
		typeFilter = append(typeFilter, uint16(t))
	}

	output := make([]SearchResult, 0, K)
	for _, c := range candidates {
		if len(output) >= K {
			break
		}

		if filter.MinSimilarity > 0 && c.Similarity < filter.MinSimilarity {
			continue
		}

		domain, nodeType := labels.Get(uint32(c.InternalID))
		if !storage.MatchesFilter(domain, nodeType, domainFilter, typeFilter) {
			continue
		}

		c.Domain = vectorgraphdb.Domain(domain)
		c.NodeType = vectorgraphdb.NodeType(nodeType)
		output = append(output, c)
	}

	return output
}
