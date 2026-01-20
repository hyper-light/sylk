// Package vectorgraphdb provides HQ.5.1: VectorGraphDB Hybrid Query Integration.
// VectorGraphDBHybridAdapter wraps VectorGraphDB to implement an interface compatible
// with HybridQueryCoordinator, bridging VectorGraphDB storage with the query system.
package vectorgraphdb

import (
	"context"
	"fmt"
	"sync"
)

// =============================================================================
// Hybrid Query Interfaces
// =============================================================================

// VectorSearchable defines the interface for vector search operations.
type VectorSearchable interface {
	// Search performs a k-nearest neighbors search.
	// Returns IDs and distances for the k nearest vectors.
	Search(vector []float32, k int) (ids []string, distances []float32, err error)
}

// GraphTraversable defines the interface for graph traversal operations.
type GraphTraversable interface {
	// GetOutgoingEdges returns all outgoing edges from a node.
	GetOutgoingEdges(nodeID string, edgeTypes []string) ([]TraversalEdge, error)

	// GetIncomingEdges returns all incoming edges to a node.
	GetIncomingEdges(nodeID string, edgeTypes []string) ([]TraversalEdge, error)

	// GetNodesByPattern finds nodes matching constraints.
	GetNodesByPattern(pattern *NodePattern) ([]string, error)
}

// EntityRetrievable defines the interface for entity retrieval.
type EntityRetrievable interface {
	// GetEntity retrieves an entity by ID.
	GetEntity(id string) (*HybridEntity, error)

	// GetEntities retrieves multiple entities by ID.
	GetEntities(ids []string) ([]*HybridEntity, error)
}

// =============================================================================
// Supporting Types
// =============================================================================

// TraversalEdge represents an edge for traversal operations.
type TraversalEdge struct {
	ID       int64   `json:"id"`
	SourceID string  `json:"source_id"`
	TargetID string  `json:"target_id"`
	EdgeType string  `json:"edge_type"`
	Weight   float64 `json:"weight"`
}

// NodePattern defines constraints for matching nodes.
type NodePattern struct {
	// EntityType constraint (nil matches any type)
	EntityType *string `json:"entity_type,omitempty"`

	// NamePattern for matching node names (supports wildcards)
	NamePattern string `json:"name_pattern,omitempty"`

	// Properties that must match
	Properties map[string]any `json:"properties,omitempty"`
}

// Matches returns true if the pattern has any constraints.
func (np *NodePattern) Matches() bool {
	if np == nil {
		return false
	}
	return np.EntityType != nil || np.NamePattern != "" || len(np.Properties) > 0
}

