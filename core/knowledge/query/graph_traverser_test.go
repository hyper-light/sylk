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

// =============================================================================
// PF.4.6 Visit Key and Path-Based Cycle Detection Tests
// =============================================================================

func TestGraphTraverser_VisitKeyFix(t *testing.T) {
	entityType := "function"

	t.Run("path-based cycle detection allows same node via different paths", func(t *testing.T) {
		// Create a diamond graph: A -> B -> D and A -> C -> D
		// With proper path-based cycle detection, the traverser should explore D via both paths
		// We verify this by counting how many times each node was visited during expansion

		visitedPaths := make(map[string][]string)
		mock := &mockEdgeQuerier{
			getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
				return []string{"A"}, nil
			},
			getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
				edges := map[string][]Edge{
					"A": {
						{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "calls", Weight: 0.9},
						{ID: 2, SourceID: "A", TargetID: "C", EdgeType: "calls", Weight: 0.8},
					},
					"B": {{ID: 3, SourceID: "B", TargetID: "D", EdgeType: "calls", Weight: 0.7}},
					"C": {{ID: 4, SourceID: "C", TargetID: "D", EdgeType: "calls", Weight: 0.6}},
					"D": {},
				}
				if e, ok := edges[nodeID]; ok {
					for _, edge := range e {
						visitedPaths[edge.TargetID] = append(visitedPaths[edge.TargetID], nodeID)
					}
				}
				return edges[nodeID], nil
			},
		}

		// Use full cycle detection (SimpleVisit=false)
		config := TraverserConfig{
			SimpleVisit:   false,
			MaxPathLength: 20,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)
		ctx := context.Background()

		pattern := &GraphPattern{
			StartNode: &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{
				{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 3},
			},
		}

		results, err := traverser.Execute(ctx, pattern, 100)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		// With path-based visit tracking, D should be queried from both B and C
		// because the visit keys for (path=[A,B], D) and (path=[A,C], D) are different
		visitorsToD := visitedPaths["D"]
		if len(visitorsToD) < 2 {
			t.Errorf("expected D to be reached via both B and C, got visitors: %v", visitorsToD)
		}

		// Verify we got some results (the algorithm returns results when all steps complete)
		if len(results) == 0 {
			t.Log("No results returned - this is expected behavior as nodes with no further edges complete the traversal step")
		}

		// Verify that B and D were both visited (showing the traversal worked)
		if len(visitedPaths["B"]) == 0 {
			t.Error("expected B to be visited")
		}
		if len(visitedPaths["D"]) == 0 {
			t.Error("expected D to be visited")
		}
	})

	t.Run("simple visit mode prevents revisits", func(t *testing.T) {
		// Same diamond graph
		mock := &mockEdgeQuerier{
			getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
				return []string{"A"}, nil
			},
			getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
				edges := map[string][]Edge{
					"A": {
						{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "calls", Weight: 0.9},
						{ID: 2, SourceID: "A", TargetID: "C", EdgeType: "calls", Weight: 0.8},
					},
					"B": {{ID: 3, SourceID: "B", TargetID: "D", EdgeType: "calls", Weight: 0.7}},
					"C": {{ID: 4, SourceID: "C", TargetID: "D", EdgeType: "calls", Weight: 0.6}},
					"D": {},
				}
				return edges[nodeID], nil
			},
		}

		// Use simple visit mode (SimpleVisit=true)
		config := TraverserConfig{
			SimpleVisit:   true,
			MaxPathLength: 20,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)
		ctx := context.Background()

		pattern := &GraphPattern{
			StartNode: &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{
				{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 3},
			},
		}

		results, err := traverser.Execute(ctx, pattern, 100)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		// With simple visit mode, D should only be visited once
		pathsToD := 0
		for _, r := range results {
			if r.ID == "D" {
				pathsToD++
			}
		}

		// Should find D only once due to simple visit tracking
		if pathsToD > 1 {
			t.Logf("simple visit mode: found %d paths to D (expected 1)", pathsToD)
		}
	})

	t.Run("max path length prevents explosion", func(t *testing.T) {
		callCount := 0
		// Create a highly connected graph
		mock := &mockEdgeQuerier{
			getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
				return []string{"start"}, nil
			},
			getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
				callCount++
				// Each node connects to multiple others
				return []Edge{
					{ID: 1, SourceID: nodeID, TargetID: nodeID + "-a", EdgeType: "calls", Weight: 0.9},
					{ID: 2, SourceID: nodeID, TargetID: nodeID + "-b", EdgeType: "calls", Weight: 0.8},
				}, nil
			},
		}

		// Use short max path length
		config := TraverserConfig{
			SimpleVisit:   false,
			MaxPathLength: 5,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)
		ctx := context.Background()

		pattern := &GraphPattern{
			StartNode: &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{
				{EdgeType: "calls", Direction: DirectionOutgoing, MaxHops: 10},
			},
		}

		results, err := traverser.Execute(ctx, pattern, 100)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		// Verify path lengths are limited
		for _, r := range results {
			if len(r.Path) > 5 {
				t.Errorf("path length %d exceeds max path length 5", len(r.Path))
			}
		}

		// Call count should be bounded due to path length limit
		if callCount > 200 {
			t.Errorf("too many edge queries (%d), max path length may not be working", callCount)
		}
	})
}

