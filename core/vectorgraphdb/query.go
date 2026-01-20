package vectorgraphdb

import (
	"sort"
)

// HybridResult represents a combined vector + graph search result.
type HybridResult struct {
	Node            *GraphNode
	VectorScore     float64
	GraphScore      float64
	CombinedScore   float64
	ConnectionCount int
}

// HybridQueryOptions configures hybrid query behavior.
type HybridQueryOptions struct {
	VectorWeight float64
	GraphWeight  float64
	VectorLimit  int
	GraphDepth   int
	MinCombined  float64
	Domains      []Domain
	NodeTypes    []NodeType
	EdgeTypes    []EdgeType
}

// QueryEngine provides hybrid vector + graph queries.
type QueryEngine struct {
	vs        *VectorSearcher
	gt        *GraphTraverser
	db        *VectorGraphDB
	nodeStore *NodeStore // Cached instance for reuse
}

// NewQueryEngine creates a new QueryEngine.
func NewQueryEngine(db *VectorGraphDB, vs *VectorSearcher, gt *GraphTraverser) *QueryEngine {
	return &QueryEngine{
		vs:        vs,
		gt:        gt,
		db:        db,
		nodeStore: NewNodeStore(db, nil), // Create once and reuse
	}
}

// getNodeStore returns the cached NodeStore instance.
func (qe *QueryEngine) getNodeStore() *NodeStore {
	return qe.nodeStore
}

// HybridQuery combines vector similarity with graph connectivity.
func (qe *QueryEngine) HybridQuery(query []float32, seedNodes []string, opts *HybridQueryOptions) ([]HybridResult, error) {
	opts = qe.normalizeOptions(opts)

	vectorResults, err := qe.searchVector(query, opts)
	if err != nil {
		return nil, err
	}

	graphNodes, err := qe.expandGraph(seedNodes, opts)
	if err != nil {
		return nil, err
	}

	return qe.combineResults(vectorResults, graphNodes, opts), nil
}

func (qe *QueryEngine) normalizeOptions(opts *HybridQueryOptions) *HybridQueryOptions {
	if opts == nil {
		opts = &HybridQueryOptions{}
	}
	qe.normalizeWeights(opts)
	qe.normalizeLimits(opts)
	return opts
}

func (qe *QueryEngine) normalizeWeights(opts *HybridQueryOptions) {
	if opts.VectorWeight == 0 && opts.GraphWeight == 0 {
		opts.VectorWeight = 0.7
		opts.GraphWeight = 0.3
	}
}

func (qe *QueryEngine) normalizeLimits(opts *HybridQueryOptions) {
	if opts.VectorLimit <= 0 {
		opts.VectorLimit = 20
	}
	if opts.GraphDepth <= 0 {
		opts.GraphDepth = 2
	}
}

func (qe *QueryEngine) searchVector(query []float32, opts *HybridQueryOptions) (map[string]float64, error) {
	results, err := qe.vs.Search(query, &SearchOptions{
		Domains:   opts.Domains,
		NodeTypes: opts.NodeTypes,
		Limit:     opts.VectorLimit,
	})
	if err != nil {
		return nil, err
	}

	scores := make(map[string]float64)
	for _, r := range results {
		scores[r.Node.ID] = r.Similarity
	}
	return scores, nil
}

func (qe *QueryEngine) expandGraph(seedNodes []string, opts *HybridQueryOptions) (map[string]int, error) {
	connections := make(map[string]int)

	for _, seed := range seedNodes {
		if err := qe.expandFromSeed(seed, opts, connections); err != nil {
			return nil, err
		}
	}
	return connections, nil
}

func (qe *QueryEngine) expandFromSeed(seed string, opts *HybridQueryOptions, connections map[string]int) error {
	travOpts := &TraversalOptions{
		MaxDepth:  opts.GraphDepth,
		EdgeTypes: opts.EdgeTypes,
		Direction: DirectionBoth,
	}

	component, err := qe.gt.GetConnectedComponent(seed, travOpts)
	if err != nil {
		return err
	}

	for _, node := range component {
		connections[node.ID]++
	}
	return nil
}

func (qe *QueryEngine) combineResults(vectorScores map[string]float64, graphCounts map[string]int, opts *HybridQueryOptions) []HybridResult {
	allIDs := qe.collectAllIDs(vectorScores, graphCounts)
	maxConn := qe.maxConnections(graphCounts)

	results := qe.scoreResults(allIDs, vectorScores, graphCounts, maxConn, opts)
	return qe.filterAndSort(results, opts.MinCombined)
}

func (qe *QueryEngine) collectAllIDs(vectorScores map[string]float64, graphCounts map[string]int) map[string]bool {
	allIDs := make(map[string]bool)
	for id := range vectorScores {
		allIDs[id] = true
	}
	for id := range graphCounts {
		allIDs[id] = true
	}
	return allIDs
}

func (qe *QueryEngine) maxConnections(graphCounts map[string]int) int {
	max := 1
	for _, c := range graphCounts {
		if c > max {
			max = c
		}
	}
	return max
}

