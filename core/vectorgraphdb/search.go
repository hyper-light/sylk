package vectorgraphdb

import (
	"fmt"
)

// SearchResult represents a vector similarity search result with node metadata.
type SearchResult struct {
	Node       *GraphNode
	Similarity float64
}

// SearchOptions configures vector search behavior.
type SearchOptions struct {
	Domains       []Domain
	NodeTypes     []NodeType
	MinSimilarity float64
	Limit         int
}

// VectorIndexSearchResult represents a search result from the vector index.
type VectorIndexSearchResult struct {
	ID         string
	Similarity float64
	Domain     Domain
	NodeType   NodeType
}

// VectorIndexSearchFilter configures vector search filtering.
type VectorIndexSearchFilter struct {
	Domains       []Domain
	NodeTypes     []NodeType
	MinSimilarity float64
}

// VectorIndexSearcher interface for vector search operations.
// Implementations include IVF+BBQ+Vamana index.
type VectorIndexSearcher interface {
	Search(query []float32, k int, filter *VectorIndexSearchFilter) []VectorIndexSearchResult
	GetVector(id string) ([]float32, error)
}

// VectorSearcher provides vector similarity search capabilities.
type VectorSearcher struct {
	db          *VectorGraphDB
	vectorIndex VectorIndexSearcher
	nodeStore   *NodeStore // Cached instance for reuse
}

// NewVectorSearcher creates a new VectorSearcher.
func NewVectorSearcher(db *VectorGraphDB, vectorIndex VectorIndexSearcher) *VectorSearcher {
	return &VectorSearcher{
		db:          db,
		vectorIndex: vectorIndex,
		nodeStore:   NewNodeStore(db, nil), // Create once and reuse
	}
}

// getNodeStore returns the cached NodeStore instance.
func (vs *VectorSearcher) getNodeStore() *NodeStore {
	return vs.nodeStore
}

// Search performs vector similarity search with optional filters.
func (vs *VectorSearcher) Search(query []float32, opts *SearchOptions) ([]SearchResult, error) {
	if opts == nil {
		opts = &SearchOptions{Limit: 10}
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	filter := vs.buildFilter(opts)
	indexResults := vs.vectorIndex.Search(query, opts.Limit, filter)

	return vs.loadNodes(indexResults, opts.MinSimilarity)
}

func (vs *VectorSearcher) buildFilter(opts *SearchOptions) *VectorIndexSearchFilter {
	return &VectorIndexSearchFilter{
		Domains:       opts.Domains,
		NodeTypes:     opts.NodeTypes,
		MinSimilarity: opts.MinSimilarity,
	}
}

func (vs *VectorSearcher) loadNodes(indexResults []VectorIndexSearchResult, minSim float64) ([]SearchResult, error) {
	// Filter by minimum similarity first
	filtered := make([]VectorIndexSearchResult, 0, len(indexResults))
	for _, hr := range indexResults {
		if hr.Similarity >= minSim {
			filtered = append(filtered, hr)
		}
	}

	if len(filtered) == 0 {
		return []SearchResult{}, nil
	}

	// Collect all IDs for batch loading
	ids := make([]string, len(filtered))
	similarityMap := make(map[string]float64, len(filtered))
	for i, hr := range filtered {
		ids[i] = hr.ID
		similarityMap[hr.ID] = hr.Similarity
	}

	ns := vs.getNodeStore()

	// Batch load all nodes at once to eliminate N+1 query pattern
	loader := NewBatchLoader(func(batchIDs []string) (map[string]*GraphNode, error) {
		return ns.GetNodesBatch(batchIDs)
	}, DefaultBatchLoaderConfig())

	nodes, err := loader.LoadBatch(ids)
	if err != nil {
		// Fallback to individual loads on batch failure
		return vs.loadNodesFallback(filtered, ns)
	}

	// Build results with pre-loaded nodes, preserving order
	results := make([]SearchResult, 0, len(filtered))
	for _, hr := range filtered {
		node := nodes[hr.ID]
		if node == nil {
			continue
		}
		results = append(results, SearchResult{
			Node:       node,
			Similarity: hr.Similarity,
		})
	}
	return results, nil
}

// loadNodesFallback loads nodes individually when batch loading fails.
func (vs *VectorSearcher) loadNodesFallback(indexResults []VectorIndexSearchResult, ns *NodeStore) ([]SearchResult, error) {
	results := make([]SearchResult, 0, len(indexResults))
	for _, ir := range indexResults {
		node, err := ns.GetNode(ir.ID)
		if err != nil {
			continue
		}
		results = append(results, SearchResult{
			Node:       node,
			Similarity: ir.Similarity,
		})
	}
	return results, nil
}

// SearchByDomain performs search filtered to a specific domain.
func (vs *VectorSearcher) SearchByDomain(query []float32, domain Domain, limit int) ([]SearchResult, error) {
	return vs.Search(query, &SearchOptions{
		Domains: []Domain{domain},
		Limit:   limit,
	})
}

// SearchByNodeType performs search filtered to specific node types.
func (vs *VectorSearcher) SearchByNodeType(query []float32, nodeTypes []NodeType, limit int) ([]SearchResult, error) {
	return vs.Search(query, &SearchOptions{
		NodeTypes: nodeTypes,
		Limit:     limit,
	})
}

// SearchMultiDomain performs search across multiple domains with per-domain limits.
func (vs *VectorSearcher) SearchMultiDomain(query []float32, limits map[Domain]int) (map[Domain][]SearchResult, error) {
	results := make(map[Domain][]SearchResult)

	for domain, limit := range limits {
		domainResults, err := vs.SearchByDomain(query, domain, limit)
		if err != nil {
			return nil, fmt.Errorf("search domain %s: %w", domain, err)
		}
		results[domain] = domainResults
	}
	return results, nil
}

// FindSimilar finds nodes similar to a given node by its ID.
func (vs *VectorSearcher) FindSimilar(nodeID string, limit int) ([]SearchResult, error) {
	vector, err := vs.getNodeVector(nodeID)
	if err != nil {
		return nil, err
	}
	return vs.findSimilarExcluding(vector, nodeID, limit)
}

func (vs *VectorSearcher) getNodeVector(nodeID string) ([]float32, error) {
	vector, err := vs.vectorIndex.GetVector(nodeID)
	if err != nil {
		return nil, fmt.Errorf("get vector: %w", err)
	}
	return vector, nil
}

func (vs *VectorSearcher) findSimilarExcluding(vector []float32, excludeID string, limit int) ([]SearchResult, error) {
	results, err := vs.Search(vector, &SearchOptions{Limit: limit + 1})
	if err != nil {
		return nil, err
	}
	return vs.filterExcluded(results, excludeID, limit), nil
}

func (vs *VectorSearcher) filterExcluded(results []SearchResult, excludeID string, limit int) []SearchResult {
	filtered := make([]SearchResult, 0, limit)
	for _, r := range results {
		if r.Node.ID == excludeID {
			continue
		}
		filtered = append(filtered, r)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered
}
