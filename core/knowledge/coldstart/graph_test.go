package coldstart

import (
	"math"
	"testing"
)

func TestBuildAdjacencyGraph(t *testing.T) {
	edges := []Edge{
		{SourceID: "A", TargetID: "B"},
		{SourceID: "A", TargetID: "C"},
		{SourceID: "B", TargetID: "C"},
	}

	graph := buildAdjacencyGraph(edges)

	if len(graph["A"]) != 2 {
		t.Errorf("Node A should have 2 neighbors, got %d", len(graph["A"]))
	}
	if len(graph["B"]) != 1 {
		t.Errorf("Node B should have 1 neighbor, got %d", len(graph["B"]))
	}
}

func TestComputePageRank_SimpleGraph(t *testing.T) {
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}

	ranks := ComputePageRank(graph, 0.85, 100)

	// All nodes should have equal rank in a cycle
	if len(ranks) != 3 {
		t.Fatalf("Expected 3 nodes, got %d", len(ranks))
	}

	sum := 0.0
	for _, rank := range ranks {
		sum += rank
		if rank < 0 || rank > 1 {
			t.Errorf("PageRank should be in [0,1], got %f", rank)
		}
	}

	// Sum of PageRank should be approximately 1.0
	if math.Abs(sum-1.0) > 0.01 {
		t.Errorf("Sum of PageRank should be ~1.0, got %f", sum)
	}
}

func TestComputePageRank_EmptyGraph(t *testing.T) {
	graph := map[string][]string{}
	ranks := ComputePageRank(graph, 0.85, 100)

	if len(ranks) != 0 {
		t.Errorf("Empty graph should return empty ranks, got %d", len(ranks))
	}
}

func TestComputePageRank_Convergence(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"C"},
		"C": {},
	}

	ranks := ComputePageRank(graph, 0.85, 100)

	// Node C should have highest rank (sink node)
	if ranks["C"] < ranks["A"] || ranks["C"] < ranks["B"] {
		t.Errorf("Sink node C should have highest rank, got A=%f, B=%f, C=%f",
			ranks["A"], ranks["B"], ranks["C"])
	}
}

func TestComputeClusteringCoefficients_Triangle(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"A", "C"},
		"C": {"A", "B"},
	}

	coeffs := ComputeClusteringCoefficients(graph)

	// All nodes in a complete triangle should have coefficient 1.0
	for node, coeff := range coeffs {
		if math.Abs(coeff-1.0) > 0.01 {
			t.Errorf("Node %s should have clustering coefficient ~1.0, got %f", node, coeff)
		}
	}
}

func TestComputeClusteringCoefficients_NoTriangles(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {},
		"C": {},
	}

	coeffs := ComputeClusteringCoefficients(graph)

	// No triangles means coefficient should be 0
	if coeffs["A"] != 0.0 {
		t.Errorf("Node A should have clustering coefficient 0.0, got %f", coeffs["A"])
	}
}

func TestComputeClusteringCoefficients_InRange(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}

	coeffs := ComputeClusteringCoefficients(graph)

	for node, coeff := range coeffs {
		if coeff < 0.0 || coeff > 1.0 {
			t.Errorf("Node %s clustering coefficient should be in [0,1], got %f", node, coeff)
		}
	}
}

func TestExtractNodes(t *testing.T) {
	graph := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
	}

	nodes := extractNodes(graph)

	// Should have 4 unique nodes: A, B, C, D
	if len(nodes) != 4 {
		t.Errorf("Expected 4 nodes, got %d", len(nodes))
	}

	nodeSet := make(map[string]bool)
	for _, n := range nodes {
		nodeSet[n] = true
	}

	expected := []string{"A", "B", "C", "D"}
	for _, e := range expected {
		if !nodeSet[e] {
			t.Errorf("Expected node %s not found", e)
		}
	}
}

func TestHasConverged(t *testing.T) {
	tests := []struct {
		name      string
		old       map[string]float64
		new       map[string]float64
		converged bool
	}{
		{
			name:      "identical",
			old:       map[string]float64{"A": 0.5, "B": 0.5},
			new:       map[string]float64{"A": 0.5, "B": 0.5},
			converged: true,
		},
		{
			name:      "small change",
			old:       map[string]float64{"A": 0.5, "B": 0.5},
			new:       map[string]float64{"A": 0.5000001, "B": 0.5},
			converged: true,
		},
		{
			name:      "large change",
			old:       map[string]float64{"A": 0.5, "B": 0.5},
			new:       map[string]float64{"A": 0.6, "B": 0.5},
			converged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasConverged(tt.old, tt.new)
			if got != tt.converged {
				t.Errorf("hasConverged() = %v, want %v", got, tt.converged)
			}
		})
	}
}
