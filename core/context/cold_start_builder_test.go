package context

import (
	"testing"
)

func TestNewColdStartBuilder(t *testing.T) {
	builder := NewColdStartBuilder()

	if builder == nil {
		t.Fatal("NewColdStartBuilder() returned nil")
	}
	if builder.nodeSignals == nil {
		t.Error("nodeSignals map should be initialized")
	}
	if builder.entityCounts == nil {
		t.Error("entityCounts map should be initialized")
	}
	if builder.domainCounts == nil {
		t.Error("domainCounts map should be initialized")
	}
}

func TestColdStartBuilder_AddNode(t *testing.T) {
	builder := NewColdStartBuilder()

	builder.AddNode("node1", "function", 0)
	builder.AddNode("node2", "struct", 0)
	builder.AddNode("node3", "function", 2)

	if builder.totalNodes != 3 {
		t.Errorf("totalNodes = %d, want 3", builder.totalNodes)
	}

	if builder.entityCounts["function"] != 2 {
		t.Errorf("function count = %d, want 2", builder.entityCounts["function"])
	}

	if builder.entityCounts["struct"] != 1 {
		t.Errorf("struct count = %d, want 1", builder.entityCounts["struct"])
	}

	if builder.domainCounts[0] != 2 {
		t.Errorf("domain 0 count = %d, want 2", builder.domainCounts[0])
	}

	if builder.domainCounts[2] != 1 {
		t.Errorf("domain 2 count = %d, want 1", builder.domainCounts[2])
	}

	signals := builder.GetSignals("node1")
	if signals == nil {
		t.Fatal("GetSignals returned nil")
	}
	if signals.NodeID != "node1" {
		t.Errorf("NodeID = %s, want node1", signals.NodeID)
	}
	if signals.EntityType != "function" {
		t.Errorf("EntityType = %s, want function", signals.EntityType)
	}
}

func TestColdStartBuilder_AddEdge(t *testing.T) {
	builder := NewColdStartBuilder()

	builder.AddNode("A", "function", 0)
	builder.AddNode("B", "function", 0)
	builder.AddNode("C", "function", 0)

	builder.AddEdge("A", "B")
	builder.AddEdge("A", "C")
	builder.AddEdge("B", "C")

	if len(builder.edges) != 3 {
		t.Errorf("edges count = %d, want 3", len(builder.edges))
	}

	if builder.outDegrees["A"] != 2 {
		t.Errorf("A out-degree = %d, want 2", builder.outDegrees["A"])
	}

	if builder.inDegrees["C"] != 2 {
		t.Errorf("C in-degree = %d, want 2", builder.inDegrees["C"])
	}
}

func TestColdStartBuilder_BuildProfile(t *testing.T) {
	builder := NewColdStartBuilder()

	builder.AddNode("A", "function", 0)
	builder.AddNode("B", "struct", 0)
	builder.AddNode("C", "function", 2)

	builder.AddEdge("A", "B")
	builder.AddEdge("B", "C")
	builder.AddEdge("C", "A")

	profile := builder.BuildProfile()

	if profile == nil {
		t.Fatal("BuildProfile() returned nil")
	}

	if profile.TotalNodes != 3 {
		t.Errorf("TotalNodes = %d, want 3", profile.TotalNodes)
	}

	if profile.EntityCounts["function"] != 2 {
		t.Errorf("function count in profile = %d, want 2", profile.EntityCounts["function"])
	}

	if profile.DomainCounts[0] != 2 {
		t.Errorf("domain 0 count in profile = %d, want 2", profile.DomainCounts[0])
	}

	if profile.AvgInDegree <= 0 {
		t.Error("AvgInDegree should be > 0")
	}

	if profile.AvgOutDegree <= 0 {
		t.Error("AvgOutDegree should be > 0")
	}

	// Verify signals were computed
	signalsA := builder.GetSignals("A")
	if signalsA == nil {
		t.Fatal("Signals for A not found")
	}

	if signalsA.InDegree != 1 {
		t.Errorf("A InDegree = %d, want 1", signalsA.InDegree)
	}

	if signalsA.OutDegree != 1 {
		t.Errorf("A OutDegree = %d, want 1", signalsA.OutDegree)
	}

	if signalsA.PageRank == 0.0 {
		t.Error("PageRank should be computed and non-zero")
	}

	if signalsA.TypeFrequency == 0.0 {
		t.Error("TypeFrequency should be computed and non-zero")
	}
}

func TestColdStartBuilder_EmptyGraph(t *testing.T) {
	builder := NewColdStartBuilder()
	profile := builder.BuildProfile()

	if profile == nil {
		t.Fatal("BuildProfile() returned nil")
	}

	if profile.TotalNodes != 0 {
		t.Errorf("TotalNodes = %d, want 0", profile.TotalNodes)
	}

	if profile.AvgInDegree != 0 {
		t.Errorf("AvgInDegree = %f, want 0", profile.AvgInDegree)
	}
}

func TestColdStartBuilder_TypeSignals(t *testing.T) {
	builder := NewColdStartBuilder()

	// Add nodes with different types
	builder.AddNode("A", "function", 0)
	builder.AddNode("B", "function", 0)
	builder.AddNode("C", "function", 0)
	builder.AddNode("D", "struct", 0)

	profile := builder.BuildProfile()
	if profile == nil {
		t.Fatal("BuildProfile() returned nil")
	}

	signalsFunc := builder.GetSignals("A")
	signalsStruct := builder.GetSignals("D")

	// 3 functions out of 4 total = 0.75 frequency
	if signalsFunc.TypeFrequency != 0.75 {
		t.Errorf("function TypeFrequency = %f, want 0.75", signalsFunc.TypeFrequency)
	}

	// Rarity = 1 - Frequency
	if signalsFunc.TypeRarity != 0.25 {
		t.Errorf("function TypeRarity = %f, want 0.25", signalsFunc.TypeRarity)
	}

	// 1 struct out of 4 total = 0.25 frequency
	if signalsStruct.TypeFrequency != 0.25 {
		t.Errorf("struct TypeFrequency = %f, want 0.25", signalsStruct.TypeFrequency)
	}

	if signalsStruct.TypeRarity != 0.75 {
		t.Errorf("struct TypeRarity = %f, want 0.75", signalsStruct.TypeRarity)
	}
}

func TestColdStartBuilder_PageRankComputation(t *testing.T) {
	builder := NewColdStartBuilder()

	// Create a simple graph: A -> B -> C
	builder.AddNode("A", "function", 0)
	builder.AddNode("B", "function", 0)
	builder.AddNode("C", "function", 0)

	builder.AddEdge("A", "B")
	builder.AddEdge("B", "C")

	profile := builder.BuildProfile()
	if profile == nil {
		t.Fatal("BuildProfile() returned nil")
	}

	// C should have highest PageRank (sink node)
	signalsA := builder.GetSignals("A")
	signalsC := builder.GetSignals("C")

	if signalsC.PageRank <= signalsA.PageRank {
		t.Errorf("Sink node C PageRank (%f) should be > source node A PageRank (%f)",
			signalsC.PageRank, signalsA.PageRank)
	}

	// Verify PageRank in valid range
	for _, nodeID := range []string{"A", "B", "C"} {
		signals := builder.GetSignals(nodeID)
		if signals.PageRank < 0 || signals.PageRank > 1 {
			t.Errorf("Node %s PageRank %f should be in [0,1]", nodeID, signals.PageRank)
		}
	}
}
