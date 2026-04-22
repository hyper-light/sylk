package vectorgraphdb

import (
	"sync"
	"testing"
)

type mockVectorIndexSearcher struct {
	mu      sync.Mutex
	vectors map[string][]float32
	results []VectorIndexSearchResult
}

func newMockVectorIndexSearcher() *mockVectorIndexSearcher {
	return &mockVectorIndexSearcher{
		vectors: make(map[string][]float32),
	}
}

func (m *mockVectorIndexSearcher) Search(query []float32, k int, filter *VectorIndexSearchFilter) []VectorIndexSearchResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.results) > k {
		return m.results[:k]
	}
	return m.results
}

func (m *mockVectorIndexSearcher) GetVector(id string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if vec, ok := m.vectors[id]; ok {
		return vec, nil
	}
	return nil, ErrNodeNotFound
}

func (m *mockVectorIndexSearcher) addVector(id string, vec []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vectors[id] = vec
}

func (m *mockVectorIndexSearcher) setResults(results []VectorIndexSearchResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = results
}

func TestVectorSearcherSearch(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{
		ID:       "node2",
		Domain:   DomainCode,
		NodeType: NodeTypeFunction,
	}, []float32{0.9, 0.2, 0.1})

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{
		{ID: "node1", Similarity: 0.95, Domain: DomainCode, NodeType: NodeTypeFile},
		{ID: "node2", Similarity: 0.85, Domain: DomainCode, NodeType: NodeTypeFunction},
	})

	vs := NewVectorSearcher(db, mock)
	results, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Got %d results, want 2", len(results))
	}
}

func TestVectorSearcherSearchWithMinSimilarity(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{
		ID:       "node2",
		Domain:   DomainCode,
		NodeType: NodeTypeFunction,
	}, []float32{0.5, 0.5, 0.5})

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{
		{ID: "node1", Similarity: 0.95},
		{ID: "node2", Similarity: 0.4},
	})

	vs := NewVectorSearcher(db, mock)
	results, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{
		MinSimilarity: 0.5,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Got %d results, want 1", len(results))
	}
}

func TestVectorSearcherSearchByDomain(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{
		ID:       "code1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}, []float32{1, 0.1, 0.1})

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{
		{ID: "code1", Similarity: 0.9, Domain: DomainCode},
	})

	vs := NewVectorSearcher(db, mock)
	results, err := vs.SearchByDomain([]float32{1, 0, 0}, DomainCode, 10)
	if err != nil {
		t.Fatalf("SearchByDomain: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Got %d results, want 1", len(results))
	}
	if results[0].Node.Domain != DomainCode {
		t.Errorf("Wrong domain: %s", results[0].Node.Domain)
	}
}

func TestVectorSearcherSearchByNodeType(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{
		ID:       "func1",
		Domain:   DomainCode,
		NodeType: NodeTypeFunction,
	}, []float32{1, 0.1, 0.1})

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{
		{ID: "func1", Similarity: 0.88, NodeType: NodeTypeFunction},
	})

	vs := NewVectorSearcher(db, mock)
	results, err := vs.SearchByNodeType([]float32{1, 0, 0}, []NodeType{NodeTypeFunction}, 10)
	if err != nil {
		t.Fatalf("SearchByNodeType: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Got %d results, want 1", len(results))
	}
}

func TestVectorSearcherSearchMultiDomain(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{ID: "code1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "hist1", Domain: DomainHistory, NodeType: NodeTypeSession}, []float32{0.8, 0.2, 0.1})

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{
		{ID: "code1", Similarity: 0.9, Domain: DomainCode},
		{ID: "hist1", Similarity: 0.7, Domain: DomainHistory},
	})

	vs := NewVectorSearcher(db, mock)
	limits := map[Domain]int{
		DomainCode:    5,
		DomainHistory: 5,
	}

	results, err := vs.SearchMultiDomain([]float32{1, 0, 0}, limits)
	if err != nil {
		t.Fatalf("SearchMultiDomain: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Got %d domain results, want 2", len(results))
	}
}

