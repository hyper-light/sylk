package vectorgraphdb

import (
	"testing"
)

func TestQueryEngineHybridQuery(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.5, 0.5, 0.5})

	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 0.95},
		{ID: "B", Similarity: 0.8},
		{ID: "C", Similarity: 0.4},
	})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.HybridQuery([]float32{1, 0, 0}, []string{"A"}, nil)
	if err != nil {
		t.Fatalf("HybridQuery: %v", err)
	}

	if len(results) < 2 {
		t.Errorf("Got %d results, want at least 2", len(results))
	}

	if results[0].CombinedScore == 0 {
		t.Error("CombinedScore should not be 0")
	}
}

func TestQueryEngineHybridQueryWithOptions(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 0.9},
		{ID: "B", Similarity: 0.3},
	})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.HybridQuery([]float32{1, 0, 0}, []string{"A"}, &HybridQueryOptions{
		VectorWeight: 0.8,
		GraphWeight:  0.2,
		MinCombined:  0.5,
	})
	if err != nil {
		t.Fatalf("HybridQuery: %v", err)
	}

	for _, r := range results {
		if r.CombinedScore < 0.5 {
			t.Errorf("Result with score %f should be filtered", r.CombinedScore)
		}
	}
}

func TestQueryEngineQueryByContext(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})

	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 0.9},
		{ID: "B", Similarity: 0.85},
	})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.QueryByContext([]float32{1, 0, 0}, []string{"A"}, 5)
	if err != nil {
		t.Fatalf("QueryByContext: %v", err)
	}

	if len(results) > 5 {
		t.Errorf("Got %d results, want max 5", len(results))
	}
}

func TestQueryEngineSemanticExpand(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})

	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})

	mock := newMockHNSWSearcher()
	mock.addVector("A", []float32{1, 0.1, 0.1})
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 1.0},
		{ID: "B", Similarity: 0.9},
	})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.SemanticExpand([]string{"A"}, 10)
	if err != nil {
		t.Fatalf("SemanticExpand: %v", err)
	}

	if len(results) == 0 {
		t.Error("SemanticExpand should return results")
	}
}

func TestQueryEngineSemanticExpandEmpty(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.SemanticExpand([]string{}, 10)
	if err != nil {
		t.Fatalf("SemanticExpand: %v", err)
	}

	if results != nil && len(results) > 0 {
		t.Error("SemanticExpand with empty seeds should return nil")
	}
}

func TestQueryEngineRelatedInDomain(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})

	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})

	mock := newMockHNSWSearcher()
	mock.addVector("A", []float32{1, 0.1, 0.1})
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 1.0, Domain: DomainCode},
		{ID: "B", Similarity: 0.9, Domain: DomainCode},
	})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.RelatedInDomain("A", 10)
	if err != nil {
		t.Fatalf("RelatedInDomain: %v", err)
	}

	for _, r := range results {
		if r.Node.Domain != DomainCode {
			t.Errorf("Result in wrong domain: %s", r.Node.Domain)
		}
	}
}

func TestQueryEngineCrossDomainQuery(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	ns.InsertNode(&GraphNode{ID: "code1", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "hist1", Domain: DomainHistory, NodeType: NodeTypeSession}, []float32{0.9, 0.2, 0.1})

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: "code1", Similarity: 0.9, Domain: DomainCode},
		{ID: "hist1", Similarity: 0.8, Domain: DomainHistory},
	})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.CrossDomainQuery(
		[]float32{1, 0, 0},
		DomainCode,
		[]Domain{DomainHistory, DomainAcademic},
		5,
	)
	if err != nil {
		t.Fatalf("CrossDomainQuery: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Got %d domain results, want 2", len(results))
	}
}

func TestQueryEngineResultOrdering(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.5, 0.5, 0.5})
	ns.InsertNode(&GraphNode{ID: "C", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.3, 0.3, 0.9})

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 0.9},
		{ID: "B", Similarity: 0.5},
		{ID: "C", Similarity: 0.3},
	})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.HybridQuery([]float32{1, 0, 0}, []string{"A"}, nil)
	if err != nil {
		t.Fatalf("HybridQuery: %v", err)
	}

	for i := 1; i < len(results); i++ {
		if results[i].CombinedScore > results[i-1].CombinedScore {
			t.Error("Results should be sorted by CombinedScore descending")
		}
	}
}

func TestQueryEngineEmptyVectorSearch(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.HybridQuery([]float32{1, 0, 0}, []string{}, nil)
	if err != nil {
		t.Fatalf("HybridQuery: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Got %d results, want 0", len(results))
	}
}