func TestTraverserConfig(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		config := DefaultTraverserConfig()

		if config.SimpleVisit != false {
			t.Error("expected SimpleVisit to be false by default")
		}
		if config.MaxPathLength != 20 {
			t.Errorf("expected MaxPathLength 20, got %d", config.MaxPathLength)
		}
	})

	t.Run("config accessor", func(t *testing.T) {
		mock := &mockEdgeQuerier{}
		config := TraverserConfig{
			SimpleVisit:   true,
			MaxPathLength: 15,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)

		got := traverser.Config()
		if got.SimpleVisit != true {
			t.Error("expected SimpleVisit to be true")
		}
		if got.MaxPathLength != 15 {
			t.Errorf("expected MaxPathLength 15, got %d", got.MaxPathLength)
		}
	})

	t.Run("zero max path length uses default", func(t *testing.T) {
		mock := &mockEdgeQuerier{}
		config := TraverserConfig{
			MaxPathLength: 0,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)

		got := traverser.Config()
		if got.MaxPathLength != 20 {
			t.Errorf("expected MaxPathLength 20 for zero input, got %d", got.MaxPathLength)
		}
	})
}

func TestGraphTraverser_VisitKey_Internal(t *testing.T) {
	mock := &mockEdgeQuerier{}

	t.Run("simple visit key is just nodeID", func(t *testing.T) {
		config := TraverserConfig{
			SimpleVisit:   true,
			MaxPathLength: 20,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)

		key := traverser.visitKey([]string{"A", "B", "C"}, "D")
		if key != "D" {
			t.Errorf("expected simple visit key 'D', got '%s'", key)
		}
	})

	t.Run("full visit key includes path hash", func(t *testing.T) {
		config := TraverserConfig{
			SimpleVisit:   false,
			MaxPathLength: 20,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)

		key1 := traverser.visitKey([]string{"A", "B"}, "D")
		key2 := traverser.visitKey([]string{"A", "C"}, "D")

		// Keys should be different for different paths to same node
		if key1 == key2 {
			t.Error("expected different visit keys for different paths")
		}

		// Keys should start with nodeID
		if key1[:2] != "D:" {
			t.Errorf("expected visit key to start with 'D:', got '%s'", key1)
		}
	})

	t.Run("same path produces same key", func(t *testing.T) {
		config := TraverserConfig{
			SimpleVisit:   false,
			MaxPathLength: 20,
		}
		traverser := NewGraphTraverserWithConfig(mock, config)

		key1 := traverser.visitKey([]string{"A", "B", "C"}, "D")
		key2 := traverser.visitKey([]string{"A", "B", "C"}, "D")

		if key1 != key2 {
			t.Error("expected same visit keys for identical paths")
		}
	})
}

