package inference

import (
	"testing"
)

// =============================================================================
// Edge Type Tests
// =============================================================================

func TestNewEdge(t *testing.T) {
	edge := NewEdge("source", "predicate", "target")

	if edge.Source != "source" {
		t.Errorf("Expected Source 'source', got '%s'", edge.Source)
	}
	if edge.Predicate != "predicate" {
		t.Errorf("Expected Predicate 'predicate', got '%s'", edge.Predicate)
	}
	if edge.Target != "target" {
		t.Errorf("Expected Target 'target', got '%s'", edge.Target)
	}
}

func TestEdge_Key(t *testing.T) {
	edge := Edge{
		Source:    "nodeA",
		Predicate: "calls",
		Target:    "nodeB",
	}

	key := edge.Key()
	expected := "nodeA|calls|nodeB"

	if key != expected {
		t.Errorf("Expected key '%s', got '%s'", expected, key)
	}
}

func TestEdge_Key_Uniqueness(t *testing.T) {
	edge1 := Edge{Source: "A", Predicate: "rel", Target: "B"}
	edge2 := Edge{Source: "A", Predicate: "rel", Target: "C"}
	edge3 := Edge{Source: "A", Predicate: "other", Target: "B"}

	if edge1.Key() == edge2.Key() {
		t.Error("Different targets should produce different keys")
	}
	if edge1.Key() == edge3.Key() {
		t.Error("Different predicates should produce different keys")
	}
}

func TestEdge_String(t *testing.T) {
	edge := Edge{
		Source:    "funcA",
		Predicate: "calls",
		Target:    "funcB",
	}

	str := edge.String()
	expected := "funcA-calls->funcB"

	if str != expected {
		t.Errorf("Expected '%s', got '%s'", expected, str)
	}
}

func TestEdge_Equals(t *testing.T) {
	edge1 := Edge{Source: "A", Predicate: "rel", Target: "B"}
	edge2 := Edge{Source: "A", Predicate: "rel", Target: "B"}
	edge3 := Edge{Source: "A", Predicate: "rel", Target: "C"}

	if !edge1.Equals(edge2) {
		t.Error("Identical edges should be equal")
	}
	if edge1.Equals(edge3) {
		t.Error("Different edges should not be equal")
	}
}

func TestEdge_Equals_AllFieldsDifferent(t *testing.T) {
	edge1 := Edge{Source: "A", Predicate: "rel", Target: "B"}

	tests := []struct {
		name  string
		edge  Edge
		equal bool
	}{
		{"same", Edge{Source: "A", Predicate: "rel", Target: "B"}, true},
		{"diff source", Edge{Source: "X", Predicate: "rel", Target: "B"}, false},
		{"diff predicate", Edge{Source: "A", Predicate: "other", Target: "B"}, false},
		{"diff target", Edge{Source: "A", Predicate: "rel", Target: "X"}, false},
		{"all different", Edge{Source: "X", Predicate: "Y", Target: "Z"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := edge1.Equals(tt.edge)
			if result != tt.equal {
				t.Errorf("Expected Equals=%v, got %v", tt.equal, result)
			}
		})
	}
}

func TestEdge_ToEvidenceEdge(t *testing.T) {
	edge := Edge{
		Source:    "nodeA",
		Predicate: "imports",
		Target:    "nodeB",
	}

	evidence := edge.ToEvidenceEdge()

	if evidence.SourceID != "nodeA" {
		t.Errorf("Expected SourceID 'nodeA', got '%s'", evidence.SourceID)
	}
	if evidence.TargetID != "nodeB" {
		t.Errorf("Expected TargetID 'nodeB', got '%s'", evidence.TargetID)
	}
	if evidence.EdgeType != "imports" {
		t.Errorf("Expected EdgeType 'imports', got '%s'", evidence.EdgeType)
	}
}

func TestEdge_ToDerivedEdge(t *testing.T) {
	edge := Edge{
		Source:    "funcMain",
		Predicate: "calls",
		Target:    "funcHelper",
	}

	derived := edge.ToDerivedEdge()

	if derived.SourceID != "funcMain" {
		t.Errorf("Expected SourceID 'funcMain', got '%s'", derived.SourceID)
	}
	if derived.TargetID != "funcHelper" {
		t.Errorf("Expected TargetID 'funcHelper', got '%s'", derived.TargetID)
	}
	if derived.EdgeType != "calls" {
		t.Errorf("Expected EdgeType 'calls', got '%s'", derived.EdgeType)
	}
}

func TestEdgeFromEvidenceEdge(t *testing.T) {
	evidence := EvidenceEdge{
		SourceID: "pkgA",
		TargetID: "pkgB",
		EdgeType: "depends",
	}

	edge := EdgeFromEvidenceEdge(evidence)

	if edge.Source != "pkgA" {
		t.Errorf("Expected Source 'pkgA', got '%s'", edge.Source)
	}
	if edge.Target != "pkgB" {
		t.Errorf("Expected Target 'pkgB', got '%s'", edge.Target)
	}
	if edge.Predicate != "depends" {
		t.Errorf("Expected Predicate 'depends', got '%s'", edge.Predicate)
	}
}

func TestEdgeFromDerivedEdge(t *testing.T) {
	derived := DerivedEdge{
		SourceID: "classA",
		TargetID: "classB",
		EdgeType: "extends",
	}

	edge := EdgeFromDerivedEdge(derived)

	if edge.Source != "classA" {
		t.Errorf("Expected Source 'classA', got '%s'", edge.Source)
	}
	if edge.Target != "classB" {
		t.Errorf("Expected Target 'classB', got '%s'", edge.Target)
	}
	if edge.Predicate != "extends" {
		t.Errorf("Expected Predicate 'extends', got '%s'", edge.Predicate)
	}
}

func TestEdge_RoundTrip_Evidence(t *testing.T) {
	original := Edge{
		Source:    "node1",
		Predicate: "relation",
		Target:    "node2",
	}

	// Convert to evidence and back
	evidence := original.ToEvidenceEdge()
	restored := EdgeFromEvidenceEdge(evidence)

	if !original.Equals(restored) {
		t.Errorf("Round-trip failed: original=%+v, restored=%+v", original, restored)
	}
}

func TestEdge_RoundTrip_Derived(t *testing.T) {
	original := Edge{
		Source:    "func1",
		Predicate: "calls",
		Target:    "func2",
	}

	// Convert to derived and back
	derived := original.ToDerivedEdge()
	restored := EdgeFromDerivedEdge(derived)

	if !original.Equals(restored) {
		t.Errorf("Round-trip failed: original=%+v, restored=%+v", original, restored)
	}
}

func TestEdge_EmptyFields(t *testing.T) {
	edge := Edge{
		Source:    "",
		Predicate: "",
		Target:    "",
	}

	key := edge.Key()
	if key != "||" {
		t.Errorf("Expected key '||' for empty fields, got '%s'", key)
	}

	str := edge.String()
	if str != "-->" {
		t.Errorf("Expected '-->' for empty fields, got '%s'", str)
	}
}

func TestEdge_SpecialCharacters(t *testing.T) {
	edge := Edge{
		Source:    "node:with:colons",
		Predicate: "has|pipe",
		Target:    "target->arrow",
	}

	// Key should still work (though might cause issues if used as map key carelessly)
	key := edge.Key()
	if key == "" {
		t.Error("Key should not be empty")
	}

	// String representation should be readable
	str := edge.String()
	if str == "" {
		t.Error("String should not be empty")
	}
}
