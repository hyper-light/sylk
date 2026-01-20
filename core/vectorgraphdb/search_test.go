package vectorgraphdb

import (
	"sync"
	"testing"
)

type mockHNSWSearcher struct {
	mu      sync.Mutex
	vectors map[string][]float32
	results []HNSWSearchResult
}

func newMockHNSWSearcher() *mockHNSWSearcher {
	return &mockHNSWSearcher{
		vectors: make(map[string][]float32),
	}
}

func (m *mockHNSWSearcher) Search(query []float32, k int, filter *HNSWSearchFilter) []HNSWSearchResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.results) > k {
		return m.results[:k]
	}
	return m.results
}

func (m *mockHNSWSearcher) GetVector(id string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if vec, ok := m.vectors[id]; ok {
		return vec, nil
	}
	return nil, ErrNodeNotFound
}

func (m *mockHNSWSearcher) addVector(id string, vec []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vectors[id] = vec
}

func (m *mockHNSWSearcher) setResults(results []HNSWSearchResult) {
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

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
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

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
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

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
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

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
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

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
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

	mock := newMockHNSWSearcher()
	mock.addVector("node1", []float32{1, 0.1, 0.1})
	mock.setResults([]HNSWSearchResult{
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

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{})

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

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{{ID: "node1", Similarity: 0.9}})

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
	for i := 0; i < 10; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: nodeID(0), Similarity: 0.9},
		{ID: nodeID(1), Similarity: 0.8},
	})

	vs := NewVectorSearcher(db, mock)

	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	for i := 0; i < 10; i++ {
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

	mock := newMockHNSWSearcher()
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
	for i := 0; i < nodeCount; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i%10) + 0.1, 0.1, 0.1})
	}

	// Setup mock with results for all nodes
	mock := newMockHNSWSearcher()
	results := make([]HNSWSearchResult, nodeCount)
	for i := 0; i < nodeCount; i++ {
		results[i] = HNSWSearchResult{
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
	for i := 0; i < 10; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	// Setup mock with varying similarity scores
	mock := newMockHNSWSearcher()
	results := []HNSWSearchResult{
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
	for i := 0; i < 5; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i + 1), 0.1, 0.1})
	}

	// Setup mock with specific order
	mock := newMockHNSWSearcher()
	results := []HNSWSearchResult{
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

	// Verify order is preserved from HNSW results
	expectedOrder := []string{nodeID(2), nodeID(0), nodeID(4), nodeID(1), nodeID(3)}
	for i, r := range searchResults {
		if r.Node.ID != expectedOrder[i] {
			t.Errorf("Result %d: got %s, want %s", i, r.Node.ID, expectedOrder[i])
		}
	}
}
