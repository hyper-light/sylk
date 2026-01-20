package query

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
)

// =============================================================================
// Mock Implementations for Testing
// =============================================================================

// mockBleveIndexForCoordinator provides a configurable mock Bleve index.
type mockBleveIndexForCoordinator struct {
	results  []TextResult
	err      error
	delay    time.Duration
	callFunc func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error)
}

func (m *mockBleveIndexForCoordinator) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, req)
	}

	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.delay):
		}
	}

	if m.err != nil {
		return nil, m.err
	}

	hits := make([]*blevesearch.DocumentMatch, len(m.results))
	for i, r := range m.results {
		hits[i] = &blevesearch.DocumentMatch{
			ID:    r.ID,
			Score: r.Score,
			Fields: map[string]interface{}{
				"content": r.Content,
			},
		}
	}

	return &bleve.SearchResult{
		Hits:  hits,
		Total: uint64(len(hits)),
	}, nil
}

// mockHNSWIndexForCoordinator provides a configurable mock HNSW index.
type mockHNSWIndexForCoordinator struct {
	results []VectorResult
	err     error
	delay   time.Duration
}

func (m *mockHNSWIndexForCoordinator) Search(vector []float32, k int) ([]string, []float32, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	if m.err != nil {
		return nil, nil, m.err
	}

	ids := make([]string, len(m.results))
	distances := make([]float32, len(m.results))
	for i, r := range m.results {
		ids[i] = r.ID
		distances[i] = r.Distance
	}

	return ids, distances, nil
}

// mockEdgeQuerierForCoordinator provides a configurable mock edge querier.
type mockEdgeQuerierForCoordinator struct {
	results []GraphResult
	nodes   []string
	err     error
	delay   time.Duration
}

func (m *mockEdgeQuerierForCoordinator) GetOutgoingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	if m.err != nil {
		return nil, m.err
	}

	// Simple mock: return edges to result nodes
	var edges []Edge
	for i, r := range m.results {
		edges = append(edges, Edge{
			ID:       int64(i),
			SourceID: nodeID,
			TargetID: r.ID,
			EdgeType: "related",
			Weight:   r.Score,
		})
	}
	return edges, nil
}

func (m *mockEdgeQuerierForCoordinator) GetIncomingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
	return nil, nil
}

func (m *mockEdgeQuerierForCoordinator) GetNodesByPattern(pattern *NodeMatcher) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.nodes) > 0 {
		return m.nodes, nil
	}
	return []string{"start-node"}, nil
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewHybridQueryCoordinator(t *testing.T) {
	bleve := NewBleveSearcher(&mockBleveIndexForCoordinator{})
	vector := NewVectorSearcher(&mockHNSWIndexForCoordinator{})
	graph := NewGraphTraverser(&mockEdgeQuerierForCoordinator{})

	coord := NewHybridQueryCoordinator(bleve, vector, graph)

	if coord == nil {
		t.Fatal("NewHybridQueryCoordinator returned nil")
	}

	if coord.bleveSearcher != bleve {
		t.Error("bleveSearcher not set correctly")
	}
	if coord.vectorSearcher != vector {
		t.Error("vectorSearcher not set correctly")
	}
	if coord.graphTraverser != graph {
		t.Error("graphTraverser not set correctly")
	}
	if coord.rrfFusion == nil {
		t.Error("rrfFusion not initialized")
	}
	if coord.learnedWeights == nil {
		t.Error("learnedWeights not initialized")
	}
	if coord.timeout != 100*time.Millisecond {
		t.Errorf("timeout = %v, want 100ms", coord.timeout)
	}
}

func TestNewHybridQueryCoordinator_NilComponents(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	if coord == nil {
		t.Fatal("NewHybridQueryCoordinator returned nil")
	}

	if coord.IsReady() {
		t.Error("expected IsReady() to return false with nil components")
	}
}

func TestNewHybridQueryCoordinatorWithOptions(t *testing.T) {
	bleve := NewBleveSearcher(&mockBleveIndexForCoordinator{})
	vector := NewVectorSearcher(&mockHNSWIndexForCoordinator{})
	graph := NewGraphTraverser(&mockEdgeQuerierForCoordinator{})
	rrf := NewRRFFusion(100)
	weights := NewLearnedQueryWeights()
	timeout := 200 * time.Millisecond

	coord := NewHybridQueryCoordinatorWithOptions(bleve, vector, graph, rrf, weights, timeout)

	if coord.rrfFusion != rrf {
		t.Error("rrfFusion not set correctly")
	}
	if coord.learnedWeights != weights {
		t.Error("learnedWeights not set correctly")
	}
	if coord.timeout != timeout {
		t.Errorf("timeout = %v, want %v", coord.timeout, timeout)
	}
}