func (qe *QueryEngine) scoreResults(allIDs map[string]bool, vectorScores map[string]float64, graphCounts map[string]int, maxConn int, opts *HybridQueryOptions) []HybridResult {
	results := make([]HybridResult, 0, len(allIDs))
	ns := qe.getNodeStore()

	// Collect all IDs for batch loading
	ids := make([]string, 0, len(allIDs))
	for id := range allIDs {
		ids = append(ids, id)
	}

	// Batch load all nodes at once to eliminate N+1 query pattern
	loader := NewBatchLoader(func(batchIDs []string) (map[string]*GraphNode, error) {
		return ns.GetNodesBatch(batchIDs)
	}, DefaultBatchLoaderConfig())

	nodes, err := loader.LoadBatch(ids)
	if err != nil {
		// Fallback to individual loads on batch failure
		for id := range allIDs {
			result := qe.scoreNodeFallback(id, vectorScores, graphCounts, maxConn, opts, ns)
			if result != nil {
				results = append(results, *result)
			}
		}
		return results
	}

	// Score with pre-loaded nodes
	for id := range allIDs {
		node := nodes[id]
		if node == nil {
			continue
		}
		result := qe.scoreNodeWithData(id, node, vectorScores, graphCounts, maxConn, opts)
		results = append(results, result)
	}
	return results
}

// scoreNodeWithData scores a node using pre-loaded node data.
func (qe *QueryEngine) scoreNodeWithData(id string, node *GraphNode, vectorScores map[string]float64, graphCounts map[string]int, maxConn int, opts *HybridQueryOptions) HybridResult {
	vecScore := vectorScores[id]
	graphScore := float64(graphCounts[id]) / float64(maxConn)
	combined := (vecScore * opts.VectorWeight) + (graphScore * opts.GraphWeight)

	return HybridResult{
		Node:            node,
		VectorScore:     vecScore,
		GraphScore:      graphScore,
		CombinedScore:   combined,
		ConnectionCount: graphCounts[id],
	}
}

// scoreNodeFallback loads and scores a single node (used when batch loading fails).
func (qe *QueryEngine) scoreNodeFallback(id string, vectorScores map[string]float64, graphCounts map[string]int, maxConn int, opts *HybridQueryOptions, ns *NodeStore) *HybridResult {
	node, err := ns.GetNode(id)
	if err != nil {
		return nil
	}

	vecScore := vectorScores[id]
	graphScore := float64(graphCounts[id]) / float64(maxConn)
	combined := (vecScore * opts.VectorWeight) + (graphScore * opts.GraphWeight)

	return &HybridResult{
		Node:            node,
		VectorScore:     vecScore,
		GraphScore:      graphScore,
		CombinedScore:   combined,
		ConnectionCount: graphCounts[id],
	}
}

func (qe *QueryEngine) filterAndSort(results []HybridResult, minScore float64) []HybridResult {
	filtered := make([]HybridResult, 0, len(results))
	for _, r := range results {
		if r.CombinedScore >= minScore {
			filtered = append(filtered, r)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CombinedScore > filtered[j].CombinedScore
	})

	return filtered
}

// QueryByContext searches using both query vector and context nodes.
func (qe *QueryEngine) QueryByContext(query []float32, contextNodeIDs []string, limit int) ([]HybridResult, error) {
	results, err := qe.HybridQuery(query, contextNodeIDs, &HybridQueryOptions{
		VectorWeight: 0.6,
		GraphWeight:  0.4,
		VectorLimit:  limit * 2,
		GraphDepth:   2,
	})
	if err != nil {
		return nil, err
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// SemanticExpand finds semantically related nodes from seeds.
func (qe *QueryEngine) SemanticExpand(seedNodeIDs []string, limit int) ([]HybridResult, error) {
	if len(seedNodeIDs) == 0 {
		return nil, nil
	}

	vector, err := qe.vs.hnsw.GetVector(seedNodeIDs[0])
	if err != nil {
		return nil, err
	}

	return qe.QueryByContext(vector, seedNodeIDs, limit)
}

// RelatedInDomain finds related nodes within the same domain.
func (qe *QueryEngine) RelatedInDomain(nodeID string, limit int) ([]HybridResult, error) {
	ns := qe.getNodeStore() // Use cached instance
	node, err := ns.GetNode(nodeID)
	if err != nil {
		return nil, err
	}

	vector, err := qe.vs.hnsw.GetVector(nodeID)
	if err != nil {
		return nil, err
	}

	return qe.HybridQuery(vector, []string{nodeID}, &HybridQueryOptions{
		Domains:      []Domain{node.Domain},
		VectorWeight: 0.8,
		GraphWeight:  0.2,
		VectorLimit:  limit + 1,
		GraphDepth:   1,
	})
}

// CrossDomainQuery finds related nodes across different domains.
func (qe *QueryEngine) CrossDomainQuery(query []float32, sourceDomain Domain, targetDomains []Domain, limit int) (map[Domain][]HybridResult, error) {
	results := make(map[Domain][]HybridResult)

	for _, domain := range targetDomains {
		domainResults, err := qe.queryDomain(query, domain, limit)
		if err != nil {
			return nil, err
		}
		results[domain] = domainResults
	}

	return results, nil
}

func (qe *QueryEngine) queryDomain(query []float32, domain Domain, limit int) ([]HybridResult, error) {
	searchResults, err := qe.vs.SearchByDomain(query, domain, limit)
	if err != nil {
		return nil, err
	}

	hybridResults := make([]HybridResult, len(searchResults))
	for i, r := range searchResults {
		hybridResults[i] = HybridResult{
			Node:          r.Node,
			VectorScore:   r.Similarity,
			CombinedScore: r.Similarity,
		}
	}
	return hybridResults, nil
}
