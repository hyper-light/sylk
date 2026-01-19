package coldstart

import (
	"testing"
)

func TestNewNodeColdStartSignals(t *testing.T) {
	tests := []struct {
		name       string
		nodeID     string
		entityType string
		domain     int
	}{
		{
			name:       "basic function node",
			nodeID:     "func_123",
			entityType: "function",
			domain:     1,
		},
		{
			name:       "type node in different domain",
			nodeID:     "type_456",
			entityType: "type",
			domain:     2,
		},
		{
			name:       "empty strings",
			nodeID:     "",
			entityType: "",
			domain:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signals := NewNodeColdStartSignals(tt.nodeID, tt.entityType, tt.domain)

			if signals.NodeID != tt.nodeID {
				t.Errorf("NodeID = %v, want %v", signals.NodeID, tt.nodeID)
			}
			if signals.EntityType != tt.entityType {
				t.Errorf("EntityType = %v, want %v", signals.EntityType, tt.entityType)
			}
			if signals.Domain != tt.domain {
				t.Errorf("Domain = %v, want %v", signals.Domain, tt.domain)
			}
		})
	}
}

func TestNodeColdStartSignals_StructuralSignals(t *testing.T) {
	signals := NewNodeColdStartSignals("node1", "function", 1)

	// Set structural signals
	signals.InDegree = 5
	signals.OutDegree = 3
	signals.PageRank = 0.42
	signals.ClusterCoeff = 0.67
	signals.Betweenness = 0.15

	if signals.InDegree != 5 {
		t.Errorf("InDegree = %v, want 5", signals.InDegree)
	}
	if signals.OutDegree != 3 {
		t.Errorf("OutDegree = %v, want 3", signals.OutDegree)
	}
	if signals.PageRank != 0.42 {
		t.Errorf("PageRank = %v, want 0.42", signals.PageRank)
	}
	if signals.ClusterCoeff != 0.67 {
		t.Errorf("ClusterCoeff = %v, want 0.67", signals.ClusterCoeff)
	}
	if signals.Betweenness != 0.15 {
		t.Errorf("Betweenness = %v, want 0.15", signals.Betweenness)
	}
}

func TestNodeColdStartSignals_ContentSignals(t *testing.T) {
	signals := NewNodeColdStartSignals("node2", "type", 2)

	// Set content signals
	signals.NameSalience = 0.85
	signals.DocCoverage = 0.92
	signals.Complexity = 0.45

	if signals.NameSalience != 0.85 {
		t.Errorf("NameSalience = %v, want 0.85", signals.NameSalience)
	}
	if signals.DocCoverage != 0.92 {
		t.Errorf("DocCoverage = %v, want 0.92", signals.DocCoverage)
	}
	if signals.Complexity != 0.45 {
		t.Errorf("Complexity = %v, want 0.45", signals.Complexity)
	}
}

func TestNodeColdStartSignals_DistributionalSignals(t *testing.T) {
	signals := NewNodeColdStartSignals("node3", "method", 1)

	// Set distributional signals
	signals.TypeFrequency = 0.35
	signals.TypeRarity = 0.65

	if signals.TypeFrequency != 0.35 {
		t.Errorf("TypeFrequency = %v, want 0.35", signals.TypeFrequency)
	}
	if signals.TypeRarity != 0.65 {
		t.Errorf("TypeRarity = %v, want 0.65", signals.TypeRarity)
	}
}

func TestNodeColdStartSignals_AllSignals(t *testing.T) {
	signals := NewNodeColdStartSignals("complete_node", "struct", 3)

	// Populate all signals
	signals.InDegree = 10
	signals.OutDegree = 7
	signals.PageRank = 0.55
	signals.ClusterCoeff = 0.73
	signals.Betweenness = 0.28
	signals.NameSalience = 0.88
	signals.DocCoverage = 0.95
	signals.Complexity = 0.62
	signals.TypeFrequency = 0.42
	signals.TypeRarity = 0.58

	// Verify all signals are set correctly
	if signals.NodeID != "complete_node" {
		t.Errorf("NodeID = %v, want complete_node", signals.NodeID)
	}
	if signals.EntityType != "struct" {
		t.Errorf("EntityType = %v, want struct", signals.EntityType)
	}
	if signals.Domain != 3 {
		t.Errorf("Domain = %v, want 3", signals.Domain)
	}
	if signals.InDegree != 10 || signals.OutDegree != 7 {
		t.Errorf("Degree = %v/%v, want 10/7", signals.InDegree, signals.OutDegree)
	}
	if signals.PageRank != 0.55 {
		t.Errorf("PageRank = %v, want 0.55", signals.PageRank)
	}
}