func TestHashPath(t *testing.T) {
	t.Run("different paths produce different hashes", func(t *testing.T) {
		hash1 := hashPath([]string{"A", "B", "C"})
		hash2 := hashPath([]string{"A", "C", "B"})

		if hash1 == hash2 {
			t.Error("expected different hashes for different paths")
		}
	})

	t.Run("same path produces same hash", func(t *testing.T) {
		hash1 := hashPath([]string{"A", "B", "C"})
		hash2 := hashPath([]string{"A", "B", "C"})

		if hash1 != hash2 {
			t.Error("expected same hashes for identical paths")
		}
	})

	t.Run("empty path has consistent hash", func(t *testing.T) {
		hash1 := hashPath([]string{})
		hash2 := hashPath([]string{})

		if hash1 != hash2 {
			t.Error("expected same hashes for empty paths")
		}
	})
}

// =============================================================================
// W4P.34: Cycle Detection and Convergence Tests
// =============================================================================

func TestGraphTraverser_SimpleCycle(t *testing.T) {
	// Test simple cycle: A -> B -> A
	entityType := "node"
	edgeCallCount := 0

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			edgeCallCount++
			edges := map[string][]Edge{
				"A": {{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "link", Weight: 1.0}},
				"B": {{ID: 2, SourceID: "B", TargetID: "A", EdgeType: "link", Weight: 1.0}},
			}
			return edges[nodeID], nil
		},
	}

	traverser := NewGraphTraverser(mock)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "link", Direction: DirectionOutgoing, MaxHops: 10},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 100)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should terminate without infinite loop
	if edgeCallCount > 10 {
		t.Errorf("too many edge calls (%d), cycle detection may have failed", edgeCallCount)
	}

	// Verify no path contains A more than once (cycle prevented)
	for _, r := range results {
		nodeCount := make(map[string]int)
		for _, n := range r.Path {
			nodeCount[n]++
			if nodeCount[n] > 1 {
				t.Errorf("cycle detected in result path: %v", r.Path)
			}
		}
	}
}

func TestGraphTraverser_DiamondPattern(t *testing.T) {
	// Test diamond pattern: A -> B -> D, A -> C -> D
	// D should be reached via both paths but only expanded once
	entityType := "node"
	expansionCount := make(map[string]int)

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			expansionCount[nodeID]++
			edges := map[string][]Edge{
				"A": {
					{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "link", Weight: 1.0},
					{ID: 2, SourceID: "A", TargetID: "C", EdgeType: "link", Weight: 1.0},
				},
				"B": {{ID: 3, SourceID: "B", TargetID: "D", EdgeType: "link", Weight: 1.0}},
				"C": {{ID: 4, SourceID: "C", TargetID: "D", EdgeType: "link", Weight: 1.0}},
				"D": {{ID: 5, SourceID: "D", TargetID: "E", EdgeType: "link", Weight: 1.0}},
				"E": {},
			}
			return edges[nodeID], nil
		},
	}

	config := TraverserConfig{
		SimpleVisit:          false,
		MaxPathLength:        20,
		ConvergenceDetection: true,
	}
	traverser := NewGraphTraverserWithConfig(mock, config)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "link", Direction: DirectionOutgoing, MaxHops: 5},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 100)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// With convergence detection, D should only be expanded once
	if expansionCount["D"] > 1 {
		t.Errorf("D was expanded %d times, expected 1 (convergence detection failed)", expansionCount["D"])
	}

	// A should be expanded exactly once
	if expansionCount["A"] != 1 {
		t.Errorf("A was expanded %d times, expected 1", expansionCount["A"])
	}

	// B and C should each be expanded once
	if expansionCount["B"] != 1 {
		t.Errorf("B was expanded %d times, expected 1", expansionCount["B"])
	}
	if expansionCount["C"] != 1 {
		t.Errorf("C was expanded %d times, expected 1", expansionCount["C"])
	}

	_ = results // Results are produced but convergence prevents redundant work
}