func TestVectorSearcherFindSimilar(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{ID: "node1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "node2", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})

	mock := newMockVectorIndexSearcher()
	mock.addVector("node1", []float32{1, 0.1, 0.1})
	mock.setResults([]VectorIndexSearchResult{
		{ID: "node1", Similarity: 1.0},
		{ID: "node2", Similarity: 0.9},
	})

	vs := NewVectorSearcher(db, mock)
	results, err := vs.FindSimilar("node1", 5)
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}

	for _, r := range results {
		if r.Node.ID == "node1" {
			t.Error("FindSimilar should exclude the source node")
		}
	}
}

func TestVectorSearcherSearchEmptyResults(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{})

	vs := NewVectorSearcher(db, mock)
	results, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Got %d results, want 0", len(results))
	}
}

func TestVectorSearcherSearchDefaultOptions(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	ns.InsertNode(&GraphNode{ID: "node1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{{ID: "node1", Similarity: 0.9}})

	vs := NewVectorSearcher(db, mock)
	results, err := vs.Search([]float32{1, 0, 0}, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Got %d results, want 1", len(results))
	}
}

func TestVectorSearcherConcurrent(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	for i := range 10 {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{
		{ID: nodeID(0), Similarity: 0.9},
		{ID: nodeID(1), Similarity: 0.8},
	})

	vs := NewVectorSearcher(db, mock)

	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{Limit: 5})
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("Concurrent search error: %v", err)
	}
}

func TestVectorSearcherFindSimilarNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	mock := newMockVectorIndexSearcher()
	vs := NewVectorSearcher(db, mock)

	_, err := vs.FindSimilar("nonexistent", 5)
	if err == nil {
		t.Error("FindSimilar should fail for nonexistent node")
	}
}

func TestVectorSearcherBatchLoading(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	// Insert many nodes to test batch loading
	nodeCount := 50
	for i := range nodeCount {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i%10) + 0.1, 0.1, 0.1})
	}

	// Setup mock with results for all nodes
	mock := newMockVectorIndexSearcher()
	results := make([]VectorIndexSearchResult, nodeCount)
	for i := range nodeCount {
		results[i] = VectorIndexSearchResult{
			ID:         nodeID(i),
			Similarity: float64(nodeCount-i) / float64(nodeCount),
			Domain:     DomainCode,
			NodeType:   NodeTypeFile,
		}
	}
	mock.setResults(results)

	vs := NewVectorSearcher(db, mock)

	// Search should use batch loading internally
	searchResults, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{Limit: nodeCount})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Verify we got all results via batch loading
	if len(searchResults) != nodeCount {
		t.Errorf("Got %d results, want %d", len(searchResults), nodeCount)
	}

	// Verify all results have valid nodes
	for _, r := range searchResults {
		if r.Node == nil {
			t.Error("Result node should not be nil")
		}
		if r.Node.ID == "" {
			t.Error("Result node ID should not be empty")
		}
	}
}