func TestNewHybridQueryCoordinatorWithOptions_Defaults(t *testing.T) {
	coord := NewHybridQueryCoordinatorWithOptions(nil, nil, nil, nil, nil, 0)

	if coord.rrfFusion == nil {
		t.Error("rrfFusion should default to non-nil")
	}
	if coord.learnedWeights == nil {
		t.Error("learnedWeights should default to non-nil")
	}
	if coord.timeout != 100*time.Millisecond {
		t.Errorf("timeout should default to 100ms, got %v", coord.timeout)
	}
}

// =============================================================================
// Timeout Configuration Tests
// =============================================================================

func TestSetTimeout(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	coord.SetTimeout(500 * time.Millisecond)
	if coord.GetTimeout() != 500*time.Millisecond {
		t.Errorf("GetTimeout() = %v, want 500ms", coord.GetTimeout())
	}
}

func TestSetTimeout_ZeroValue(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	originalTimeout := coord.GetTimeout()

	coord.SetTimeout(0)
	if coord.GetTimeout() != originalTimeout {
		t.Errorf("timeout should not change for zero value, got %v", coord.GetTimeout())
	}
}

func TestSetTimeout_NegativeValue(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	originalTimeout := coord.GetTimeout()

	coord.SetTimeout(-100 * time.Millisecond)
	if coord.GetTimeout() != originalTimeout {
		t.Errorf("timeout should not change for negative value, got %v", coord.GetTimeout())
	}
}

// =============================================================================
// RRF Parameter Tests
// =============================================================================

func TestSetRRFParameter(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	coord.SetRRFParameter(100)
	if coord.GetRRFParameter() != 100 {
		t.Errorf("GetRRFParameter() = %d, want 100", coord.GetRRFParameter())
	}
}

func TestSetRRFParameter_ZeroDefaultsTo60(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	coord.SetRRFParameter(0)
	if coord.GetRRFParameter() != 60 {
		t.Errorf("GetRRFParameter() = %d, want 60", coord.GetRRFParameter())
	}
}

// =============================================================================
// Execute Tests - Basic Functionality
// =============================================================================

func TestExecute_NilQuery(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	ctx := context.Background()

	results, err := coord.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for nil query, got %d", len(results))
	}
}

func TestExecute_InvalidQuery(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	ctx := context.Background()

	// Query with no modalities
	query := &HybridQuery{}

	_, err := coord.Execute(ctx, query)
	if err == nil {
		t.Error("expected error for invalid query")
	}
}

func TestExecute_TextSearchOnly(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{
			{ID: "doc1", Score: 0.95, Content: "content 1"},
			{ID: "doc2", Score: 0.85, Content: "content 2"},
		},
	}
	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery: "test query",
		Limit:     10,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	if results[0].ID != "doc1" {
		t.Errorf("results[0].ID = %v, want doc1", results[0].ID)
	}
}

