package vectorgraphdb

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// =============================================================================
// Mock HNSW Searcher for Hybrid Query Tests
// =============================================================================

type hybridTestHNSWSearcher struct {
	results []HNSWSearchResult
	vectors map[string][]float32
}

func newHybridTestHNSWSearcher() *hybridTestHNSWSearcher {
	return &hybridTestHNSWSearcher{
		results: []HNSWSearchResult{},
		vectors: make(map[string][]float32),
	}
}

func (m *hybridTestHNSWSearcher) Search(query []float32, k int, filter *HNSWSearchFilter) []HNSWSearchResult {
	var results []HNSWSearchResult

	for _, r := range m.results {
		if filter != nil {
			// Apply domain filter
			if len(filter.Domains) > 0 {
				found := false
				for _, d := range filter.Domains {
					if d == r.Domain {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			// Apply node type filter
			if len(filter.NodeTypes) > 0 {
				found := false
				for _, nt := range filter.NodeTypes {
					if nt == r.NodeType {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			// Apply minimum similarity filter
			if r.Similarity < filter.MinSimilarity {
				continue
			}
		}

		results = append(results, r)
		if len(results) >= k {
			break
		}
	}

	return results
}

func (m *hybridTestHNSWSearcher) GetVector(id string) ([]float32, error) {
	if v, ok := m.vectors[id]; ok {
		return v, nil
	}
	return nil, ErrNodeNotFound
}

func (m *hybridTestHNSWSearcher) addResult(id string, similarity float64, domain Domain, nodeType NodeType) {
	m.results = append(m.results, HNSWSearchResult{
		ID:         id,
		Similarity: similarity,
		Domain:     domain,
		NodeType:   nodeType,
	})
}

func (m *hybridTestHNSWSearcher) setVector(id string, vector []float32) {
	m.vectors[id] = vector
}

// =============================================================================
// Test Helper Functions for Hybrid Query Tests
// =============================================================================

func setupHybridTestDB(t *testing.T) (*VectorGraphDB, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hybrid_test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	return db, func() { db.Close() }
}

func setupTestAdapter(t *testing.T) (*VectorGraphDBHybridAdapter, *hybridTestHNSWSearcher, func()) {
	t.Helper()

	db, cleanup := setupHybridTestDB(t)
	hnsw := newHybridTestHNSWSearcher()

	adapter := NewVectorGraphDBHybridAdapter(VectorGraphDBHybridAdapterConfig{
		DB:   db,
		HNSW: hnsw,
	})

	return adapter, hnsw, cleanup
}

func insertHybridTestNode(t *testing.T, db *VectorGraphDB, id string, domain Domain, nodeType NodeType) {
	t.Helper()

	ns := NewNodeStore(db, nil)
	node := &GraphNode{
		ID:       id,
		Domain:   domain,
		NodeType: nodeType,
		Name:     id,
		Content:  "Test content for " + id,
		Metadata: map[string]any{"test": true},
	}

	embedding := make([]float32, EmbeddingDimension)
	for i := range embedding {
		embedding[i] = 0.1
	}

	err := ns.InsertNode(node, embedding)
	if err != nil {
		t.Fatalf("failed to insert test node: %v", err)
	}
}

func insertHybridTestEdge(t *testing.T, db *VectorGraphDB, sourceID, targetID string, edgeType EdgeType) {
	t.Helper()

	es := NewEdgeStore(db)
	edge := &GraphEdge{
		SourceID: sourceID,
		TargetID: targetID,
		EdgeType: edgeType,
		Weight:   1.0,
	}

	err := es.InsertEdge(edge)
	if err != nil {
		t.Fatalf("failed to insert test edge: %v", err)
	}
}

// =============================================================================
// Adapter Creation Tests
// =============================================================================

func TestNewVectorGraphDBHybridAdapter(t *testing.T) {
	db, cleanup := setupHybridTestDB(t)
	defer cleanup()

	hnsw := newHybridTestHNSWSearcher()

	t.Run("creates adapter with valid config", func(t *testing.T) {
		adapter := NewVectorGraphDBHybridAdapter(VectorGraphDBHybridAdapterConfig{
			DB:   db,
			HNSW: hnsw,
		})

		if adapter == nil {
			t.Fatal("expected adapter, got nil")
		}

		if !adapter.IsReady() {
			t.Error("expected adapter to be ready")
		}
	})

	t.Run("returns nil with nil DB", func(t *testing.T) {
		adapter := NewVectorGraphDBHybridAdapter(VectorGraphDBHybridAdapterConfig{
			DB:   nil,
			HNSW: hnsw,
		})

		if adapter != nil {
			t.Error("expected nil adapter with nil DB")
		}
	})

	t.Run("creates adapter with domain hint", func(t *testing.T) {
		domain := DomainCode
		adapter := NewVectorGraphDBHybridAdapter(VectorGraphDBHybridAdapterConfig{
			DB:         db,
			HNSW:       hnsw,
			DomainHint: &domain,
		})

		if adapter == nil {
			t.Fatal("expected adapter, got nil")
		}

		if adapter.GetDomainHint() == nil {
			t.Error("expected domain hint to be set")
		}

		if *adapter.GetDomainHint() != DomainCode {
			t.Errorf("expected domain hint %v, got %v", DomainCode, *adapter.GetDomainHint())
		}
	})

	t.Run("simple constructor works", func(t *testing.T) {
		adapter := NewVectorGraphDBHybridAdapterSimple(db, hnsw)

		if adapter == nil {
			t.Fatal("expected adapter, got nil")
		}

		if !adapter.IsReady() {
			t.Error("expected adapter to be ready")
		}
	})
}

// =============================================================================
// VectorSearchable Tests
// =============================================================================

func TestAdapter_Search(t *testing.T) {
	adapter, hnsw, cleanup := setupTestAdapter(t)
	defer cleanup()

	// Add test results to mock
	hnsw.addResult("node1", 0.95, DomainCode, NodeTypeFunction)
	hnsw.addResult("node2", 0.85, DomainCode, NodeTypeStruct)
	hnsw.addResult("node3", 0.75, DomainHistory, NodeTypeSession)

	t.Run("returns results for valid vector", func(t *testing.T) {
		vector := make([]float32, EmbeddingDimension)
		for i := range vector {
			vector[i] = 0.1
		}

		ids, distances, err := adapter.Search(vector, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ids) != 3 {
			t.Errorf("expected 3 results, got %d", len(ids))
		}

		if len(distances) != 3 {
			t.Errorf("expected 3 distances, got %d", len(distances))
		}

		// Check that distances are derived from similarity (1 - similarity)
		expectedDistance := float32(1.0 - 0.95)
		if distances[0] != expectedDistance {
			t.Errorf("expected distance %f, got %f", expectedDistance, distances[0])
		}
	})

	t.Run("returns error for empty vector", func(t *testing.T) {
		ids, _, err := adapter.Search([]float32{}, 10)
		if err == nil {
			t.Error("expected error for empty vector")
		}
		if ids != nil {
			t.Error("expected nil ids for error case")
		}
	})

	t.Run("uses default k when k <= 0", func(t *testing.T) {
		vector := make([]float32, EmbeddingDimension)
		ids, _, err := adapter.Search(vector, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should use default of 10
		if len(ids) > 10 {
			t.Errorf("expected at most 10 results, got %d", len(ids))
		}
	})

	t.Run("applies domain hint filter", func(t *testing.T) {
		domain := DomainCode
		adapter.SetDomainHint(&domain)

		vector := make([]float32, EmbeddingDimension)
		ids, _, err := adapter.Search(vector, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should only return Code domain results
		if len(ids) != 2 {
			t.Errorf("expected 2 Code domain results, got %d", len(ids))
		}

		// Reset domain hint
		adapter.SetDomainHint(nil)
	})
}

func TestAdapter_SearchVector(t *testing.T) {
	adapter, hnsw, cleanup := setupTestAdapter(t)
	defer cleanup()

	hnsw.addResult("node1", 0.95, DomainCode, NodeTypeFunction)
	hnsw.addResult("node2", 0.85, DomainCode, NodeTypeStruct)
	hnsw.addResult("node3", 0.75, DomainHistory, NodeTypeSession)

	ctx := context.Background()
	vector := make([]float32, EmbeddingDimension)

	t.Run("returns VectorSearchResult", func(t *testing.T) {
		result, err := adapter.SearchVector(ctx, vector, 10, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("expected result, got nil")
		}

		if len(result.Results) != 3 {
			t.Errorf("expected 3 results, got %d", len(result.Results))
		}

		// Check first match
		if result.Results[0].ID != "node1" {
			t.Errorf("expected first result ID 'node1', got '%s'", result.Results[0].ID)
		}

		if result.Results[0].Similarity != 0.95 {
			t.Errorf("expected similarity 0.95, got %f", result.Results[0].Similarity)
		}
	})

	t.Run("filters by domain in options", func(t *testing.T) {
		opts := &VectorSearchOptions{
			Domains: []Domain{DomainCode},
		}

		result, err := adapter.SearchVector(ctx, vector, 10, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Results) != 2 {
			t.Errorf("expected 2 Code domain results, got %d", len(result.Results))
		}
	})

	t.Run("filters by node type in options", func(t *testing.T) {
		opts := &VectorSearchOptions{
			NodeTypes: []NodeType{NodeTypeFunction},
		}

		result, err := adapter.SearchVector(ctx, vector, 10, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Results) != 1 {
			t.Errorf("expected 1 Function type result, got %d", len(result.Results))
		}
	})

	t.Run("filters by minimum similarity", func(t *testing.T) {
		opts := &VectorSearchOptions{
			MinSimilarity: 0.8,
		}

		result, err := adapter.SearchVector(ctx, vector, 10, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Results) != 2 {
			t.Errorf("expected 2 results with similarity >= 0.8, got %d", len(result.Results))
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := adapter.SearchVector(ctx, vector, 10, nil)
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("returns empty result for empty vector", func(t *testing.T) {
		result, err := adapter.SearchVector(ctx, []float32{}, 10, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Results) != 0 {
			t.Errorf("expected 0 results for empty vector, got %d", len(result.Results))
		}
	})
}

// =============================================================================
// GraphTraversable Tests
// =============================================================================

func TestAdapter_GetOutgoingEdges(t *testing.T) {
	adapter, _, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()

	// Insert test nodes and edges
	insertHybridTestNode(t, db, "source", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "target1", DomainCode, NodeTypeStruct)
	insertHybridTestNode(t, db, "target2", DomainCode, NodeTypeInterface)

	insertHybridTestEdge(t, db, "source", "target1", EdgeTypeCalls)
	insertHybridTestEdge(t, db, "source", "target2", EdgeTypeReturns)

	t.Run("returns all outgoing edges", func(t *testing.T) {
		edges, err := adapter.GetOutgoingEdges("source", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(edges) != 2 {
			t.Errorf("expected 2 outgoing edges, got %d", len(edges))
		}
	})

	t.Run("filters by edge type", func(t *testing.T) {
		edges, err := adapter.GetOutgoingEdges("source", []string{"calls"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(edges) != 1 {
			t.Errorf("expected 1 'calls' edge, got %d", len(edges))
		}

		if edges[0].EdgeType != "calls" {
			t.Errorf("expected edge type 'calls', got '%s'", edges[0].EdgeType)
		}
	})

	t.Run("returns empty for node without edges", func(t *testing.T) {
		edges, err := adapter.GetOutgoingEdges("target1", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(edges) != 0 {
			t.Errorf("expected 0 edges, got %d", len(edges))
		}
	})
}

func TestAdapter_GetIncomingEdges(t *testing.T) {
	adapter, _, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()

	// Insert test nodes and edges
	insertHybridTestNode(t, db, "source1", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "source2", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "target", DomainCode, NodeTypeStruct)

	insertHybridTestEdge(t, db, "source1", "target", EdgeTypeCalls)
	insertHybridTestEdge(t, db, "source2", "target", EdgeTypeReferences)

	t.Run("returns all incoming edges", func(t *testing.T) {
		edges, err := adapter.GetIncomingEdges("target", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(edges) != 2 {
			t.Errorf("expected 2 incoming edges, got %d", len(edges))
		}
	})

	t.Run("filters by edge type", func(t *testing.T) {
		edges, err := adapter.GetIncomingEdges("target", []string{"references"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(edges) != 1 {
			t.Errorf("expected 1 'references' edge, got %d", len(edges))
		}
	})
}

func TestAdapter_GetNodesByPattern(t *testing.T) {
	adapter, _, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()

	// Insert test nodes
	insertHybridTestNode(t, db, "func1", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "func2", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "struct1", DomainCode, NodeTypeStruct)

	t.Run("matches by entity type", func(t *testing.T) {
		entityType := "function"
		ids, err := adapter.GetNodesByPattern(&NodePattern{
			EntityType: &entityType,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ids) != 2 {
			t.Errorf("expected 2 function nodes, got %d", len(ids))
		}
	})

	t.Run("returns nil for nil pattern", func(t *testing.T) {
		ids, err := adapter.GetNodesByPattern(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if ids != nil {
			t.Errorf("expected nil for nil pattern, got %v", ids)
		}
	})

	t.Run("returns nil for empty pattern", func(t *testing.T) {
		ids, err := adapter.GetNodesByPattern(&NodePattern{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if ids != nil {
			t.Errorf("expected nil for empty pattern, got %v", ids)
		}
	})
}

// =============================================================================
// EntityRetrievable Tests
// =============================================================================

func TestAdapter_GetEntity(t *testing.T) {
	adapter, _, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()
	insertHybridTestNode(t, db, "test-entity", DomainCode, NodeTypeFunction)

	t.Run("retrieves existing entity", func(t *testing.T) {
		entity, err := adapter.GetEntity("test-entity")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if entity == nil {
			t.Fatal("expected HybridEntity, got nil")
		}

		if entity.ID != "test-entity" {
			t.Errorf("expected ID 'test-entity', got '%s'", entity.ID)
		}

		if entity.Domain != DomainCode {
			t.Errorf("expected domain Code, got %v", entity.Domain)
		}

		if entity.NodeType != NodeTypeFunction {
			t.Errorf("expected node type Function, got %v", entity.NodeType)
		}
	})

	t.Run("returns error for non-existent entity", func(t *testing.T) {
		_, err := adapter.GetEntity("non-existent")
		if err == nil {
			t.Error("expected error for non-existent entity")
		}
	})
}

func TestAdapter_GetEntities(t *testing.T) {
	adapter, _, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()
	insertHybridTestNode(t, db, "entity1", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "entity2", DomainCode, NodeTypeStruct)
	insertHybridTestNode(t, db, "entity3", DomainHistory, NodeTypeSession)

	t.Run("retrieves multiple entities", func(t *testing.T) {
		entities, err := adapter.GetEntities([]string{"entity1", "entity2", "entity3"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(entities) != 3 {
			t.Errorf("expected 3 entities, got %d", len(entities))
		}
	})

	t.Run("skips non-existent entities", func(t *testing.T) {
		entities, err := adapter.GetEntities([]string{"entity1", "non-existent", "entity2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(entities) != 2 {
			t.Errorf("expected 2 entities (skipping non-existent), got %d", len(entities))
		}
	})

	t.Run("returns empty for empty input", func(t *testing.T) {
		entities, err := adapter.GetEntities([]string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(entities) != 0 {
			t.Errorf("expected 0 entities, got %d", len(entities))
		}
	})
}

// =============================================================================
// Graph Traversal Tests
// =============================================================================

func TestAdapter_TraverseGraph(t *testing.T) {
	adapter, _, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()

	// Create a small graph
	insertHybridTestNode(t, db, "root", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "child1", DomainCode, NodeTypeStruct)
	insertHybridTestNode(t, db, "child2", DomainCode, NodeTypeInterface)
	insertHybridTestNode(t, db, "grandchild", DomainCode, NodeTypeMethod)

	insertHybridTestEdge(t, db, "root", "child1", EdgeTypeCalls)
	insertHybridTestEdge(t, db, "root", "child2", EdgeTypeReturns)
	insertHybridTestEdge(t, db, "child1", "grandchild", EdgeTypeHasMethod)

	ctx := context.Background()

	t.Run("traverses graph with default options", func(t *testing.T) {
		result, err := adapter.TraverseGraph(ctx, "root", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("expected result, got nil")
		}

		if len(result.Nodes) < 3 {
			t.Errorf("expected at least 3 nodes in traversal, got %d", len(result.Nodes))
		}

		if len(result.Edges) < 2 {
			t.Errorf("expected at least 2 edges in traversal, got %d", len(result.Edges))
		}
	})

	t.Run("respects max depth", func(t *testing.T) {
		result, err := adapter.TraverseGraph(ctx, "root", &TraverseOptions{
			MaxDepth: 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// With depth 1, should get root + direct children only
		if len(result.Nodes) > 3 {
			t.Errorf("expected at most 3 nodes with depth 1, got %d", len(result.Nodes))
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := adapter.TraverseGraph(ctx, "root", nil)
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

// =============================================================================
// Hybrid Query Tests
// =============================================================================

func TestAdapter_ExecuteHybridQuery(t *testing.T) {
	adapter, hnsw, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()

	// Insert test nodes
	insertHybridTestNode(t, db, "vec-node1", DomainCode, NodeTypeFunction)
	insertHybridTestNode(t, db, "vec-node2", DomainCode, NodeTypeStruct)
	insertHybridTestNode(t, db, "graph-node", DomainCode, NodeTypeMethod)

	insertHybridTestEdge(t, db, "vec-node1", "graph-node", EdgeTypeCalls)

	// Add vector search results
	hnsw.addResult("vec-node1", 0.9, DomainCode, NodeTypeFunction)
	hnsw.addResult("vec-node2", 0.8, DomainCode, NodeTypeStruct)

	ctx := context.Background()

	t.Run("executes vector-only query", func(t *testing.T) {
		vector := make([]float32, EmbeddingDimension)
		response, err := adapter.ExecuteHybridQuery(ctx, &HybridQueryRequest{
			Vector:       vector,
			VectorWeight: 1.0,
			Limit:        10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(response.Results) != 2 {
			t.Errorf("expected 2 results, got %d", len(response.Results))
		}
	})

	t.Run("executes graph-only query", func(t *testing.T) {
		response, err := adapter.ExecuteHybridQuery(ctx, &HybridQueryRequest{
			StartNodeID: "vec-node1",
			GraphWeight: 1.0,
			GraphDepth:  2,
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(response.Results) == 0 {
			t.Error("expected at least 1 result from graph traversal")
		}
	})

	t.Run("executes hybrid query combining vector and graph", func(t *testing.T) {
		vector := make([]float32, EmbeddingDimension)
		response, err := adapter.ExecuteHybridQuery(ctx, &HybridQueryRequest{
			Vector:       vector,
			StartNodeID:  "vec-node1",
			VectorWeight: 0.5,
			GraphWeight:  0.5,
			GraphDepth:   2,
			Limit:        10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(response.Results) < 2 {
			t.Errorf("expected at least 2 results from hybrid query, got %d", len(response.Results))
		}

		// Results should be sorted by combined score
		for i := 1; i < len(response.Results); i++ {
			if response.Results[i].CombinedScore > response.Results[i-1].CombinedScore {
				t.Error("results not sorted by combined score descending")
			}
		}
	})

	t.Run("returns empty response for nil query", func(t *testing.T) {
		response, err := adapter.ExecuteHybridQuery(ctx, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(response.Results) != 0 {
			t.Errorf("expected 0 results for nil query, got %d", len(response.Results))
		}
	})

	t.Run("respects result limit", func(t *testing.T) {
		vector := make([]float32, EmbeddingDimension)
		response, err := adapter.ExecuteHybridQuery(ctx, &HybridQueryRequest{
			Vector:       vector,
			VectorWeight: 1.0,
			Limit:        1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(response.Results) > 1 {
			t.Errorf("expected at most 1 result, got %d", len(response.Results))
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		vector := make([]float32, EmbeddingDimension)
		_, err := adapter.ExecuteHybridQuery(ctx, &HybridQueryRequest{
			Vector: vector,
		})
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

// =============================================================================
// Configuration Tests
// =============================================================================

func TestAdapter_Configuration(t *testing.T) {
	adapter, _, cleanup := setupTestAdapter(t)
	defer cleanup()

	t.Run("SetDomainHint and GetDomainHint", func(t *testing.T) {
		domain := DomainHistory
		adapter.SetDomainHint(&domain)

		hint := adapter.GetDomainHint()
		if hint == nil {
			t.Fatal("expected domain hint, got nil")
		}

		if *hint != DomainHistory {
			t.Errorf("expected domain %v, got %v", DomainHistory, *hint)
		}

		// Clear hint
		adapter.SetDomainHint(nil)
		if adapter.GetDomainHint() != nil {
			t.Error("expected nil domain hint after clearing")
		}
	})

	t.Run("SetHNSW", func(t *testing.T) {
		newHNSW := newHybridTestHNSWSearcher()
		newHNSW.addResult("new-node", 0.99, DomainCode, NodeTypeFunction)

		adapter.SetHNSW(newHNSW)

		// Verify new HNSW is used
		vector := make([]float32, EmbeddingDimension)
		ids, _, err := adapter.Search(vector, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ids) != 1 || ids[0] != "new-node" {
			t.Error("new HNSW not being used")
		}
	})

	t.Run("IsReady", func(t *testing.T) {
		if !adapter.IsReady() {
			t.Error("expected adapter to be ready")
		}

		// Create adapter without HNSW
		db := adapter.DB()
		adapterNoHNSW := NewVectorGraphDBHybridAdapter(VectorGraphDBHybridAdapterConfig{
			DB: db,
		})

		if adapterNoHNSW.IsReady() {
			t.Error("expected adapter without HNSW to not be ready")
		}
	})
}

// =============================================================================
// NodePattern Tests
// =============================================================================

func TestNodePattern_Matches(t *testing.T) {
	t.Run("returns false for nil pattern", func(t *testing.T) {
		var pattern *NodePattern
		if pattern.Matches() {
			t.Error("expected false for nil pattern")
		}
	})

	t.Run("returns false for empty pattern", func(t *testing.T) {
		pattern := &NodePattern{}
		if pattern.Matches() {
			t.Error("expected false for empty pattern")
		}
	})

	t.Run("returns true when entity type set", func(t *testing.T) {
		entityType := "function"
		pattern := &NodePattern{EntityType: &entityType}
		if !pattern.Matches() {
			t.Error("expected true when entity type set")
		}
	})

	t.Run("returns true when name pattern set", func(t *testing.T) {
		pattern := &NodePattern{NamePattern: "test*"}
		if !pattern.Matches() {
			t.Error("expected true when name pattern set")
		}
	})

	t.Run("returns true when properties set", func(t *testing.T) {
		pattern := &NodePattern{Properties: map[string]any{"key": "value"}}
		if !pattern.Matches() {
			t.Error("expected true when properties set")
		}
	})
}

// =============================================================================
// Wildcard Matching Tests
// =============================================================================

func TestMatchWildcard(t *testing.T) {
	tests := []struct {
		pattern  string
		text     string
		expected bool
	}{
		{"*", "anything", true},
		{"test*", "testing", true},
		{"test*", "test", true},
		{"test*", "tes", false},
		{"*test", "mytest", true},
		{"*test", "test", true},
		{"*test", "testing", false},
		{"exact", "exact", true},
		{"exact", "Exact", false},
		{"", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.pattern+"_"+tc.text, func(t *testing.T) {
			result := matchWildcard(tc.pattern, tc.text)
			if result != tc.expected {
				t.Errorf("matchWildcard(%q, %q) = %v, expected %v",
					tc.pattern, tc.text, result, tc.expected)
			}
		})
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestAdapter_ConcurrentAccess(t *testing.T) {
	adapter, hnsw, cleanup := setupTestAdapter(t)
	defer cleanup()

	db := adapter.DB()
	insertHybridTestNode(t, db, "concurrent-test", DomainCode, NodeTypeFunction)
	hnsw.addResult("concurrent-test", 0.9, DomainCode, NodeTypeFunction)

	ctx := context.Background()
	vector := make([]float32, EmbeddingDimension)

	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer func() { done <- true }()

			for j := 0; j < 100; j++ {
				// Perform various operations
				_, _, _ = adapter.Search(vector, 5)
				_, _ = adapter.SearchVector(ctx, vector, 5, nil)
				_, _ = adapter.GetEntity("concurrent-test")
				_, _ = adapter.GetOutgoingEdges("concurrent-test", nil)

				// Test configuration changes
				domain := DomainCode
				adapter.SetDomainHint(&domain)
				_ = adapter.GetDomainHint()
				adapter.SetDomainHint(nil)
			}
		}()
	}

	// Wait for all goroutines with timeout
	timeout := time.After(10 * time.Second)
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
			// Continue
		case <-timeout:
			t.Fatal("test timed out - possible deadlock")
		}
	}
}