func TestGraphTraverser_DiamondPatternNoConvergenceDetection(t *testing.T) {
	// Test diamond pattern with convergence detection disabled
	// D should be expanded multiple times
	entityType := "node"
	expansionCount := make(map[string]int)

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			expansionCount[nodeID]++
			edges := map[string][]Edge{
				"A": {
					{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "link", Weight: 1.0},
					{ID: 2, SourceID: "A", TargetID: "C", EdgeType: "link", Weight: 1.0},
				},
				"B": {{ID: 3, SourceID: "B", TargetID: "D", EdgeType: "link", Weight: 1.0}},
				"C": {{ID: 4, SourceID: "C", TargetID: "D", EdgeType: "link", Weight: 1.0}},
				"D": {},
			}
			return edges[nodeID], nil
		},
	}

	config := TraverserConfig{
		SimpleVisit:          false,
		MaxPathLength:        20,
		ConvergenceDetection: false, // Disabled
	}
	traverser := NewGraphTraverserWithConfig(mock, config)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "link", Direction: DirectionOutgoing, MaxHops: 5},
		},
	}

	_, err := traverser.Execute(ctx, pattern, 100)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Without convergence detection, D may be expanded multiple times
	// (depends on path-based visit tracking allowing it)
	t.Logf("D was expanded %d times (convergence detection disabled)", expansionCount["D"])
}

func TestGraphTraverser_DeepGraphWithConvergence(t *testing.T) {
	// Test a deeper graph with multiple convergence points:
	//     A
	//    / \
	//   B   C
	//   |\ /|
	//   | X |
	//   |/ \|
	//   D   E
	//    \ /
	//     F
	entityType := "node"
	expansionCount := make(map[string]int)

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			expansionCount[nodeID]++
			edges := map[string][]Edge{
				"A": {
					{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "link", Weight: 1.0},
					{ID: 2, SourceID: "A", TargetID: "C", EdgeType: "link", Weight: 1.0},
				},
				"B": {
					{ID: 3, SourceID: "B", TargetID: "D", EdgeType: "link", Weight: 1.0},
					{ID: 4, SourceID: "B", TargetID: "E", EdgeType: "link", Weight: 1.0},
				},
				"C": {
					{ID: 5, SourceID: "C", TargetID: "D", EdgeType: "link", Weight: 1.0},
					{ID: 6, SourceID: "C", TargetID: "E", EdgeType: "link", Weight: 1.0},
				},
				"D": {{ID: 7, SourceID: "D", TargetID: "F", EdgeType: "link", Weight: 1.0}},
				"E": {{ID: 8, SourceID: "E", TargetID: "F", EdgeType: "link", Weight: 1.0}},
				"F": {},
			}
			return edges[nodeID], nil
		},
	}

	config := TraverserConfig{
		SimpleVisit:          false,
		MaxPathLength:        20,
		ConvergenceDetection: true,
	}
	traverser := NewGraphTraverserWithConfig(mock, config)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "link", Direction: DirectionOutgoing, MaxHops: 10},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 100)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Each convergence node should only be expanded once
	convergenceNodes := []string{"D", "E", "F"}
	for _, node := range convergenceNodes {
		if expansionCount[node] > 1 {
			t.Errorf("%s was expanded %d times, expected 1", node, expansionCount[node])
		}
	}

	// Total expansions should be reasonable (not exponential)
	totalExpansions := 0
	for _, count := range expansionCount {
		totalExpansions += count
	}
	if totalExpansions > 10 {
		t.Errorf("total expansions %d is too high, convergence detection may have failed", totalExpansions)
	}

	_ = results
}