func TestExecute_SemanticSearchOnly(t *testing.T) {
	mockHNSW := &mockHNSWIndexForCoordinator{
		results: []VectorResult{
			{ID: "vec1", Score: 0.95, Distance: 0.1},
			{ID: "vec2", Score: 0.80, Distance: 0.25},
		},
	}
	vector := NewVectorSearcher(mockHNSW)
	coord := NewHybridQueryCoordinator(nil, vector, nil)
	ctx := context.Background()

	query := &HybridQuery{
		SemanticVector: []float32{0.1, 0.2, 0.3},
		Limit:          10,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestExecute_GraphSearchOnly(t *testing.T) {
	mockEdge := &mockEdgeQuerierForCoordinator{
		results: []GraphResult{
			{ID: "node1", Score: 0.9, Path: []string{"start", "node1"}},
		},
		nodes: []string{"start"},
	}
	graph := NewGraphTraverser(mockEdge)
	coord := NewHybridQueryCoordinator(nil, nil, graph)
	ctx := context.Background()

	entityType := "test_type"
	query := &HybridQuery{
		GraphPattern: &GraphPattern{
			StartNode: &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{
				{Direction: DirectionOutgoing, MaxHops: 1},
			},
		},
		Limit: 10,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Results depend on graph traversal implementation
	if results == nil {
		t.Error("expected non-nil results")
	}
}

// =============================================================================
// Execute Tests - Parallel Execution
// =============================================================================

func TestExecute_ParallelExecution(t *testing.T) {
	var textCalled, semanticCalled, graphCalled atomic.Bool

	mockBleve := &mockBleveIndexForCoordinator{
		callFunc: func(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
			textCalled.Store(true)
			time.Sleep(10 * time.Millisecond)
			return &bleve.SearchResult{
				Hits: []*blevesearch.DocumentMatch{
					{ID: "text1", Score: 0.9},
				},
			}, nil
		},
	}

	mockHNSW := &mockHNSWIndexForCoordinator{
		results: []VectorResult{{ID: "vec1", Score: 0.85}},
		delay:   10 * time.Millisecond,
	}

	mockEdge := &mockEdgeQuerierForCoordinator{
		results: []GraphResult{{ID: "graph1", Score: 0.8}},
		nodes:   []string{"start"},
		delay:   10 * time.Millisecond,
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockHNSW)
	graph := NewGraphTraverser(mockEdge)
	coord := NewHybridQueryCoordinator(bleve, vector, graph)
	coord.SetTimeout(1 * time.Second)
	ctx := context.Background()

	entityType := "test"
	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		GraphPattern: &GraphPattern{
			StartNode:  &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{{Direction: DirectionOutgoing, MaxHops: 1}},
		},
		Limit: 10,
	}

	start := time.Now()
	results, err := coord.Execute(ctx, query)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify parallel execution - should complete faster than sequential
	// Sequential would be ~30ms, parallel should be ~10-15ms
	if elapsed > 50*time.Millisecond {
		t.Errorf("execution took %v, expected parallel execution to be faster", elapsed)
	}

	// Verify results from multiple sources
	if len(results) == 0 {
		t.Error("expected results from parallel execution")
	}

	// Verify text search was called
	if !textCalled.Load() {
		t.Error("text search was not called")
	}

	// Mark semantic and graph as called (they execute via mocks)
	semanticCalled.Store(true)
	graphCalled.Store(true)
}

// =============================================================================
// Execute Tests - Timeout Handling
// =============================================================================

func TestExecute_TimeoutReturnsPartialResults(t *testing.T) {
	// Fast text search
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{{ID: "fast1", Score: 0.9}},
		delay:   5 * time.Millisecond,
	}

	// Slow semantic search (should timeout)
	mockHNSW := &mockHNSWIndexForCoordinator{
		results: []VectorResult{{ID: "slow1", Score: 0.85}},
		delay:   500 * time.Millisecond,
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockHNSW)
	coord := NewHybridQueryCoordinator(bleve, vector, nil)
	coord.SetTimeout(50 * time.Millisecond)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should have partial results from fast search
	if len(result.Results) == 0 {
		t.Error("expected partial results on timeout")
	}

	// Should indicate timeout occurred
	if !result.Metrics.TimedOut {
		t.Error("expected TimedOut to be true")
	}
}

func TestExecute_QueryTimeoutOverridesDefault(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
		delay:   30 * time.Millisecond,
	}

	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	coord.SetTimeout(10 * time.Millisecond) // Short default timeout
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery: "test",
		Timeout:   100 * time.Millisecond, // Query specifies longer timeout
		Limit:     10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Query timeout should allow search to complete
	if result.Metrics.TimedOut {
		t.Error("query should not have timed out with longer query timeout")
	}

	if len(result.Results) == 0 {
		t.Error("expected results when query timeout is sufficient")
	}
}

// =============================================================================
// Execute Tests - Graceful Degradation
// =============================================================================

func TestExecute_TextSearchError_GracefulDegradation(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		err: errors.New("bleve error"),
	}
	mockHNSW := &mockHNSWIndexForCoordinator{
		results: []VectorResult{{ID: "vec1", Score: 0.9}},
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockHNSW)
	coord := NewHybridQueryCoordinator(bleve, vector, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          10,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() should not return error on single source failure, got %v", err)
	}

	// Should still have results from semantic search
	if len(results) == 0 {
		t.Error("expected results from semantic search despite text search failure")
	}
}

func TestExecute_AllSearchersError_ReturnsEmptyResults(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		err: errors.New("bleve error"),
	}
	mockHNSW := &mockHNSWIndexForCoordinator{
		err: errors.New("hnsw error"),
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockHNSW)
	coord := NewHybridQueryCoordinator(bleve, vector, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          10,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() should not return error, got %v", err)
	}

	// Should return empty results, not error
	if len(results) != 0 {
		t.Errorf("expected empty results when all searches fail, got %d", len(results))
	}
}

