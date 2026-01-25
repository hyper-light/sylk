package graph

import (
	"bytes"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func TestNode_MarshalUnmarshalBinary(t *testing.T) {
	node := Node{
		ID:          12345,
		Domain:      vectorgraphdb.DomainCode,
		Type:        vectorgraphdb.NodeTypeFunction,
		Name:        "TestFunction",
		Path:        "/path/to/file.go",
		Package:     "graph",
		Signature:   "func TestFunction(t *testing.T)",
		Content:     []byte("func TestFunction(t *testing.T) { }"),
		ContentHash: 0xDEADBEEF,
		CreatedAt:   1234567890,
		SessionID:   "session-abc-123",
		CreatedBy:   42,
	}

	data := node.MarshalBinary()

	var restored Node
	if err := restored.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.ID != node.ID {
		t.Errorf("ID: got %d, want %d", restored.ID, node.ID)
	}
	if restored.Domain != node.Domain {
		t.Errorf("Domain: got %v, want %v", restored.Domain, node.Domain)
	}
	if restored.Type != node.Type {
		t.Errorf("Type: got %v, want %v", restored.Type, node.Type)
	}
	if restored.Name != node.Name {
		t.Errorf("Name: got %q, want %q", restored.Name, node.Name)
	}
	if restored.Path != node.Path {
		t.Errorf("Path: got %q, want %q", restored.Path, node.Path)
	}
	if restored.Package != node.Package {
		t.Errorf("Package: got %q, want %q", restored.Package, node.Package)
	}
	if restored.Signature != node.Signature {
		t.Errorf("Signature: got %q, want %q", restored.Signature, node.Signature)
	}
	if !bytes.Equal(restored.Content, node.Content) {
		t.Errorf("Content: got %q, want %q", restored.Content, node.Content)
	}
	if restored.ContentHash != node.ContentHash {
		t.Errorf("ContentHash: got %x, want %x", restored.ContentHash, node.ContentHash)
	}
	if restored.CreatedAt != node.CreatedAt {
		t.Errorf("CreatedAt: got %d, want %d", restored.CreatedAt, node.CreatedAt)
	}
	if restored.SessionID != node.SessionID {
		t.Errorf("SessionID: got %q, want %q", restored.SessionID, node.SessionID)
	}
	if restored.CreatedBy != node.CreatedBy {
		t.Errorf("CreatedBy: got %d, want %d", restored.CreatedBy, node.CreatedBy)
	}
}

func TestNode_MarshalUnmarshalEmpty(t *testing.T) {
	node := Node{
		ID:     1,
		Domain: vectorgraphdb.DomainHistory,
		Type:   vectorgraphdb.NodeTypeSession,
	}

	data := node.MarshalBinary()

	var restored Node
	if err := restored.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.ID != node.ID {
		t.Errorf("ID: got %d, want %d", restored.ID, node.ID)
	}
	if restored.Name != "" || restored.Path != "" || restored.Package != "" {
		t.Error("expected empty strings for unset fields")
	}
	if len(restored.Content) != 0 {
		t.Error("expected empty content")
	}
}

func TestNode_UnmarshalInvalidData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"too short", []byte{1, 2, 3}},
		{"truncated fixed", make([]byte, nodeFixedSize-1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var node Node
			if err := node.UnmarshalBinary(tc.data); err == nil {
				t.Error("expected error for invalid data")
			}
		})
	}
}

func TestNode_ContentHash(t *testing.T) {
	node := Node{
		Content: []byte("test content for hashing"),
	}

	hash1 := node.ComputeContentHash()
	hash2 := node.ComputeContentHash()

	if hash1 != hash2 {
		t.Error("hash should be deterministic")
	}

	node.UpdateContentHash()
	if node.ContentHash != hash1 {
		t.Error("UpdateContentHash should set ContentHash field")
	}

	node.Content = []byte("different content")
	hash3 := node.ComputeContentHash()
	if hash1 == hash3 {
		t.Error("different content should produce different hash")
	}
}

func TestNode_LargeContent(t *testing.T) {
	largeContent := make([]byte, 1024*1024)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	node := Node{
		ID:      999,
		Content: largeContent,
	}

	data := node.MarshalBinary()

	var restored Node
	if err := restored.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !bytes.Equal(restored.Content, node.Content) {
		t.Error("large content mismatch")
	}
}

func BenchmarkNode_MarshalBinary(b *testing.B) {
	node := Node{
		ID:        12345,
		Domain:    vectorgraphdb.DomainCode,
		Type:      vectorgraphdb.NodeTypeFunction,
		Name:      "BenchmarkFunction",
		Path:      "/path/to/benchmark/file.go",
		Package:   "graph",
		Signature: "func BenchmarkFunction(b *testing.B)",
		Content:   bytes.Repeat([]byte("x"), 1000),
		SessionID: "session-bench-001",
		CreatedBy: 1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = node.MarshalBinary()
	}
}

func BenchmarkNode_UnmarshalBinary(b *testing.B) {
	node := Node{
		ID:        12345,
		Domain:    vectorgraphdb.DomainCode,
		Type:      vectorgraphdb.NodeTypeFunction,
		Name:      "BenchmarkFunction",
		Path:      "/path/to/benchmark/file.go",
		Package:   "graph",
		Signature: "func BenchmarkFunction(b *testing.B)",
		Content:   bytes.Repeat([]byte("x"), 1000),
		SessionID: "session-bench-001",
		CreatedBy: 1,
	}
	data := node.MarshalBinary()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n Node
		_ = n.UnmarshalBinary(data)
	}
}
