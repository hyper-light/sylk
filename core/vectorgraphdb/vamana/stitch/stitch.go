package stitch

import (
	"container/heap"
	"runtime"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

// StitchConfig configures the graph stitching operation.
type StitchConfig struct {
	// Boundary sampling configuration.
	Boundary BoundaryConfig

	// R is the maximum degree (neighbors per node).
	// Used for RobustPrune when adding cross-edges.
	R int

	// Alpha is the RNG pruning parameter.
	Alpha float64

	// L is the search beam width for finding cross-graph neighbors.
	L int

	// CrossEdgesPerNode is how many cross-graph edges to add per boundary node.
	// Defaults to R/2 if 0.
	CrossEdgesPerNode int
}

// DefaultStitchConfig returns sensible defaults for stitching.
func DefaultStitchConfig() StitchConfig {
	vCfg := vamana.DefaultConfig()
	return StitchConfig{
		Boundary:          DefaultBoundaryConfig(),
		R:                 vCfg.R,
		Alpha:             vCfg.Alpha,
		L:                 vCfg.L,
		CrossEdgesPerNode: 0,
	}
}

// StitchResult contains statistics from a stitch operation.
type StitchResult struct {
	MainBoundaryNodes    int
	SegmentBoundaryNodes int
	CrossEdgesAdded      int
	SearchesPerformed    int
	PruneOperations      int
}

// Stitcher connects two independently-built Vamana graphs.
type Stitcher struct {
	config   StitchConfig
	magCache *vamana.MagnitudeCache
}

// NewStitcher creates a stitcher with the given configuration.
func NewStitcher(config StitchConfig, magCache *vamana.MagnitudeCache) *Stitcher {
	if config.CrossEdgesPerNode <= 0 {
		config.CrossEdgesPerNode = config.R / 2
		if config.CrossEdgesPerNode < 1 {
			config.CrossEdgesPerNode = 1
		}
	}
	return &Stitcher{
		config:   config,
		magCache: magCache,
	}
}

// Stitch connects a segment graph to the main graph.
// After stitching, searches on main can discover nodes in segment and vice versa.
//
// The segment's nodes must have IDs starting at mainGraph.Count().
// Both graphs must use the same VectorStore (segment vectors appended after main).
func (s *Stitcher) Stitch(
	mainGraph *storage.GraphStore,
	segmentGraph *storage.GraphStore,
	vectors *storage.VectorStore,
	mainMedoid uint32,
	segmentMedoid uint32,
) (*StitchResult, error) {
	result := &StitchResult{}

	mainCount := int(mainGraph.Count())
	segmentCount := int(segmentGraph.Count())

	if mainCount == 0 || segmentCount == 0 {
		return result, nil
	}

	segmentOffset := uint32(mainCount)

	mainBoundary := SampleBoundary(mainGraph, s.config.Boundary)
	segmentBoundary := SampleBoundary(segmentGraph, s.config.Boundary)

	result.MainBoundaryNodes = len(mainBoundary)
	result.SegmentBoundaryNodes = len(segmentBoundary)

	numWorkers := runtime.NumCPU()
	snapshot := vectors.Snapshot()

	type crossEdge struct {
		mainID      uint32
		segLocalID  uint32
		segGlobalID uint32
	}

	phase1Results := make(chan []crossEdge, numWorkers)
	segChan := make(chan uint32, len(segmentBoundary))
	for _, id := range segmentBoundary {
		segChan <- id
	}
	close(segChan)

	for range numWorkers {
		go func() {
			var localEdges []crossEdge
			for segLocalID := range segChan {
				segGlobalID := segLocalID + segmentOffset
				segVec := snapshot.Get(segGlobalID)
				if segVec == nil {
					continue
				}

				neighbors := vamana.GreedySearchFast(
					segVec,
					mainMedoid,
					s.config.L,
					s.config.CrossEdgesPerNode,
					snapshot,
					mainGraph,
					s.magCache,
				)

				for _, neighbor := range neighbors {
					localEdges = append(localEdges, crossEdge{
						mainID:      uint32(neighbor.InternalID),
						segLocalID:  segLocalID,
						segGlobalID: segGlobalID,
					})
				}
			}
			phase1Results <- localEdges
		}()
	}

	var phase1Edges []crossEdge
	for range numWorkers {
		phase1Edges = append(phase1Edges, <-phase1Results...)
	}

	phase2Results := make(chan []crossEdge, numWorkers)
	mainChan := make(chan uint32, len(mainBoundary))
	for _, id := range mainBoundary {
		mainChan <- id
	}
	close(mainChan)

	for range numWorkers {
		go func() {
			var localEdges []crossEdge
			for mainNodeID := range mainChan {
				mainVec := snapshot.Get(mainNodeID)
				if mainVec == nil {
					continue
				}

				neighbors := s.searchSegmentFast(
					mainVec,
					segmentMedoid,
					segmentGraph,
					snapshot,
					segmentOffset,
					segmentCount,
				)

				for _, neighbor := range neighbors {
					segLocalID := uint32(neighbor.InternalID)
					localEdges = append(localEdges, crossEdge{
						mainID:      mainNodeID,
						segLocalID:  segLocalID,
						segGlobalID: segLocalID + segmentOffset,
					})
				}
			}
			phase2Results <- localEdges
		}()
	}

	var phase2Edges []crossEdge
	for range numWorkers {
		phase2Edges = append(phase2Edges, <-phase2Results...)
	}

	mergeSegmentIntoMain(mainGraph, segmentGraph, segmentOffset, segmentCount)

	for _, e := range phase1Edges {
		mainGraph.AddNeighbor(e.mainID, e.segGlobalID)
		mainGraph.AddNeighbor(e.segGlobalID, e.mainID)
	}

	for _, e := range phase2Edges {
		mainGraph.AddNeighbor(e.mainID, e.segGlobalID)
		mainGraph.AddNeighbor(e.segGlobalID, e.mainID)
	}

	result.SearchesPerformed = len(segmentBoundary) + len(mainBoundary)
	result.CrossEdgesAdded = (len(phase1Edges) + len(phase2Edges)) * 2

	return result, nil
}

func mergeSegmentIntoMain(mainGraph, segmentGraph *storage.GraphStore, segmentOffset uint32, segmentCount int) {
	for localID := range segmentCount {
		globalID := uint32(localID) + segmentOffset
		localNeighbors := segmentGraph.GetNeighbors(uint32(localID))

		globalNeighbors := make([]uint32, len(localNeighbors))
		for i, n := range localNeighbors {
			globalNeighbors[i] = n + segmentOffset
		}

		mainGraph.SetNeighbors(globalID, globalNeighbors)
	}
}

func (s *Stitcher) addEdgeNoPrune(graph *storage.GraphStore, fromID, toID uint32) {
	graph.AddNeighbor(fromID, toID)
}

// searchSegmentFast searches segment graph using lock-free snapshot and []bool visited.
func (s *Stitcher) searchSegmentFast(
	query []float32,
	medoid uint32,
	segmentGraph *storage.GraphStore,
	snapshot *storage.VectorSnapshot,
	segmentOffset uint32,
	segmentCount int,
) []vamana.SearchResult {
	L := s.config.L
	K := s.config.CrossEdgesPerNode

	if K > L {
		K = L
	}

	queryMag := vamana.Magnitude(query)
	if queryMag == 0 {
		return nil
	}

	distFn := func(localID uint32) float64 {
		globalID := localID + segmentOffset
		vec := snapshot.Get(globalID)
		if vec == nil {
			return 2.0
		}
		vecMag := s.magCache.GetOrCompute(globalID, vec)
		if vecMag == 0 {
			return 2.0
		}
		dot := vamana.DotProduct(query, vec)
		return 1.0 - float64(dot)/(queryMag*vecMag)
	}

	visited := make([]bool, segmentCount)
	candidates := make(minHeap, 0, L)
	results := make(maxHeap, 0, L)

	heap.Push(&candidates, searchCandidate{id: medoid, dist: distFn(medoid)})

	itersSinceImprovement := 0
	var prevBound float64 = 2.0

	for candidates.Len() > 0 {
		current := heap.Pop(&candidates).(searchCandidate)

		if visited[current.id] {
			continue
		}
		visited[current.id] = true

		heap.Push(&results, current)
		if results.Len() > L {
			heap.Pop(&results)
		}

		bound := 2.0
		if results.Len() > 0 {
			bound = results[0].dist
		}

		if bound < prevBound {
			itersSinceImprovement = 0
			prevBound = bound
		} else {
			itersSinceImprovement++
			if results.Len() >= L && itersSinceImprovement >= L {
				break
			}
		}

		neighbors := segmentGraph.GetNeighbors(current.id)
		for _, nid := range neighbors {
			if visited[nid] {
				continue
			}
			dist := distFn(nid)
			if results.Len() < L || dist < bound {
				heap.Push(&candidates, searchCandidate{id: nid, dist: dist})
			}
		}
	}

	n := min(results.Len(), K)
	sorted := make([]vamana.SearchResult, n)
	for i := n - 1; i >= 0; i-- {
		c := heap.Pop(&results).(searchCandidate)
		sorted[i] = vamana.SearchResult{
			InternalID: vamana.InternalID(c.id),
			Similarity: 1.0 - c.dist,
		}
	}
	return sorted
}

func (s *Stitcher) addEdgeWithPrune(
	graph *storage.GraphStore,
	fromID, toID uint32,
	vectors *storage.VectorStore,
	result *StitchResult,
) {
	if !graph.AddNeighbor(fromID, toID) {
		return
	}

	neighbors := graph.GetNeighbors(fromID)
	if len(neighbors) <= s.config.R {
		return
	}

	fromVec := vectors.Get(fromID)
	if fromVec == nil {
		return
	}

	fromMag := s.magCache.GetOrCompute(fromID, fromVec)

	pruned := vamana.RobustPruneBatch(
		fromID,
		fromVec,
		fromMag,
		neighbors,
		s.config.Alpha,
		s.config.R,
		func(id uint32) []float32 { return vectors.Get(id) },
		func(id uint32) float64 {
			vec := vectors.Get(id)
			if vec == nil {
				return 0
			}
			return s.magCache.GetOrCompute(id, vec)
		},
	)

	graph.SetNeighbors(fromID, pruned)
	result.PruneOperations++
}

type searchCandidate struct {
	id   uint32
	dist float64
}

type minHeap []searchCandidate

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].dist < h[j].dist }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(searchCandidate)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type maxHeap []searchCandidate

func (h maxHeap) Len() int           { return len(h) }
func (h maxHeap) Less(i, j int) bool { return h[i].dist > h[j].dist }
func (h maxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)        { *h = append(*h, x.(searchCandidate)) }
func (h *maxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
