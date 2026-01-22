package stitch

import (
	"container/heap"

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

	// Connect segment boundary nodes to main graph
	for _, segLocalID := range segmentBoundary {
		segGlobalID := segLocalID + segmentOffset
		segVec := vectors.Get(segGlobalID)
		if segVec == nil {
			continue
		}

		// Search main graph for nearest neighbors
		neighbors := vamana.GreedySearch(
			segVec,
			mainMedoid,
			s.config.L,
			s.config.CrossEdgesPerNode,
			vectors,
			mainGraph,
			s.magCache,
		)
		result.SearchesPerformed++

		// Add bidirectional cross-edges
		for _, neighbor := range neighbors {
			mainNodeID := uint32(neighbor.InternalID)

			// Add edge: segment node → main node
			s.addEdgeWithPrune(segmentGraph, segLocalID, mainNodeID, vectors, result)

			// Add edge: main node → segment node (global ID)
			s.addEdgeWithPrune(mainGraph, mainNodeID, segGlobalID, vectors, result)

			result.CrossEdgesAdded += 2
		}
	}

	// Connect main boundary nodes to segment graph
	for _, mainNodeID := range mainBoundary {
		mainVec := vectors.Get(mainNodeID)
		if mainVec == nil {
			continue
		}

		// Search segment graph for nearest neighbors
		// Need to adjust: segment graph uses local IDs (0-based)
		// but vectors are at global positions (mainCount + localID)
		neighbors := s.searchSegment(
			mainVec,
			segmentMedoid,
			segmentGraph,
			vectors,
			segmentOffset,
		)
		result.SearchesPerformed++

		for _, neighbor := range neighbors {
			segLocalID := uint32(neighbor.InternalID)
			segGlobalID := segLocalID + segmentOffset

			// Add edge: main node → segment node (global ID)
			s.addEdgeWithPrune(mainGraph, mainNodeID, segGlobalID, vectors, result)

			// Add edge: segment node → main node
			s.addEdgeWithPrune(segmentGraph, segLocalID, mainNodeID, vectors, result)

			result.CrossEdgesAdded += 2
		}
	}

	return result, nil
}

// searchSegment searches segment graph, translating local IDs to global vector positions.
func (s *Stitcher) searchSegment(
	query []float32,
	medoid uint32,
	segmentGraph *storage.GraphStore,
	vectors *storage.VectorStore,
	segmentOffset uint32,
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
		vec := vectors.Get(globalID)
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

	visited := make(map[uint32]struct{}, L*2)
	candidates := make(minHeap, 0, L)
	results := make(maxHeap, 0, L)

	heap.Push(&candidates, searchCandidate{id: medoid, dist: distFn(medoid)})

	for candidates.Len() > 0 {
		current := heap.Pop(&candidates).(searchCandidate)

		if _, seen := visited[current.id]; seen {
			continue
		}
		visited[current.id] = struct{}{}

		heap.Push(&results, current)
		if results.Len() > L {
			heap.Pop(&results)
		}

		bound := 2.0
		if results.Len() > 0 {
			bound = results[0].dist
		}

		neighbors := segmentGraph.GetNeighbors(current.id)
		for _, nid := range neighbors {
			if _, seen := visited[nid]; seen {
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
