package vectorgraphdb

import (
	"fmt"
	"sort"
)

// SearchResult represents a vector similarity search result with node metadata.
// Similarity is the raw vector distance; Score is the ranking-adjusted value
// used for ordering and surfaced to callers. When no trust weighting is
// requested Score == Similarity.
type SearchResult struct {
	Node       *GraphNode
	Similarity float64
	Score      float64
	// Conflicts are active conflict IDs involving Node.ID, populated when
	// SearchOptions.SurfaceConflicts is set and a conflict provider is
	// wired. Zero-valued otherwise.
	Conflicts []string
	// Provenance is the source-attribution chain for Node.ID, populated
	// when SearchOptions.AttachProvenance is set and a ProvenanceLookup
	// is wired (KG-31). Each entry encodes one hop on the path-to-root
	// source trail — agents that need to cite or audit can walk this.
	Provenance []ProvenanceHop
}

// ProvenanceHop is one link on a node's source-attribution chain. Kept
// package-local (not tied to mitigations.Provenance) so the search layer
// doesn't acquire a cyclic dependency on the mitigations subpackage.
type ProvenanceHop struct {
	SourceType SourceType
	SourceID   string
	Confidence float64
	VerifiedAt int64 // unix nano
}

// ProvenanceLookup is the minimal interface the search ranker needs to
// attach source chains to results. A concrete implementation wraps
// core/vectorgraphdb/mitigations.ProvenanceTracker.
type ProvenanceLookup interface {
	ChainForNode(nodeID string, maxDepth int) []ProvenanceHop
}

// SearchOptions configures vector search behavior.
type SearchOptions struct {
	Domains       []Domain
	NodeTypes     []NodeType
	MinSimilarity float64
	Limit         int

	// TrustBoost controls how much a node's TrustLevel influences ranking,
	// in [0, 1]. 0 disables trust weighting (default, preserves raw
	// similarity order); 1 fully substitutes trust for similarity. A
	// typical value is 0.2–0.4. See applyTrustBoost for the formula.
	TrustBoost float64

	// HomeDomains restricts results to nodes whose Domain is in this list
	// OR whose canonical domain is listed. Used by Architect to enforce
	// per-request domain scoping; empty means no restriction. See also
	// Domains (exact-match filter applied at the vector-index layer).
	HomeDomains []Domain

	// SurfaceConflicts, when true, instructs the ranker to populate
	// SearchResult.Conflicts via the DB's conflict provider (KG-32).
	SurfaceConflicts bool

	// AttachProvenance, when true, instructs the ranker to populate
	// SearchResult.Provenance via the wired ProvenanceLookup (KG-31).
	AttachProvenance bool
	// ProvenanceDepth caps the per-result chain depth; defaults to
	// defaultProvenanceDepth when zero.
	ProvenanceDepth int
}

// defaultProvenanceDepth is the fallback chain depth for KG-31 attachment
// when SearchOptions.ProvenanceDepth is unset. Matches the common use-case
// of "direct source plus one verifier hop."
const defaultProvenanceDepth = 3

// ConflictLookup is the minimal interface the search ranker needs to
// surface active conflicts per node. Implementations typically delegate
// to core/vectorgraphdb/mitigations.ConflictDetector.
type ConflictLookup interface {
	ActiveForNode(nodeID string) []string
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

	// conflicts (optional) is consulted when SurfaceConflicts is set in
	// the search options. Nil disables conflict surfacing even if the
	// option is true, so producing side-effect-free results remains
	// cheap in the common case.
	conflicts ConflictLookup

	// provenance (optional) is consulted when AttachProvenance is set.
	// Nil disables the feature regardless of the option.
	provenance ProvenanceLookup
}

// SetConflictLookup wires a ConflictLookup for KG-32 conflict surfacing.
// Pass nil to disable.
func (vs *VectorSearcher) SetConflictLookup(c ConflictLookup) {
	vs.conflicts = c
}

