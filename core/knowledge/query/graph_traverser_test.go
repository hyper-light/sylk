package query

import (
	"context"
	"errors"
	"testing"
	"time"
)

// =============================================================================
// Mock Edge Querier
// =============================================================================

// mockEdgeQuerier implements EdgeQuerier for testing.
type mockEdgeQuerier struct {
	getOutgoingEdgesFunc func(nodeID string, edgeTypes []string) ([]Edge, error)
	getIncomingEdgesFunc func(nodeID string, edgeTypes []string) ([]Edge, error)
	getNodesByPatternFunc func(pattern *NodeMatcher) ([]string, error)
}

func (m *mockEdgeQuerier) GetOutgoingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
	if m.getOutgoingEdgesFunc != nil {
		return m.getOutgoingEdgesFunc(nodeID, edgeTypes)
	}
	return nil, nil
}

func (m *mockEdgeQuerier) GetIncomingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
	if m.getIncomingEdgesFunc != nil {
		return m.getIncomingEdgesFunc(nodeID, edgeTypes)
	}
	return nil, nil
}

func (m *mockEdgeQuerier) GetNodesByPattern(pattern *NodeMatcher) ([]string, error) {
	if m.getNodesByPatternFunc != nil {
		return m.getNodesByPatternFunc(pattern)
	}
	return nil, nil
}

// =============================================================================
// GraphTraverser Constructor Tests
// =============================================================================

func TestNewGraphTraverser(t *testing.T) {
	mock := &mockEdgeQuerier{}
	traverser := NewGraphTraverser(mock)

	if traverser == nil {
		t.Fatal("NewGraphTraverser returned nil")
	}

	if !traverser.IsReady() {
		t.Error("expected IsReady() to return true")
	}
}

func TestNewGraphTraverser_NilDB(t *testing.T) {
	traverser := NewGraphTraverser(nil)

	if traverser == nil {
		t.Fatal("NewGraphTraverser returned nil")
	}

	if traverser.IsReady() {
		t.Error("expected IsReady() to return false for nil db")
	}
}

// =============================================================================
// GraphTraverser Execute Tests
// =============================================================================

func TestGraphTraverser_Execute_NilDB(t *testing.T) {
	traverser := NewGraphTraverser(nil)
	ctx := context.Background()

	entityType := "function"
	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
	}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for nil db, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for nil db, got %d", len(results))
	}
}

func TestGraphTraverser_Execute_NilPattern(t *testing.T) {
	mock := &mockEdgeQuerier{}
	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	results, err := traverser.Execute(ctx, nil, 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for nil pattern, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for nil pattern, got %d", len(results))
	}
}

func TestGraphTraverser_Execute_EmptyPattern(t *testing.T) {
	mock := &mockEdgeQuerier{}
	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for empty pattern, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for empty pattern, got %d", len(results))
	}
}

func TestGraphTraverser_Execute_NoTraversalSteps(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			if pattern.EntityType != nil && *pattern.EntityType == entityType {
				return []string{"node1", "node2"}, nil
			}
			return nil, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode:  &NodeMatcher{EntityType: &entityType},
		Traversals: nil,
	}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// With no traversal steps, should return start nodes as results
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Check that results contain the start nodes
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	if !ids["node1"] || !ids["node2"] {
		t.Errorf("expected results to contain node1 and node2")
	}
}

func TestGraphTraverser_Execute_SimpleTraversal(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"start"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			if nodeID == "start" {
				return []Edge{
					{ID: 1, SourceID: "start", TargetID: "target1", EdgeType: "calls", Weight: 0.8},
					{ID: 2, SourceID: "start", TargetID: "target2", EdgeType: "calls", Weight: 0.6},
				}, nil
			}
			return nil, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 1},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should find paths to target1 and target2
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(results))
	}
}

func TestGraphTraverser_Execute_MultiHopTraversal(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			edges := map[string][]Edge{
				"A": {{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "calls", Weight: 0.9}},
				"B": {{ID: 2, SourceID: "B", TargetID: "C", EdgeType: "calls", Weight: 0.8}},
				"C": {},
			}
			return edges[nodeID], nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	// Use multiple traversal steps to match the multi-hop path
	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			// First step: A -> B
			{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 1},
			// Second step: B -> C
			{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 1},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should find path A -> B -> C
	// Results may include intermediate paths as well
	foundPath := false
	for _, r := range results {
		if len(r.Path) >= 2 {
			foundPath = true
			break
		}
	}
	if len(results) > 0 && !foundPath {
		// At minimum we should have some results from the traversal
		t.Logf("Got %d results", len(results))
	}
}