// =============================================================================
// Execute Tests - Result Fusion
// =============================================================================

func TestExecute_ResultFusion_CombinedScores(t *testing.T) {
	// Same document appears in both text and semantic results
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{
			{ID: "common", Score: 0.9, Content: "shared content"},
			{ID: "text-only", Score: 0.8, Content: "text only"},
		},
	}
	mockHNSW := &mockHNSWIndexForCoordinator{
		results: []VectorResult{
			{ID: "common", Score: 0.85, Distance: 0.15},
			{ID: "vec-only", Score: 0.75, Distance: 0.25},
		},
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockHNSW)
	coord := NewHybridQueryCoordinator(bleve, vector, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          10,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Common document should be ranked first (appears in both sources)
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	// Find the common document
	var commonResult *HybridResult
	for i := range results {
		if results[i].ID == "common" {
			commonResult = &results[i]
			break
		}
	}

	if commonResult == nil {
		t.Fatal("expected to find 'common' document in results")
	}

	// Verify it has combined source
	if commonResult.Source != SourceCombined {
		t.Errorf("common result source = %v, want SourceCombined", commonResult.Source)
	}

	// Verify both component scores are set
	if commonResult.TextScore <= 0 {
		t.Error("expected TextScore > 0 for combined result")
	}
	if commonResult.SemanticScore <= 0 {
		t.Error("expected SemanticScore > 0 for combined result")
	}
}

func TestExecute_ResultFusion_ExplicitWeights(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{{ID: "text1", Score: 0.9}},
	}
	mockHNSW := &mockHNSWIndexForCoordinator{
		results: []VectorResult{{ID: "vec1", Score: 0.9}},
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockHNSW)
	coord := NewHybridQueryCoordinator(bleve, vector, nil)
	ctx := context.Background()

	// Query with heavy text weight
	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		TextWeight:     0.9,
		SemanticWeight: 0.1,
		GraphWeight:    0.0,
		Limit:          10,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) < 2 {
		t.Fatal("expected at least 2 results")
	}

	// With heavy text weight, text result should rank higher
	if results[0].ID != "text1" {
		t.Errorf("expected text1 to be first with high text weight, got %s", results[0].ID)
	}
}

// =============================================================================
// Execute Tests - Limit Handling
// =============================================================================

func TestExecute_LimitApplied(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{
			{ID: "doc1", Score: 0.95},
			{ID: "doc2", Score: 0.90},
			{ID: "doc3", Score: 0.85},
			{ID: "doc4", Score: 0.80},
			{ID: "doc5", Score: 0.75},
		},
	}

	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery: "test",
		Limit:     3,
	}

	results, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results (limit), got %d", len(results))
	}
}

func TestExecute_DefaultLimitWhenZero(t *testing.T) {
	results := make([]TextResult, 20)
	for i := 0; i < 20; i++ {
		results[i] = TextResult{ID: string(rune('a' + i)), Score: float64(20-i) / 20.0}
	}

	mockBleve := &mockBleveIndexForCoordinator{results: results}
	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery: "test",
		Limit:     0, // Zero limit
	}

	resultList, err := coord.Execute(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should return all results when limit is 0
	if len(resultList) == 0 {
		t.Error("expected results when limit is 0")
	}
}

// =============================================================================
// Metrics Tests
// =============================================================================

func TestExecuteWithMetrics_TracksTiming(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
		delay:   10 * time.Millisecond,
	}

	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery: "test",
		Limit:     10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	if err != nil {
		t.Fatalf("ExecuteWithMetrics() error = %v", err)
	}

	if result.Metrics == nil {
		t.Fatal("expected non-nil metrics")
	}

	if result.Metrics.TextLatency < 10*time.Millisecond {
		t.Errorf("TextLatency = %v, expected >= 10ms", result.Metrics.TextLatency)
	}

	if result.Metrics.TotalLatency < 10*time.Millisecond {
		t.Errorf("TotalLatency = %v, expected >= 10ms", result.Metrics.TotalLatency)
	}

	if !result.Metrics.TextContributed {
		t.Error("expected TextContributed to be true")
	}
}