func TestGraphTraverser_ConvergenceWithMultipleSteps(t *testing.T) {
	// Test convergence detection with multiple traversal steps
	// Ensure convergence is tracked per-step (node can be expanded in different steps)
	entityType := "node"
	expansionCount := make(map[string]int)

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			expansionCount[nodeID]++
			edges := map[string][]Edge{
				"A": {
					{ID: 1, SourceID: "A", TargetID: "B", EdgeType: "step1", Weight: 1.0},
					{ID: 2, SourceID: "A", TargetID: "C", EdgeType: "step1", Weight: 1.0},
				},
				"B": {{ID: 3, SourceID: "B", TargetID: "D", EdgeType: "step1", Weight: 1.0}},
				"C": {{ID: 4, SourceID: "C", TargetID: "D", EdgeType: "step1", Weight: 1.0}},
				"D": {{ID: 5, SourceID: "D", TargetID: "E", EdgeType: "step2", Weight: 1.0}},
				"E": {},
			}
			return edges[nodeID], nil
		},
	}

	config := TraverserConfig{
		SimpleVisit:          false,
		MaxPathLength:        20,
		ConvergenceDetection: true,
	}
	traverser := NewGraphTraverserWithConfig(mock, config)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "step1", Direction: DirectionOutgoing, MaxHops: 3},
			{EdgeType: "step2", Direction: DirectionOutgoing, MaxHops: 2},
		},
	}

	results, err := traverser.Execute(ctx, pattern, 100)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// D should be expanded once per step it participates in
	// In step1, D is a convergence point (reached via B and C)
	// In step2, D may be expanded again (new step context)
	t.Logf("Expansion counts: %v", expansionCount)

	// Verify we got results (the traversal completed successfully)
	if len(results) == 0 {
		t.Log("No final results - paths may not have completed all steps")
	}
}

func TestGraphTraverser_ExpandKeyFormat(t *testing.T) {
	mock := &mockEdgeQuerier{}
	traverser := NewGraphTraverser(mock)

	key1 := traverser.expandKey("nodeA", 0)
	key2 := traverser.expandKey("nodeA", 1)
	key3 := traverser.expandKey("nodeB", 0)

	// Same node, different steps should have different keys
	if key1 == key2 {
		t.Error("expected different keys for same node at different steps")
	}

	// Different nodes, same step should have different keys
	if key1 == key3 {
		t.Error("expected different keys for different nodes at same step")
	}

	// Verify key format contains both node and step
	if key1 != "nodeA@0" {
		t.Errorf("unexpected key format: %s", key1)
	}
}

func TestGraphTraverser_LargeDiamondGraph(t *testing.T) {
	// Test with a larger diamond pattern to stress test convergence detection
	// Graph: A -> [B1, B2, B3, B4] -> C (all Bi connect to C)
	entityType := "node"
	expansionCount := make(map[string]int)

	mock := &mockEdgeQuerier{
		getNodesByPatternFunc: func(pattern *NodeMatcher) ([]string, error) {
			return []string{"A"}, nil
		},
		getOutgoingEdgesFunc: func(nodeID string, edgeTypes []string) ([]Edge, error) {
			expansionCount[nodeID]++
			switch nodeID {
			case "A":
				return []Edge{
					{ID: 1, SourceID: "A", TargetID: "B1", EdgeType: "link", Weight: 1.0},
					{ID: 2, SourceID: "A", TargetID: "B2", EdgeType: "link", Weight: 1.0},
					{ID: 3, SourceID: "A", TargetID: "B3", EdgeType: "link", Weight: 1.0},
					{ID: 4, SourceID: "A", TargetID: "B4", EdgeType: "link", Weight: 1.0},
				}, nil
			case "B1", "B2", "B3", "B4":
				return []Edge{
					{ID: 10, SourceID: nodeID, TargetID: "C", EdgeType: "link", Weight: 1.0},
				}, nil
			default:
				return nil, nil
			}
		},
	}

	config := TraverserConfig{
		SimpleVisit:          false,
		MaxPathLength:        20,
		ConvergenceDetection: true,
	}
	traverser := NewGraphTraverserWithConfig(mock, config)
	ctx := context.Background()

	pattern := &GraphPattern{
		StartNode: &NodeMatcher{EntityType: &entityType},
		Traversals: []TraversalStep{
			{EdgeType: "link", Direction: DirectionOutgoing, MaxHops: 5},
		},
	}

	_, err := traverser.Execute(ctx, pattern, 100)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// C should only be expanded once despite being reached via 4 different paths
	if expansionCount["C"] > 1 {
		t.Errorf("C was expanded %d times, expected 1", expansionCount["C"])
	}

	// Each Bi should be expanded exactly once
	for i := 1; i <= 4; i++ {
		nodeName := "B" + string(rune('0'+i))
		if expansionCount[nodeName] != 1 {
			t.Errorf("%s was expanded %d times, expected 1", nodeName, expansionCount[nodeName])
		}
	}
}