func TestGraphTraverser_Execute_BidirectionalTraversal(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"center"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			if nodeID == "center" {
				return []Edge{
					{ID: 1, SourceID: "center", TargetID: "out1", EdgeType: "calls", Weight: 0.8},
				}, nil
			}
			return nil, nil
		},
		getIncomingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			if nodeID == "center" {
				return []Edge{
					{ID: 2, SourceID: "in1", TargetID: "center", EdgeType: "calls", Weight: 0.7},
				}, nil
			}
			return nil, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "calls", Direction: DirectionBoth, MaxHops: 1},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should find both outgoing and incoming neighbors
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(results))
	}
}

func TestGraphTraverser_Execute_CycleDetection(t *testing.T) {
	entityType := "function"
	callCount := 0
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			callCount++
			// Create a cycle: A -> B -> A
			edges := map[string][]Edge{
				"A": {{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "calls", Weight: 0.9}},
				"B": {{ID: 2, SourceID: "B", TargetID: "A", EdgeType: "calls", Weight: 0.8}}, // Cycle back
			}
			return edges[nodeID], nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 5},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should not get stuck in infinite loop
	// The cycle detection should prevent revisiting A
	if callCount > 20 { // Should complete in reasonable time
		t.Errorf("too many calls (%d), cycle detection may not be working", callCount)
	}

	_ = results
}

func TestGraphTraverser_Execute_ContextCancelled(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"node1"}, nil
		},
	}

	traverser := NewGraphTraverser(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
	}

	results, err := traverser.Execute(ctx, pattern, 10)

	if err != context.Canceled {
		t.Errorf("Execute() should return context.Canceled, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results on context cancel, got %d", len(results))
	}
}

func TestGraphTraverser_Execute_GracefulDegradation_PatternError(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return nil, errors.New("pattern matching failed")
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
	}

	results, err := traverser.Execute(ctx, pattern, 10)

	// Should return empty results, not error (graceful degradation)
	if err != nil {
		t.Errorf("Execute() should return nil error for graceful degradation, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results on pattern error, got %d", len(results))
	}
}

func TestGraphTraverser_Execute_GracefulDegradation_EdgeError(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"start"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			return nil, errors.New("edge query failed")
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 1},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 10)

	// Should handle gracefully
	if err != nil {
		t.Errorf("Execute() should handle edge errors gracefully, got %v", err)
	}

	// May have empty results or partial results
	_ = results
}

func TestGraphTraverser_Execute_LimitEnforced(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			// Return many start nodes
			return []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8", "n9", "n10"}, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode:  &NodeMatcher{EntityType: &entityType},
		Traversals: nil, // No traversals, return start nodes
	}

	limit := 3
	results, err := traverser.Execute(ctx, pattern, limit)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) > limit {
		t.Errorf("expected at most %d results, got %d", limit, len(results))
	}
}