func TestRecordMetrics(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	defer coord.Close()

	metrics := &QueryMetrics{
		TextLatency:         10 * time.Millisecond,
		SemanticLatency:     15 * time.Millisecond,
		TotalLatency:        20 * time.Millisecond,
		TextContributed:     true,
		SemanticContributed: true,
	}

	coord.RecordMetrics("query-1", metrics)

	retrieved, ok := coord.GetQueryMetrics("query-1")
	if !ok {
		t.Fatal("expected to find metrics for query-1")
	}

	if retrieved.TextLatency != 10*time.Millisecond {
		t.Errorf("TextLatency = %v, want 10ms", retrieved.TextLatency)
	}
}

func TestGetAverageMetrics(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	defer coord.Close()

	// Record multiple metrics
	coord.RecordMetrics("q1", &QueryMetrics{
		TextLatency:     10 * time.Millisecond,
		TotalLatency:    20 * time.Millisecond,
		TextContributed: true,
	})
	coord.RecordMetrics("q2", &QueryMetrics{
		TextLatency:     30 * time.Millisecond,
		TotalLatency:    40 * time.Millisecond,
		TextContributed: true,
	})

	avg := coord.GetAverageMetrics()

	if avg.TextLatency != 20*time.Millisecond {
		t.Errorf("average TextLatency = %v, want 20ms", avg.TextLatency)
	}

	if avg.TotalLatency != 30*time.Millisecond {
		t.Errorf("average TotalLatency = %v, want 30ms", avg.TotalLatency)
	}
}

func TestGetAverageMetrics_Empty(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	defer coord.Close()

	avg := coord.GetAverageMetrics()

	if avg.TotalLatency != 0 {
		t.Errorf("expected zero latency for empty metrics, got %v", avg.TotalLatency)
	}
}

// =============================================================================
// Status Tests
// =============================================================================

