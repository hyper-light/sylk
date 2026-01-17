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

// HNSWSearchResult represents a search result from HNSW index.
type HNSWSearchResult struct {
	ID         string
	Similarity float64
	Domain     Domain
	NodeType   NodeType
}

// HNSWSearchFilter configures HNSW search filtering.
type HNSWSearchFilter struct {
	Domains       []Domain
	NodeTypes     []NodeType
	MinSimilarity float64
}

// HNSWSearcher interface for vector search operations.
type HNSWSearcher interface {
	Search(query []float32, k int, filter *HNSWSearchFilter) []HNSWSearchResult
	GetVector(id string) ([]float32, error)
}

// VectorSearcher provides vector similarity search capabilities.
type VectorSearcher struct {
	db   *VectorGraphDB
	hnsw HNSWSearcher
}

// NewVectorSearcher creates a new VectorSearcher.
func NewVectorSearcher(db *VectorGraphDB, hnswIndex HNSWSearcher) *VectorSearcher {
	return &VectorSearcher{db: db, hnsw: hnswIndex}
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
	hnswResults := vs.hnsw.Search(query, opts.Limit, filter)

	return vs.loadNodes(hnswResults, opts.MinSimilarity)
}

func (vs *VectorSearcher) buildFilter(opts *SearchOptions) *HNSWSearchFilter {
	return &HNSWSearchFilter{
		Domains:       opts.Domains,
		NodeTypes:     opts.NodeTypes,
		MinSimilarity: opts.MinSimilarity,
	}
}

func (vs *VectorSearcher) loadNodes(hnswResults []HNSWSearchResult, minSim float64) ([]SearchResult, error) {
	results := make([]SearchResult, 0, len(hnswResults))
	ns := NewNodeStore(vs.db, nil)

	for _, hr := range hnswResults {
		if hr.Similarity < minSim {
			continue
		}
		result, err := vs.loadSingleResult(ns, hr)
		if err != nil {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func (vs *VectorSearcher) loadSingleResult(ns *NodeStore, hr HNSWSearchResult) (SearchResult, error) {
	node, err := ns.GetNode(hr.ID)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{Node: node, Similarity: hr.Similarity}, nil
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
	vector, err := vs.hnsw.GetVector(nodeID)
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