// SetProvenanceLookup wires a ProvenanceLookup for KG-31 source-chain
// surfacing. Pass nil to disable.
func (vs *VectorSearcher) SetProvenanceLookup(p ProvenanceLookup) {
	vs.provenance = p
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

	results, err := vs.loadNodes(indexResults, opts.MinSimilarity)
	if err != nil {
		return nil, err
	}
	results = applyHomeDomainFilter(results, opts.HomeDomains)
	applyTrustBoost(results, opts.TrustBoost)
	vs.applyConflictSurfacing(results, opts.SurfaceConflicts)
	vs.applyProvenanceAttachment(results, opts.AttachProvenance, opts.ProvenanceDepth)
	sortByScore(results)
	return results, nil
}

// applyProvenanceAttachment populates SearchResult.Provenance via the
// wired ProvenanceLookup (KG-31). No-op when the lookup is absent or the
// option is off.
func (vs *VectorSearcher) applyProvenanceAttachment(results []SearchResult, attach bool, depth int) {
	if !attach || vs == nil || vs.provenance == nil {
		return
	}
	if depth <= 0 {
		depth = defaultProvenanceDepth
	}
	for i := range results {
		if results[i].Node == nil {
			continue
		}
		chain := vs.provenance.ChainForNode(results[i].Node.ID, depth)
		if len(chain) > 0 {
			results[i].Provenance = chain
		}
	}
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

	// Build results with pre-loaded nodes, preserving order. Score is
	// seeded from Similarity; any trust-boost / conflict surfacing runs
	// in Search() after this function returns.
	results := make([]SearchResult, 0, len(filtered))
	for _, hr := range filtered {
		node := nodes[hr.ID]
		if node == nil {
			continue
		}
		results = append(results, SearchResult{
			Node:       node,
			Similarity: hr.Similarity,
			Score:      hr.Similarity,
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
			Score:      ir.Similarity,
		})
	}
	return results, nil
}

// applyHomeDomainFilter drops results whose Node.Domain is not in home
// (KG-33). An empty home list disables the filter.
func applyHomeDomainFilter(results []SearchResult, home []Domain) []SearchResult {
	if len(home) == 0 {
		return results
	}
	allowed := make(map[Domain]struct{}, len(home))
	for _, d := range home {
		allowed[d] = struct{}{}
	}
	filtered := results[:0]
	for _, r := range results {
		if r.Node == nil {
			continue
		}
		if _, ok := allowed[r.Node.Domain]; ok {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// applyTrustBoost folds each node's TrustLevel into its Score using a
// convex combination (KG-30). Score = Similarity * ((1 - boost) + trust *
// boost) where trust is TrustLevel/TrustLevelGround in [0, 1]. A zero
// boost leaves Score == Similarity.
func applyTrustBoost(results []SearchResult, boost float64) {
	if boost < 0 {
		boost = 0
	}
	if boost > 1 {
		boost = 1
	}
	for i := range results {
		results[i].Score = results[i].Similarity
		if results[i].Node == nil {
			continue
		}
		if boost == 0 {
			continue
		}
		trust := trustFactor(results[i].Node.TrustLevel)
		results[i].Score = results[i].Similarity * ((1 - boost) + trust*boost)
	}
}

// trustFactor normalizes a TrustLevel to [0, 1] using TrustLevelGround
// (the highest standard level) as the anchor. Values above ground clamp
// to 1; unset (zero-value) defaults to the "standard" factor to avoid
// penalizing nodes that predate provenance tracking.
func trustFactor(level TrustLevel) float64 {
	if level <= 0 {
		return float64(TrustLevelStandard) / float64(TrustLevelGround)
	}
	f := float64(level) / float64(TrustLevelGround)
	if f > 1 {
		return 1
	}
	return f
}

// applyConflictSurfacing populates SearchResult.Conflicts via the wired
// ConflictLookup when opts.SurfaceConflicts is set.
func (vs *VectorSearcher) applyConflictSurfacing(results []SearchResult, surface bool) {
	if !surface || vs == nil || vs.conflicts == nil {
		return
	}
	for i := range results {
		if results[i].Node == nil {
			continue
		}
		ids := vs.conflicts.ActiveForNode(results[i].Node.ID)
		if len(ids) > 0 {
			results[i].Conflicts = ids
		}
	}
}

// sortByScore orders results by Score descending. Stable sort preserves
// the vector-index order within equal scores.
func sortByScore(results []SearchResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
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