func TestIsReady(t *testing.T) {
	tests := []struct {
		name     string
		bleve    *BleveSearcher
		vector   *VectorSearcher
		graph    *GraphTraverser
		expected bool
	}{
		{
			name:     "all_nil",
			expected: false,
		},
		{
			name:     "bleve_only",
			bleve:    NewBleveSearcher(&mockBleveIndexForCoordinator{}),
			expected: true,
		},
		{
			name:     "vector_only",
			vector:   NewVectorSearcher(&mockHNSWIndexForCoordinator{}),
			expected: true,
		},
		{
			name:     "graph_only",
			graph:    NewGraphTraverser(&mockEdgeQuerierForCoordinator{}),
			expected: true,
		},
		{
			name:     "all_present",
			bleve:    NewBleveSearcher(&mockBleveIndexForCoordinator{}),
			vector:   NewVectorSearcher(&mockHNSWIndexForCoordinator{}),
			graph:    NewGraphTraverser(&mockEdgeQuerierForCoordinator{}),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coord := NewHybridQueryCoordinator(tt.bleve, tt.vector, tt.graph)
			if got := coord.IsReady(); got != tt.expected {
				t.Errorf("IsReady() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestReadySearchers(t *testing.T) {
	bleve := NewBleveSearcher(&mockBleveIndexForCoordinator{})
	vector := NewVectorSearcher(&mockHNSWIndexForCoordinator{})
	coord := NewHybridQueryCoordinator(bleve, vector, nil)

	ready := coord.ReadySearchers()

	if len(ready) != 2 {
		t.Errorf("expected 2 ready searchers, got %d", len(ready))
	}

	hasText := false
	hasSemantic := false
	for _, s := range ready {
		if s == "text" {
			hasText = true
		}
		if s == "semantic" {
			hasSemantic = true
		}
	}

	if !hasText {
		t.Error("expected 'text' in ready searchers")
	}
	if !hasSemantic {
		t.Error("expected 'semantic' in ready searchers")
	}
}

// =============================================================================
// Component Accessor Tests
// =============================================================================

func TestComponentAccessors(t *testing.T) {
	bleve := NewBleveSearcher(&mockBleveIndexForCoordinator{})
	vector := NewVectorSearcher(&mockHNSWIndexForCoordinator{})
	graph := NewGraphTraverser(&mockEdgeQuerierForCoordinator{})
	coord := NewHybridQueryCoordinator(bleve, vector, graph)

	if coord.BleveSearcher() != bleve {
		t.Error("BleveSearcher() returned wrong instance")
	}
	if coord.VectorSearcher() != vector {
		t.Error("VectorSearcher() returned wrong instance")
	}
	if coord.GraphTraverser() != graph {
		t.Error("GraphTraverser() returned wrong instance")
	}
	if coord.RRFFusion() == nil {
		t.Error("RRFFusion() returned nil")
	}
	if coord.LearnedWeights() == nil {
		t.Error("LearnedWeights() returned nil")
	}
}

// =============================================================================
// Domain Detection Tests
// =============================================================================

func TestDetectDomain_FromFilter(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	query := &HybridQuery{
		TextQuery: "test",
		Filters: []QueryFilter{
			{Type: FilterDomain, Value: domain.DomainArchitect},
		},
	}

	d := coord.detectDomain(query)
	if d != domain.DomainArchitect {
		t.Errorf("detectDomain() = %v, want DomainArchitect", d)
	}
}

func TestDetectDomain_FromStringFilter(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	query := &HybridQuery{
		TextQuery: "test",
		Filters: []QueryFilter{
			{Type: FilterDomain, Value: "engineer"},
		},
	}

	d := coord.detectDomain(query)
	if d != domain.DomainEngineer {
		t.Errorf("detectDomain() = %v, want DomainEngineer", d)
	}
}

func TestDetectDomain_DefaultToLibrarian(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	query := &HybridQuery{
		TextQuery: "test",
	}

	d := coord.detectDomain(query)
	if d != domain.DomainLibrarian {
		t.Errorf("detectDomain() = %v, want DomainLibrarian", d)
	}
}

// =============================================================================
// Weight Learning Integration Tests
// =============================================================================

func TestUpdateWeights(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	textResults := []TextResult{
		{ID: "doc1", Score: 0.9},
		{ID: "doc2", Score: 0.8},
	}
	semanticResults := []VectorResult{
		{ID: "doc2", Score: 0.95},
		{ID: "doc1", Score: 0.7},
	}

	// Update weights as if user clicked doc2 (which was ranked higher by semantic)
	coord.UpdateWeights(
		"query-1",
		"doc2",
		textResults,
		semanticResults,
		nil,
		domain.DomainLibrarian,
	)

	// Verify learned weights were updated (semantic should be favored)
	stats := coord.LearnedWeights().GetStats()
	if stats.SemanticMean == 0 {
		t.Error("expected semantic weight to be updated")
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestExecute_ContextCancelled(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
		delay:   100 * time.Millisecond,
	}

	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	coord.SetTimeout(1 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	query := &HybridQuery{
		TextQuery: "test",
		Limit:     10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should return quickly with timeout flag
	if !result.Metrics.TimedOut {
		t.Error("expected TimedOut to be true on context cancellation")
	}
}

// =============================================================================
// Query Metrics Tests
// =============================================================================

func TestQueryMetrics_ErrorTracking(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		err: errors.New("test error"),
	}

	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery: "test",
		Limit:     10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Note: Bleve searcher does graceful degradation, so error is swallowed
	// The metric should show no contribution
	if result.Metrics.TextContributed {
		t.Error("expected TextContributed to be false on error")
	}
}

func TestQueryMetrics_SourceContribution(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
	}
	mockHNSW := &mockHNSWIndexForCoordinator{
		results: []VectorResult{}, // Empty results
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockHNSW)
	coord := NewHybridQueryCoordinator(bleve, vector, nil)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !result.Metrics.TextContributed {
		t.Error("expected TextContributed to be true")
	}
	if result.Metrics.SemanticContributed {
		t.Error("expected SemanticContributed to be false for empty results")
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestConcurrentExecute(t *testing.T) {
	mockBleve := &mockBleveIndexForCoordinator{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
	}

	bleve := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleve, nil, nil)
	ctx := context.Background()

	// Run multiple concurrent queries
	const numQueries = 10
	done := make(chan bool, numQueries)

	for i := 0; i < numQueries; i++ {
		go func(id int) {
			query := &HybridQuery{
				TextQuery: "test",
				Limit:     10,
			}
			_, err := coord.Execute(ctx, query)
			if err != nil {
				t.Errorf("query %d error: %v", id, err)
			}
			done <- true
		}(i)
	}

	// Wait for all queries to complete
	for i := 0; i < numQueries; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for concurrent queries")
		}
	}
}

func TestConcurrentSetTimeout(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)
	defer coord.Close()

	// Concurrent timeout updates
	const numUpdates = 100
	done := make(chan bool, numUpdates)

	for i := 0; i < numUpdates; i++ {
		go func(ms int) {
			coord.SetTimeout(time.Duration(ms) * time.Millisecond)
			_ = coord.GetTimeout()
			done <- true
		}(i + 1)
	}

	for i := 0; i < numUpdates; i++ {
		<-done
	}

	// Should complete without race conditions
}

// =============================================================================
// Bounded Metrics Tracker Tests
// =============================================================================

func TestMetricsTracker_RecordAndRetrieve(t *testing.T) {
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      100,
		TTL:             1 * time.Hour,
		CleanupInterval: 1 * time.Hour, // Long interval to avoid cleanup during test
	})
	defer mt.Close()

	metrics := &QueryMetrics{
		TextLatency:     10 * time.Millisecond,
		TotalLatency:    20 * time.Millisecond,
		TextContributed: true,
	}

	mt.Record("query-1", metrics)

	retrieved, ok := mt.GetMetrics("query-1")
	if !ok {
		t.Fatal("expected to find metrics for query-1")
	}

	if retrieved.TextLatency != 10*time.Millisecond {
		t.Errorf("TextLatency = %v, want 10ms", retrieved.TextLatency)
	}
	if retrieved.TotalLatency != 20*time.Millisecond {
		t.Errorf("TotalLatency = %v, want 20ms", retrieved.TotalLatency)
	}
}

func TestMetricsTracker_NotFound(t *testing.T) {
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      100,
		TTL:             1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	})
	defer mt.Close()

	_, ok := mt.GetMetrics("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent query")
	}
}

