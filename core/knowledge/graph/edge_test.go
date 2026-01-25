package graph

import (
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func TestEdge_MarshalUnmarshalBinary(t *testing.T) {
	edge := Edge{
		SourceID:  100,
		TargetID:  200,
		Type:      vectorgraphdb.EdgeTypeCalls,
		Weight:    0.75,
		SessionID: "session-xyz-789",
		AgentID:   5,
		CreatedAt: 1234567890,
		UpdatedAt: 1234567899,
	}

	data := edge.MarshalBinary()

	var restored Edge
	if err := restored.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.SourceID != edge.SourceID {
		t.Errorf("SourceID: got %d, want %d", restored.SourceID, edge.SourceID)
	}
	if restored.TargetID != edge.TargetID {
		t.Errorf("TargetID: got %d, want %d", restored.TargetID, edge.TargetID)
	}
	if restored.Type != edge.Type {
		t.Errorf("Type: got %v, want %v", restored.Type, edge.Type)
	}
	if restored.SessionID != edge.SessionID {
		t.Errorf("SessionID: got %q, want %q", restored.SessionID, edge.SessionID)
	}
	if restored.AgentID != edge.AgentID {
		t.Errorf("AgentID: got %d, want %d", restored.AgentID, edge.AgentID)
	}
	if restored.CreatedAt != edge.CreatedAt {
		t.Errorf("CreatedAt: got %d, want %d", restored.CreatedAt, edge.CreatedAt)
	}
	if restored.UpdatedAt != edge.UpdatedAt {
		t.Errorf("UpdatedAt: got %d, want %d", restored.UpdatedAt, edge.UpdatedAt)
	}
}

func TestEdge_MarshalUnmarshalEmpty(t *testing.T) {
	edge := Edge{
		SourceID: 1,
		TargetID: 2,
		Type:     vectorgraphdb.EdgeTypeDefines,
	}

	data := edge.MarshalBinary()

	var restored Edge
	if err := restored.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.SourceID != edge.SourceID || restored.TargetID != edge.TargetID {
		t.Error("basic fields mismatch")
	}
	if restored.SessionID != "" {
		t.Errorf("expected empty session, got %q", restored.SessionID)
	}
}

func TestEdge_UnmarshalInvalidData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"too short", []byte{1, 2, 3}},
		{"truncated", make([]byte, 30)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var edge Edge
			if err := edge.UnmarshalBinary(tc.data); err == nil {
				t.Error("expected error for invalid data")
			}
		})
	}
}

func TestEdgeKey(t *testing.T) {
	key := makeEdgeKey(100, 200, vectorgraphdb.EdgeTypeCalls)

	if key.SourceID() != 100 {
		t.Errorf("SourceID: got %d, want 100", key.SourceID())
	}
	if key.TargetID() != 200 {
		t.Errorf("TargetID: got %d, want 200", key.TargetID())
	}
	if key.EdgeType() != vectorgraphdb.EdgeTypeCalls {
		t.Errorf("EdgeType: got %v, want Calls", key.EdgeType())
	}
}

func TestEdgeKey_Uniqueness(t *testing.T) {
	key1 := makeEdgeKey(1, 2, vectorgraphdb.EdgeTypeCalls)
	key2 := makeEdgeKey(1, 2, vectorgraphdb.EdgeTypeCalls)
	key3 := makeEdgeKey(1, 2, vectorgraphdb.EdgeTypeImports)
	key4 := makeEdgeKey(2, 1, vectorgraphdb.EdgeTypeCalls)

	if key1 != key2 {
		t.Error("identical keys should be equal")
	}
	if key1 == key3 {
		t.Error("different types should produce different keys")
	}
	if key1 == key4 {
		t.Error("swapped src/dst should produce different keys")
	}
}

func BenchmarkEdge_MarshalBinary(b *testing.B) {
	edge := Edge{
		SourceID:  100,
		TargetID:  200,
		Type:      vectorgraphdb.EdgeTypeCalls,
		Weight:    0.75,
		SessionID: "session-benchmark-001",
		AgentID:   5,
		CreatedAt: 1234567890,
		UpdatedAt: 1234567899,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = edge.MarshalBinary()
	}
}

func BenchmarkEdge_UnmarshalBinary(b *testing.B) {
	edge := Edge{
		SourceID:  100,
		TargetID:  200,
		Type:      vectorgraphdb.EdgeTypeCalls,
		Weight:    0.75,
		SessionID: "session-benchmark-001",
		AgentID:   5,
		CreatedAt: 1234567890,
		UpdatedAt: 1234567899,
	}
	data := edge.MarshalBinary()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var e Edge
		_ = e.UnmarshalBinary(data)
	}
}