// HybridEntity represents a knowledge entity for the hybrid query system.
// This is distinct from the Entity type in entity_types.go which is used
// for identity resolution. HybridEntity provides a simplified view for
// query results.
type HybridEntity struct {
	ID          string         `json:"id"`
	EntityType  string         `json:"entity_type"`
	Name        string         `json:"name"`
	Content     string         `json:"content,omitempty"`
	Domain      Domain         `json:"domain"`
	NodeType    NodeType       `json:"node_type"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Connections int            `json:"connections,omitempty"`
}

// =============================================================================
// VectorGraphDB Hybrid Adapter
// =============================================================================

// VectorGraphDBHybridAdapter wraps VectorGraphDB to implement hybrid query interfaces.
// It provides SearchVector(), TraverseGraph(), and GetEntity() methods compatible
// with the HybridQueryCoordinator.
type VectorGraphDBHybridAdapter struct {
	db         *VectorGraphDB
	hnsw       HNSWSearcher
	nodeStore  *NodeStore
	edgeStore  *EdgeStore
	traverser  *GraphTraverser
	domainHint *Domain

	mu sync.RWMutex
}

// VectorGraphDBHybridAdapterConfig configures the hybrid adapter.
type VectorGraphDBHybridAdapterConfig struct {
	// DB is the VectorGraphDB instance to wrap.
	DB *VectorGraphDB

	// HNSW is the HNSW index for vector search.
	HNSW HNSWSearcher

	// DomainHint is an optional domain to filter results.
	DomainHint *Domain
}

// NewVectorGraphDBHybridAdapter creates a new hybrid adapter.
func NewVectorGraphDBHybridAdapter(config VectorGraphDBHybridAdapterConfig) *VectorGraphDBHybridAdapter {
	if config.DB == nil {
		return nil
	}

	adapter := &VectorGraphDBHybridAdapter{
		db:         config.DB,
		hnsw:       config.HNSW,
		nodeStore:  NewNodeStore(config.DB, nil),
		edgeStore:  NewEdgeStore(config.DB),
		traverser:  NewGraphTraverser(config.DB),
		domainHint: config.DomainHint,
	}

	return adapter
}

// NewVectorGraphDBHybridAdapterSimple creates a hybrid adapter with minimal config.
func NewVectorGraphDBHybridAdapterSimple(db *VectorGraphDB, hnsw HNSWSearcher) *VectorGraphDBHybridAdapter {
	return NewVectorGraphDBHybridAdapter(VectorGraphDBHybridAdapterConfig{
		DB:   db,
		HNSW: hnsw,
	})
}

// =============================================================================
// VectorSearchable Implementation
// =============================================================================

// Search implements VectorSearchable interface.
// Performs k-nearest neighbors search using the HNSW index.
func (a *VectorGraphDBHybridAdapter) Search(vector []float32, k int) ([]string, []float32, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.hnsw == nil {
		return nil, nil, fmt.Errorf("HNSW index not configured")
	}

	if len(vector) == 0 {
		return nil, nil, fmt.Errorf("empty query vector")
	}

	if k <= 0 {
		k = 10
	}

	// Build filter from domain hint
	var filter *HNSWSearchFilter
	if a.domainHint != nil {
		filter = &HNSWSearchFilter{
			Domains: []Domain{*a.domainHint},
		}
	}

	// Execute search
	results := a.hnsw.Search(vector, k, filter)

	// Convert results
	ids := make([]string, len(results))
	distances := make([]float32, len(results))
	for i, r := range results {
		ids[i] = r.ID
		// Convert similarity to distance (higher similarity = lower distance)
		distances[i] = float32(1.0 - r.Similarity)
	}

	return ids, distances, nil
}

// SearchVector performs vector search with additional options.
func (a *VectorGraphDBHybridAdapter) SearchVector(ctx context.Context, vector []float32, k int, opts *VectorSearchOptions) (*VectorSearchResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.hnsw == nil {
		return &VectorSearchResult{Results: []VectorMatch{}}, nil
	}

	if len(vector) == 0 {
		return &VectorSearchResult{Results: []VectorMatch{}}, nil
	}

	if k <= 0 {
		k = 10
	}

	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Build filter
	filter := a.buildSearchFilter(opts)

	// Execute search
	results := a.hnsw.Search(vector, k, filter)

	// Convert to VectorMatch
	matches := make([]VectorMatch, 0, len(results))
	for _, r := range results {
		match := VectorMatch{
			ID:         r.ID,
			Similarity: r.Similarity,
			Distance:   float32(1.0 - r.Similarity),
			Domain:     r.Domain,
			NodeType:   r.NodeType,
		}
		matches = append(matches, match)
	}

	return &VectorSearchResult{Results: matches}, nil
}

// buildSearchFilter builds an HNSW filter from options.
func (a *VectorGraphDBHybridAdapter) buildSearchFilter(opts *VectorSearchOptions) *HNSWSearchFilter {
	if opts == nil && a.domainHint == nil {
		return nil
	}

	filter := &HNSWSearchFilter{}

	if opts != nil {
		filter.Domains = opts.Domains
		filter.NodeTypes = opts.NodeTypes
		filter.MinSimilarity = opts.MinSimilarity
	}

	// Apply domain hint if no domains specified
	if len(filter.Domains) == 0 && a.domainHint != nil {
		filter.Domains = []Domain{*a.domainHint}
	}

	return filter
}

// VectorSearchOptions configures vector search behavior.
type VectorSearchOptions struct {
	Domains       []Domain
	NodeTypes     []NodeType
	MinSimilarity float64
}

// VectorSearchResult contains vector search results.
type VectorSearchResult struct {
	Results []VectorMatch
}

// VectorMatch represents a single vector search match.
type VectorMatch struct {
	ID         string
	Similarity float64
	Distance   float32
	Domain     Domain
	NodeType   NodeType
}

// =============================================================================
// GraphTraversable Implementation
// =============================================================================

// GetOutgoingEdges implements GraphTraversable interface.
func (a *VectorGraphDBHybridAdapter) GetOutgoingEdges(nodeID string, edgeTypes []string) ([]TraversalEdge, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Convert string edge types to EdgeType
	var types []EdgeType
	for _, et := range edgeTypes {
		if parsed, ok := ParseEdgeType(et); ok {
			types = append(types, parsed)
		}
	}

	edges, err := a.edgeStore.GetOutgoingEdges(nodeID, types...)
	if err != nil {
		return nil, fmt.Errorf("get outgoing edges: %w", err)
	}

	return a.convertEdges(edges), nil
}

// GetIncomingEdges implements GraphTraversable interface.
func (a *VectorGraphDBHybridAdapter) GetIncomingEdges(nodeID string, edgeTypes []string) ([]TraversalEdge, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Convert string edge types to EdgeType
	var types []EdgeType
	for _, et := range edgeTypes {
		if parsed, ok := ParseEdgeType(et); ok {
			types = append(types, parsed)
		}
	}

	edges, err := a.edgeStore.GetIncomingEdges(nodeID, types...)
	if err != nil {
		return nil, fmt.Errorf("get incoming edges: %w", err)
	}

	return a.convertEdges(edges), nil
}

// convertEdges converts GraphEdge slice to TraversalEdge slice.
func (a *VectorGraphDBHybridAdapter) convertEdges(edges []*GraphEdge) []TraversalEdge {
	result := make([]TraversalEdge, len(edges))
	for i, e := range edges {
		result[i] = TraversalEdge{
			ID:       e.ID,
			SourceID: e.SourceID,
			TargetID: e.TargetID,
			EdgeType: e.EdgeType.String(),
			Weight:   e.Weight,
		}
	}
	return result
}

// GetNodesByPattern implements GraphTraversable interface.
func (a *VectorGraphDBHybridAdapter) GetNodesByPattern(pattern *NodePattern) ([]string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if pattern == nil || !pattern.Matches() {
		return nil, nil
	}

	// Build query based on pattern
	var ids []string

	if pattern.EntityType != nil {
		// Query by node type
		nodeType, ok := ParseNodeType(*pattern.EntityType)
		if !ok {
			return nil, fmt.Errorf("invalid entity type: %s", *pattern.EntityType)
		}

		// Query nodes by type across all domains
		for _, domain := range ValidDomains() {
			if nodeType.IsValidForDomain(domain) {
				nodes, err := a.nodeStore.GetNodesByType(domain, nodeType, 100)
				if err != nil {
					continue
				}
				for _, node := range nodes {
					if a.matchesPattern(node, pattern) {
						ids = append(ids, node.ID)
					}
				}
			}
		}
	}

	// Filter by domain hint if set
	if a.domainHint != nil {
		ids = a.filterByDomain(ids)
	}

	return ids, nil
}

// matchesPattern checks if a node matches the pattern constraints.
func (a *VectorGraphDBHybridAdapter) matchesPattern(node *GraphNode, pattern *NodePattern) bool {
	// Check name pattern
	if pattern.NamePattern != "" {
		if !matchWildcard(pattern.NamePattern, node.Name) {
			return false
		}
	}

	// Check properties
	for key, value := range pattern.Properties {
		nodeValue, ok := node.Metadata[key]
		if !ok || nodeValue != value {
			return false
		}
	}

	return true
}

// matchWildcard performs simple wildcard matching.
func matchWildcard(pattern, text string) bool {
	// Simple implementation: * matches any sequence
	if pattern == "*" {
		return true
	}

	// Prefix match
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(text) >= len(prefix) && text[:len(prefix)] == prefix
	}

	// Suffix match
	if len(pattern) > 0 && pattern[0] == '*' {
		suffix := pattern[1:]
		return len(text) >= len(suffix) && text[len(text)-len(suffix):] == suffix
	}

	// Exact match
	return pattern == text
}

// filterByDomain filters node IDs to only those matching the domain hint.
func (a *VectorGraphDBHybridAdapter) filterByDomain(ids []string) []string {
	if a.domainHint == nil {
		return ids
	}

	var filtered []string
	for _, id := range ids {
		node, err := a.nodeStore.GetNode(id)
		if err != nil {
			continue
		}
		if node.Domain == *a.domainHint {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// TraverseGraph performs graph traversal from a starting node.
func (a *VectorGraphDBHybridAdapter) TraverseGraph(ctx context.Context, startID string, opts *TraverseOptions) (*TraverseResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Use default options if nil
	if opts == nil {
		opts = DefaultTraverseOptions()
	}

	// Get subgraph from start node
	subgraph, err := a.traverser.GetSubgraph(startID, opts.MaxDepth)
	if err != nil {
		return nil, fmt.Errorf("traverse graph: %w", err)
	}

	// Convert to TraverseResult
	result := &TraverseResult{
		Nodes: make([]*HybridEntity, 0, len(subgraph.Nodes)),
		Edges: make([]TraversalEdge, 0, len(subgraph.Edges)),
	}

	for _, node := range subgraph.Nodes {
		entity := a.nodeToHybridEntity(node)
		result.Nodes = append(result.Nodes, entity)
	}

	result.Edges = a.convertEdges(subgraph.Edges)

	return result, nil
}

// TraverseOptions configures graph traversal behavior.
type TraverseOptions struct {
	MaxDepth  int
	EdgeTypes []EdgeType
	Direction TraversalDirection
}

// DefaultTraverseOptions returns default traversal options.
func DefaultTraverseOptions() *TraverseOptions {
	return &TraverseOptions{
		MaxDepth:  2,
		Direction: DirectionBoth,
	}
}

// TraverseResult contains graph traversal results.
type TraverseResult struct {
	Nodes []*HybridEntity
	Edges []TraversalEdge
}

// =============================================================================
// EntityRetrievable Implementation
// =============================================================================

// GetEntity implements EntityRetrievable interface.
func (a *VectorGraphDBHybridAdapter) GetEntity(id string) (*HybridEntity, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	node, err := a.nodeStore.GetNode(id)
	if err != nil {
		return nil, fmt.Errorf("get entity: %w", err)
	}

	return a.nodeToHybridEntity(node), nil
}

// GetEntities retrieves multiple entities by ID.
func (a *VectorGraphDBHybridAdapter) GetEntities(ids []string) ([]*HybridEntity, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entities := make([]*HybridEntity, 0, len(ids))
	for _, id := range ids {
		node, err := a.nodeStore.GetNode(id)
		if err != nil {
			continue // Skip missing nodes
		}
		entities = append(entities, a.nodeToHybridEntity(node))
	}

	return entities, nil
}

// nodeToHybridEntity converts a GraphNode to a HybridEntity.
func (a *VectorGraphDBHybridAdapter) nodeToHybridEntity(node *GraphNode) *HybridEntity {
	if node == nil {
		return nil
	}

	// Count connections
	connections := a.countConnections(node.ID)

	return &HybridEntity{
		ID:          node.ID,
		EntityType:  node.NodeType.String(),
		Name:        node.Name,
		Content:     node.Content,
		Domain:      node.Domain,
		NodeType:    node.NodeType,
		Metadata:    node.Metadata,
		Connections: connections,
	}
}

// countConnections counts the number of edges connected to a node.
func (a *VectorGraphDBHybridAdapter) countConnections(nodeID string) int {
	outgoing, _ := a.edgeStore.GetOutgoingEdges(nodeID)
	incoming, _ := a.edgeStore.GetIncomingEdges(nodeID)
	return len(outgoing) + len(incoming)
}

// =============================================================================
// Configuration Methods
// =============================================================================

// SetDomainHint sets the domain filter hint.
func (a *VectorGraphDBHybridAdapter) SetDomainHint(domain *Domain) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.domainHint = domain
}

// GetDomainHint returns the current domain hint.
func (a *VectorGraphDBHybridAdapter) GetDomainHint() *Domain {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.domainHint
}

// SetHNSW updates the HNSW index.
func (a *VectorGraphDBHybridAdapter) SetHNSW(hnsw HNSWSearcher) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hnsw = hnsw
}

// IsReady returns true if the adapter is properly configured.
func (a *VectorGraphDBHybridAdapter) IsReady() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.db != nil && a.hnsw != nil
}

// DB returns the underlying VectorGraphDB.
func (a *VectorGraphDBHybridAdapter) DB() *VectorGraphDB {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.db
}

// =============================================================================
// Hybrid Query Execution
// =============================================================================

// ExecuteHybridQuery executes a hybrid query combining vector and graph search.
func (a *VectorGraphDBHybridAdapter) ExecuteHybridQuery(ctx context.Context, query *HybridQueryRequest) (*HybridQueryResponse, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if query == nil {
		return &HybridQueryResponse{Results: []HybridQueryMatch{}}, nil
	}

	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Execute vector search if vector provided
	var vectorMatches []VectorMatch
	if len(query.Vector) > 0 && a.hnsw != nil {
		vectorResult, err := a.SearchVector(ctx, query.Vector, query.Limit*2, &VectorSearchOptions{
			Domains:       query.Domains,
			NodeTypes:     query.NodeTypes,
			MinSimilarity: query.MinSimilarity,
		})
		if err == nil && vectorResult != nil {
			vectorMatches = vectorResult.Results
		}
	}

	// Execute graph traversal if start node provided
	var graphNodes []*HybridEntity
	if query.StartNodeID != "" {
		traverseResult, err := a.TraverseGraph(ctx, query.StartNodeID, &TraverseOptions{
			MaxDepth:  query.GraphDepth,
			EdgeTypes: query.EdgeTypes,
		})
		if err == nil && traverseResult != nil {
			graphNodes = traverseResult.Nodes
		}
	}

	// Combine results
	return a.combineResults(vectorMatches, graphNodes, query), nil
}

// combineResults merges vector and graph results into HybridQueryResponse.
func (a *VectorGraphDBHybridAdapter) combineResults(
	vectorMatches []VectorMatch,
	graphNodes []*HybridEntity,
	query *HybridQueryRequest,
) *HybridQueryResponse {
	// Create result map for deduplication
	resultMap := make(map[string]*HybridQueryMatch)

	// Process vector matches
	for _, vm := range vectorMatches {
		match := &HybridQueryMatch{
			EntityID:    vm.ID,
			VectorScore: vm.Similarity,
		}
		resultMap[vm.ID] = match
	}

	// Process graph nodes
	for _, node := range graphNodes {
		if existing, ok := resultMap[node.ID]; ok {
			existing.GraphScore = 1.0 / float64(node.Connections+1)
			existing.Entity = node
		} else {
			resultMap[node.ID] = &HybridQueryMatch{
				EntityID:   node.ID,
				GraphScore: 1.0 / float64(node.Connections+1),
				Entity:     node,
			}
		}
	}

	// Calculate combined scores and build result slice
	results := make([]HybridQueryMatch, 0, len(resultMap))
	vectorWeight := query.VectorWeight
	graphWeight := query.GraphWeight

	// Normalize weights
	total := vectorWeight + graphWeight
	if total > 0 {
		vectorWeight /= total
		graphWeight /= total
	} else {
		vectorWeight = 0.5
		graphWeight = 0.5
	}

	for _, match := range resultMap {
		match.CombinedScore = match.VectorScore*vectorWeight + match.GraphScore*graphWeight

		// Load entity if not already loaded
		if match.Entity == nil {
			entity, err := a.GetEntity(match.EntityID)
			if err == nil {
				match.Entity = entity
			}
		}

		results = append(results, *match)
	}

	// Sort by combined score descending
	sortHybridMatches(results)

	// Apply limit
	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return &HybridQueryResponse{Results: results}
}

// sortHybridMatches sorts matches by combined score descending.
func sortHybridMatches(matches []HybridQueryMatch) {
	for i := 0; i < len(matches)-1; i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].CombinedScore > matches[i].CombinedScore {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
}

// HybridQueryRequest defines parameters for a hybrid query.
type HybridQueryRequest struct {
	// Vector for semantic search
	Vector []float32

	// StartNodeID for graph traversal
	StartNodeID string

	// Query weights
	VectorWeight float64
	GraphWeight  float64

	// Search constraints
	Domains       []Domain
	NodeTypes     []NodeType
	EdgeTypes     []EdgeType
	MinSimilarity float64

	// Graph traversal depth
	GraphDepth int

	// Result limit
	Limit int
}

// HybridQueryResponse contains hybrid query results.
type HybridQueryResponse struct {
	Results []HybridQueryMatch
}

// HybridQueryMatch represents a single hybrid query result.
type HybridQueryMatch struct {
	EntityID      string
	Entity        *HybridEntity
	VectorScore   float64
	GraphScore    float64
	CombinedScore float64
}