func TestMetricsTracker_MaxEntriesEviction(t *testing.T) {
	const maxEntries = 5
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      maxEntries,
		TTL:             1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	})
	defer mt.Close()

	// Record more entries than max
	for i := 0; i < maxEntries+10; i++ {
		mt.Record(
			string(rune('a'+i)),
			&QueryMetrics{TotalLatency: time.Duration(i) * time.Millisecond},
		)
	}

	// Verify entry count does not exceed max
	count := mt.EntryCount()
	if count > maxEntries {
		t.Errorf("entry count = %d, want <= %d", count, maxEntries)
	}

	// Most recent entries should still be present
	for i := maxEntries + 10 - 1; i >= maxEntries+10-maxEntries; i-- {
		_, ok := mt.GetMetrics(string(rune('a' + i)))
		if !ok {
			t.Errorf("expected recent entry %c to be present", rune('a'+i))
		}
	}
}

func TestMetricsTracker_TTLExpiration(t *testing.T) {
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      100,
		TTL:             50 * time.Millisecond, // Short TTL for testing
		CleanupInterval: 10 * time.Millisecond, // Fast cleanup
	})
	defer mt.Close()

	// Record an entry
	mt.Record("query-1", &QueryMetrics{TotalLatency: 10 * time.Millisecond})

	// Entry should be present initially
	_, ok := mt.GetMetrics("query-1")
	if !ok {
		t.Fatal("expected to find metrics immediately after recording")
	}

	// Wait for TTL to expire and cleanup to run
	time.Sleep(100 * time.Millisecond)

	// Entry should be cleaned up
	_, ok = mt.GetMetrics("query-1")
	if ok {
		t.Error("expected entry to be expired and cleaned up")
	}
}

func TestMetricsTracker_BoundedGrowth(t *testing.T) {
	const maxEntries = 100
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      maxEntries,
		TTL:             1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	})
	defer mt.Close()

	// Record many entries
	for i := 0; i < 1000; i++ {
		mt.Record(
			string(rune(i)),
			&QueryMetrics{TotalLatency: time.Duration(i) * time.Millisecond},
		)
	}

	// Verify bounded growth
	count := mt.EntryCount()
	if count > maxEntries {
		t.Errorf("entry count = %d, exceeds max %d", count, maxEntries)
	}
}

func TestMetricsTracker_AggregatesPreserved(t *testing.T) {
	const maxEntries = 3
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      maxEntries,
		TTL:             1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	})
	defer mt.Close()

	// Record entries that will be evicted
	for i := 0; i < 10; i++ {
		mt.Record(
			string(rune('a'+i)),
			&QueryMetrics{
				TotalLatency:    time.Duration(10*(i+1)) * time.Millisecond,
				TextContributed: true,
				TextLatency:     time.Duration(5*(i+1)) * time.Millisecond,
			},
		)
	}

	// Aggregates should reflect all recorded entries, not just retained ones
	avg := mt.GetAverageMetrics()

	// Total latency sum: 10+20+30+...+100 = 550ms, count = 10, avg = 55ms
	expectedAvg := 55 * time.Millisecond
	if avg.TotalLatency != expectedAvg {
		t.Errorf("average TotalLatency = %v, want %v", avg.TotalLatency, expectedAvg)
	}
}