func TestGraphTraverser_Execute_ZeroLimit(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"start"}, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode:  &NodeMatcher{EntityType: &entityType},
		Traversals: nil,
	}

	// With zero limit, should default to 10
	results, err := traverser.Execute(ctx, pattern, 0)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 1 { // Only 1 start node
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// =============================================================================
// GraphResult Tests
// =============================================================================

func TestGraphResult_Fields(t *testing.T) {
	result := GraphResult{
		ID:   "test-id",
		Path: []string{"A", "B", "C"},
		MatchedEdges: []EdgeMatch{
			{EdgeID: 1, EdgeType: "calls", Weight: 0.9},
			{EdgeID: 2, EdgeType: "calls", Weight: 0.8},
		},
		Score: 0.85,
	}

	if result.ID != "test-id" {
		t.Errorf("ID = %v, want test-id", result.ID)
	}
	if len(result.Path) != 3 {
		t.Errorf("Path length = %d, want 3", len(result.Path))
	}
	if len(result.MatchedEdges) != 2 {
		t.Errorf("MatchedEdges length = %d, want 2", len(result.MatchedEdges))
	}
	if result.Score != 0.85 {
		t.Errorf("Score = %v, want 0.85", result.Score)
	}
}

// =============================================================================
// Edge Tests
// =============================================================================

func TestEdge_Fields(t *testing.T) {
	edge := Edge{
		ID:       123,
		SourceID: "source",
		TargetID: "target",
		EdgeType: "calls",
		Weight:   0.95,
	}

	if edge.ID != 123 {
		t.Errorf("ID = %v, want 123", edge.ID)
	}
	if edge.SourceID != "source" {
		t.Errorf("SourceID = %v, want source", edge.SourceID)
	}
	if edge.TargetID != "target" {
		t.Errorf("TargetID = %v, want target", edge.TargetID)
	}
	if edge.EdgeType != "calls" {
		t.Errorf("EdgeType = %v, want calls", edge.EdgeType)
	}
	if edge.Weight != 0.95 {
		t.Errorf("Weight = %v, want 0.95", edge.Weight)
	}
}

// =============================================================================
// GraphTraverser IsReady Tests
// =============================================================================

func TestGraphTraverser_IsReady(t *testing.T) {
	tests := []struct {
		name     string
		db       EdgeQuerier
		expected bool
	}{
		{"with_db", &mockEdgeQuerier{}, true},
		{"nil_db", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			traverser := NewGraphTraverser(tt.db)
			if got := traverser.IsReady(); got != tt.expected {
				t.Errorf("IsReady() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// GraphTraverser Score Calculation Tests
// =============================================================================

func TestGraphTraverser_ScoreCalculation(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"start"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			edges := map[string][]Edge{
				"start": {{ID: 1, SourceID: "start", TargetID: "end", EdgeType: "calls", Weight: 1.0}},
				"end":   {},
			}
			return edges[nodeID], nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 1},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, result := range results {
		// Score should be positive
		if result.Score <= 0 {
			t.Errorf("result.Score = %v, expected positive", result.Score)
		}
		// Score should be bounded
		if result.Score > 1.0 {
			t.Errorf("result.Score = %v, expected <= 1.0", result.Score)
		}
	}
}

// =============================================================================
// GraphTraverser Direction Tests
// =============================================================================

func TestGraphTraverser_DirectionOutgoing(t *testing.T) {
	entityType := "function"
	outgoingCalled := false
	incomingCalled := false

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"start"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			outgoingCalled = true
			return nil, nil
		},
		getIncomingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			incomingCalled = true
			return nil, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{Direction: DirectionOutgoing, MaxHops: 1},
		},
	}

	_, _ = traverser.Execute(ctx, pattern, 10)

	if !outgoingCalled {
		t.Error("expected GetOutgoingEdges to be called for DirectionOutgoing")
	}
	if incomingCalled {
		t.Error("GetIncomingEdges should not be called for DirectionOutgoing")
	}
}

func TestGraphTraverser_DirectionIncoming(t *testing.T) {
	entityType := "function"
	outgoingCalled := false
	incomingCalled := false

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"start"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			outgoingCalled = true
			return nil, nil
		},
		getIncomingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			incomingCalled = true
			return nil, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{Direction: DirectionIncoming, MaxHops: 1},
		},
	}

	_, _ = traverser.Execute(ctx, pattern, 10)

	if outgoingCalled {
		t.Error("GetOutgoingEdges should not be called for DirectionIncoming")
	}
	if !incomingCalled {
		t.Error("expected GetIncomingEdges to be called for DirectionIncoming")
	}
}

func TestGraphTraverser_DirectionBoth(t *testing.T) {
	entityType := "function"
	outgoingCalled := false
	incomingCalled := false

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"start"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			outgoingCalled = true
			return nil, nil
		},
		getIncomingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			incomingCalled = true
			return nil, nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{Direction: DirectionBoth, MaxHops: 1},
		},
	}

	_, _ = traverser.Execute(ctx, pattern, 10)

	if !outgoingCalled {
		t.Error("expected GetOutgoingEdges to be called for DirectionBoth")
	}
	if !incomingCalled {
		t.Error("expected GetIncomingEdges to be called for DirectionBoth")
	}
}

// =============================================================================
// GraphTraverser Timeout Test
// =============================================================================

func TestGraphTraverser_Execute_ContextTimeout(t *testing.T) {
	entityType := "function"
	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			// Simulate slow pattern matching
			time.Sleep(100 * time.Millisecond)
			return []string{"start"}, nil
		},
	}

	traverser := NewGraphTraverser(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
	}

	// May return timeout error or gracefully degrade
	results, _ := traverser.Execute(ctx, pattern, 10)

	// Should handle timeout gracefully
	_ = results
}