func TestQueryEngineDefaultWeights(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{{ID: "A", Similarity: 0.9}})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.HybridQuery([]float32{1, 0, 0}, []string{"A"}, &HybridQueryOptions{})
	if err != nil {
		t.Fatalf("HybridQuery: %v", err)
	}

	if len(results) == 0 {
		t.Error("HybridQuery with default weights should return results")
	}
}

func TestQueryEngineHybridScoring(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	ns.InsertNode(&GraphNode{ID: "A", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{1, 0.1, 0.1})
	ns.InsertNode(&GraphNode{ID: "B", Domain: DomainCode, NodeType: NodeTypeFile}, []float32{0.9, 0.2, 0.1})

	es.InsertEdge(&GraphEdge{SourceID: "A", TargetID: "B", EdgeType: EdgeTypeCalls})

	mock := newMockHNSWSearcher()
	mock.setResults([]HNSWSearchResult{
		{ID: "A", Similarity: 1.0},
		{ID: "B", Similarity: 0.5},
	})
	mock.addVector("A", []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	results, err := qe.HybridQuery([]float32{1, 0, 0}, []string{"A"}, &HybridQueryOptions{
		VectorWeight: 0.5,
		GraphWeight:  0.5,
	})
	if err != nil {
		t.Fatalf("HybridQuery: %v", err)
	}

	for _, r := range results {
		if r.VectorScore == 0 && r.GraphScore == 0 && r.CombinedScore != 0 {
			t.Error("CombinedScore should be derived from VectorScore and GraphScore")
		}
	}
}

func TestQueryEngineBatchLoading(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)
	es := NewEdgeStore(db)

	// Create many nodes to verify batch loading handles multiple nodes
	nodeCount := 50
	for i := 0; i < nodeCount; i++ {
		ns.InsertNode(&GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}, []float32{float32(i%10) + 0.1, 0.1, 0.1})
	}

	// Create edges to form a connected graph
	for i := 0; i < nodeCount-1; i++ {
		es.InsertEdge(&GraphEdge{SourceID: nodeID(i), TargetID: nodeID(i + 1), EdgeType: EdgeTypeCalls})
	}

	// Setup mock HNSW with results
	mock := newMockHNSWSearcher()
	results := make([]HNSWSearchResult, nodeCount)
	for i := 0; i < nodeCount; i++ {
		results[i] = HNSWSearchResult{ID: nodeID(i), Similarity: float64(nodeCount-i) / float64(nodeCount)}
	}
	mock.setResults(results)
	mock.addVector(nodeID(0), []float32{1, 0.1, 0.1})

	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	// Run hybrid query - should use batch loading internally
	queryResults, err := qe.HybridQuery([]float32{1, 0, 0}, []string{nodeID(0)}, &HybridQueryOptions{
		VectorLimit: nodeCount,
		GraphDepth:  2,
	})
	if err != nil {
		t.Fatalf("HybridQuery: %v", err)
	}

	// Verify we got results - batch loading should have worked
	if len(queryResults) == 0 {
		t.Error("HybridQuery should return results with batch loading")
	}

	// Verify all results have valid nodes
	for _, r := range queryResults {
		if r.Node == nil {
			t.Error("Result node should not be nil")
		}
		if r.Node.ID == "" {
			t.Error("Result node ID should not be empty")
		}
	}
}

func TestQueryEngineScoreResultsWithBatchLoader(t *testing.T) {
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

	mock := newMockHNSWSearcher()
	vs := NewVectorSearcher(db, mock)
	gt := NewGraphTraverser(db)
	qe := NewQueryEngine(db, vs, gt)

	// Prepare test data
	allIDs := make(map[string]bool)
	vectorScores := make(map[string]float64)
	graphCounts := make(map[string]int)

	for i := 0; i < 10; i++ {
		id := nodeID(i)
		allIDs[id] = true
		vectorScores[id] = float64(10-i) / 10.0
		graphCounts[id] = i + 1
	}

	opts := &HybridQueryOptions{
		VectorWeight: 0.7,
		GraphWeight:  0.3,
	}

	// Call scoreResults directly to test batch loading
	results := qe.scoreResults(allIDs, vectorScores, graphCounts, 10, opts)

	if len(results) != 10 {
		t.Errorf("Got %d results, want 10", len(results))
	}

	// Verify all results have nodes and proper scores
	for _, r := range results {
		if r.Node == nil {
			t.Error("Result node should not be nil")
		}
	}
}