func TestVectorSearcherBatchLoadingWithMinSimilarity(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	// Insert nodes
	for i := range 10 {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	// Setup mock with varying similarity scores
	mock := newMockVectorIndexSearcher()
	results := []VectorIndexSearchResult{
		{ID: nodeID(0), Similarity: 0.9},
		{ID: nodeID(1), Similarity: 0.8},
		{ID: nodeID(2), Similarity: 0.6},
		{ID: nodeID(3), Similarity: 0.4}, // Below threshold
		{ID: nodeID(4), Similarity: 0.3}, // Below threshold
	}
	mock.setResults(results)

	vs := NewVectorSearcher(db, mock)

	// Search with min similarity filter
	searchResults, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{
		MinSimilarity: 0.5,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Should only return 3 results (those with similarity >= 0.5)
	if len(searchResults) != 3 {
		t.Errorf("Got %d results, want 3", len(searchResults))
	}

	// Verify all results meet minimum similarity
	for _, r := range searchResults {
		if r.Similarity < 0.5 {
			t.Errorf("Result %s has similarity %f, want >= 0.5", r.Node.ID, r.Similarity)
		}
	}
}

func TestVectorSearcherBatchLoadingPreservesOrder(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	// Insert nodes
	for i := range 5 {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	// Setup mock with specific order
	mock := newMockVectorIndexSearcher()
	results := []VectorIndexSearchResult{
		{ID: nodeID(2), Similarity: 0.95},
		{ID: nodeID(0), Similarity: 0.9},
		{ID: nodeID(4), Similarity: 0.85},
		{ID: nodeID(1), Similarity: 0.8},
		{ID: nodeID(3), Similarity: 0.75},
	}
	mock.setResults(results)

	vs := NewVectorSearcher(db, mock)

	searchResults, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Verify order is preserved from vector index results
	expectedOrder := []string{nodeID(2), nodeID(0), nodeID(4), nodeID(1), nodeID(3)}
	for i, r := range searchResults {
		if r.Node.ID != expectedOrder[i] {
			t.Errorf("Result %d: got %s, want %s", i, r.Node.ID, expectedOrder[i])
		}
	}
}

// =============================================================================
// W2 KG-30 / KG-31 / KG-32 / KG-33
// =============================================================================

type stubConflictLookup struct{ byNode map[string][]string }

func (s *stubConflictLookup) ActiveForNode(nodeID string) []string {
	if s == nil {
		return nil
	}
	return s.byNode[nodeID]
}

type stubProvenanceLookup struct{ byNode map[string][]ProvenanceHop }

func (s *stubProvenanceLookup) ChainForNode(nodeID string, _ int) []ProvenanceHop {
	if s == nil {
		return nil
	}
	return s.byNode[nodeID]
}

func setupRankableDB(t *testing.T) (*VectorGraphDB, *mockVectorIndexSearcher, func()) {
	t.Helper()
	db, path := setupTestDB(t)
	cleanup := func() { cleanupDB(db, path) }

	ns := NewNodeStore(db, nil)
	// High-trust node.
	if err := ns.InsertNode(&GraphNode{
		ID:         "ground-1",
		Domain:     DomainCode,
		NodeType:   NodeTypeFile,
		TrustLevel: TrustLevelGround,
	}, []float32{1, 0.1, 0.1}); err != nil {
		t.Fatalf("insert ground-1: %v", err)
	}
	// Low-trust node with slightly higher raw similarity.
	if err := ns.InsertNode(&GraphNode{
		ID:         "llm-2",
		Domain:     DomainCode,
		NodeType:   NodeTypeFunction,
		TrustLevel: TrustLevelLLM,
	}, []float32{0.9, 0.2, 0.1}); err != nil {
		t.Fatalf("insert llm-2: %v", err)
	}
	// Off-domain node for KG-33.
	if err := ns.InsertNode(&GraphNode{
		ID:         "hist-3",
		Domain:     DomainHistory,
		NodeType:   NodeTypeHistoryEntry,
		TrustLevel: TrustLevelStandard,
	}, []float32{0.8, 0.3, 0.1}); err != nil {
		t.Fatalf("insert hist-3: %v", err)
	}

	mock := newMockVectorIndexSearcher()
	mock.setResults([]VectorIndexSearchResult{
		{ID: "llm-2", Similarity: 0.91, Domain: DomainCode, NodeType: NodeTypeFunction},
		{ID: "ground-1", Similarity: 0.90, Domain: DomainCode, NodeType: NodeTypeFile},
		{ID: "hist-3", Similarity: 0.85, Domain: DomainHistory, NodeType: NodeTypeHistoryEntry},
	})
	return db, mock, cleanup
}

// TestVectorSearcher_TrustBoostReordersResults closes KG-30.
func TestVectorSearcher_TrustBoostReordersResults(t *testing.T) {
	db, mock, cleanup := setupRankableDB(t)
	defer cleanup()

	vs := NewVectorSearcher(db, mock)
	// Without boost: llm-2 (0.91) ranks above ground-1 (0.90).
	plain, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{Limit: 3})
	if err != nil {
		t.Fatalf("plain Search: %v", err)
	}
	if plain[0].Node.ID != "llm-2" {
		t.Fatalf("baseline: want llm-2 first, got %s", plain[0].Node.ID)
	}
	for _, r := range plain {
		if r.Score != r.Similarity {
			t.Fatalf("no boost: Score must equal Similarity, got %v != %v", r.Score, r.Similarity)
		}
	}

	// With boost: ground-1's trust pulls it ahead.
	boosted, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{Limit: 3, TrustBoost: 0.5})
	if err != nil {
		t.Fatalf("boosted Search: %v", err)
	}
	if boosted[0].Node.ID != "ground-1" {
		t.Fatalf("boosted: want ground-1 first, got %s", boosted[0].Node.ID)
	}
	if boosted[0].Score <= boosted[1].Score {
		t.Fatalf("top-1 Score must exceed top-2: %v vs %v", boosted[0].Score, boosted[1].Score)
	}
}

// TestVectorSearcher_HomeDomainFilter closes KG-33.
func TestVectorSearcher_HomeDomainFilter(t *testing.T) {
	db, mock, cleanup := setupRankableDB(t)
	defer cleanup()

	vs := NewVectorSearcher(db, mock)
	results, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{
		Limit:       10,
		HomeDomains: []Domain{DomainCode},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Node.Domain != DomainCode {
			t.Fatalf("HomeDomains filter should exclude %s, got result %s", r.Node.Domain, r.Node.ID)
		}
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 code results, got %d", len(results))
	}
}

// TestVectorSearcher_SurfaceConflicts closes KG-32.
func TestVectorSearcher_SurfaceConflicts(t *testing.T) {
	db, mock, cleanup := setupRankableDB(t)
	defer cleanup()

	lookup := &stubConflictLookup{byNode: map[string][]string{
		"ground-1": {"conflict-A", "conflict-B"},
	}}
	vs := NewVectorSearcher(db, mock)
	vs.SetConflictLookup(lookup)

	results, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{
		Limit:            10,
		SurfaceConflicts: true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var got *SearchResult
	for i := range results {
		if results[i].Node.ID == "ground-1" {
			got = &results[i]
			break
		}
	}
	if got == nil {
		t.Fatal("ground-1 missing from results")
	}
	if len(got.Conflicts) != 2 {
		t.Fatalf("want 2 conflict ids, got %v", got.Conflicts)
	}
}

// TestVectorSearcher_AttachProvenance closes KG-31.
func TestVectorSearcher_AttachProvenance(t *testing.T) {
	db, mock, cleanup := setupRankableDB(t)
	defer cleanup()

	chain := []ProvenanceHop{
		{SourceType: SourceTypeCode, SourceID: "repo-a/file.go", Confidence: 0.95},
		{SourceType: SourceTypeHistory, SourceID: "commit-abc", Confidence: 0.80},
	}
	lookup := &stubProvenanceLookup{byNode: map[string][]ProvenanceHop{"ground-1": chain}}

	vs := NewVectorSearcher(db, mock)
	vs.SetProvenanceLookup(lookup)

	results, err := vs.Search([]float32{1, 0, 0}, &SearchOptions{
		Limit:            10,
		AttachProvenance: true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Node.ID != "ground-1" {
			continue
		}
		if len(r.Provenance) != 2 {
			t.Fatalf("want 2 provenance hops, got %d", len(r.Provenance))
		}
		if r.Provenance[0].SourceID != "repo-a/file.go" {
			t.Fatalf("unexpected first hop: %+v", r.Provenance[0])
		}
		return
	}
	t.Fatal("ground-1 not present in results")
}