func TestMetricsTracker_ConcurrentAccess(t *testing.T) {
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      100,
		TTL:             1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	})
	defer mt.Close()

	const numGoroutines = 50
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // Writers + readers

	// Writers
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				queryID := string(rune(goroutineID*1000 + i))
				mt.Record(queryID, &QueryMetrics{
					TotalLatency: time.Duration(i) * time.Millisecond,
				})
			}
		}(g)
	}

	// Readers
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				queryID := string(rune(goroutineID*1000 + i))
				mt.GetMetrics(queryID)
				mt.GetAverageMetrics()
				mt.EntryCount()
			}
		}(g)
	}

	wg.Wait()

	// Should complete without race conditions
	count := mt.EntryCount()
	if count > mt.MaxEntries() {
		t.Errorf("entry count %d exceeds max %d", count, mt.MaxEntries())
	}
}

func TestMetricsTracker_DefaultConfig(t *testing.T) {
	config := DefaultMetricsTrackerConfig()

	if config.MaxEntries != DefaultMetricsMaxEntries {
		t.Errorf("MaxEntries = %d, want %d", config.MaxEntries, DefaultMetricsMaxEntries)
	}
	if config.TTL != DefaultMetricsTTL {
		t.Errorf("TTL = %v, want %v", config.TTL, DefaultMetricsTTL)
	}
	if config.CleanupInterval != DefaultCleanupInterval {
		t.Errorf("CleanupInterval = %v, want %v", config.CleanupInterval, DefaultCleanupInterval)
	}
}

func TestMetricsTracker_ConfigDefaults(t *testing.T) {
	// Test that invalid config values get defaults
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      0,  // Should default
		TTL:             0,  // Should default
		CleanupInterval: 0,  // Should default
	})
	defer mt.Close()

	if mt.MaxEntries() != DefaultMetricsMaxEntries {
		t.Errorf("MaxEntries = %d, want %d", mt.MaxEntries(), DefaultMetricsMaxEntries)
	}
	if mt.TTL() != DefaultMetricsTTL {
		t.Errorf("TTL = %v, want %v", mt.TTL(), DefaultMetricsTTL)
	}
}

func TestMetricsTracker_Close(t *testing.T) {
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      100,
		TTL:             1 * time.Hour,
		CleanupInterval: 10 * time.Millisecond,
	})

	// Record some entries
	mt.Record("query-1", &QueryMetrics{TotalLatency: 10 * time.Millisecond})

	// Close should stop the cleanup goroutine
	mt.Close()

	// After close, the tracker is not usable but should not panic
	// This test verifies graceful shutdown
}

func TestCoordinator_Close(t *testing.T) {
	coord := NewHybridQueryCoordinator(nil, nil, nil)

	// Record some metrics
	coord.RecordMetrics("q1", &QueryMetrics{TotalLatency: 10 * time.Millisecond})

	// Close should clean up the metrics tracker
	coord.Close()

	// After close, coordinator operations may not work correctly,
	// but this test ensures Close doesn't panic
}

func TestMetricsTracker_EvictionOrder(t *testing.T) {
	const maxEntries = 3
	mt := newMetricsTrackerWithConfig(MetricsTrackerConfig{
		MaxEntries:      maxEntries,
		TTL:             1 * time.Hour,
		CleanupInterval: 1 * time.Hour,
	})
	defer mt.Close()

	// Record entries with delays to ensure different timestamps
	mt.Record("first", &QueryMetrics{TotalLatency: 1 * time.Millisecond})
	time.Sleep(1 * time.Millisecond)
	mt.Record("second", &QueryMetrics{TotalLatency: 2 * time.Millisecond})
	time.Sleep(1 * time.Millisecond)
	mt.Record("third", &QueryMetrics{TotalLatency: 3 * time.Millisecond})
	time.Sleep(1 * time.Millisecond)

	// Add one more, should evict "first" (oldest)
	mt.Record("fourth", &QueryMetrics{TotalLatency: 4 * time.Millisecond})

	// "first" should be evicted
	_, ok := mt.GetMetrics("first")
	if ok {
		t.Error("expected 'first' to be evicted")
	}

	// "second", "third", "fourth" should still be present
	for _, id := range []string{"second", "third", "fourth"} {
		_, ok := mt.GetMetrics(id)
		if !ok {
			t.Errorf("expected '%s' to be present", id)
		}
	}
}
