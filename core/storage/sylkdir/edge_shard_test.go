package sylkdir

import (
	"testing"
	"time"
)

func makeTestEdge(src, dst uint32, typ uint8) *Edge {
	now := uint64(time.Now().UnixNano())
	return &Edge{
		SourceID:  src,
		TargetID:  dst,
		Type:      typ,
		Weight:    0.5,
		SessionID: 1,
		AgentID:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestEdgeMarshalUnmarshal(t *testing.T) {
	original := makeTestEdge(100, 200, 1)
	original.Weight = 0.75

	data := original.MarshalBinary()
	if len(data) != EdgeRecordSize {
		t.Errorf("MarshalBinary length = %d, want %d", len(data), EdgeRecordSize)
	}

	decoded := &Edge{}
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	if decoded.SourceID != original.SourceID {
		t.Errorf("SourceID: got %d, want %d", decoded.SourceID, original.SourceID)
	}
	if decoded.TargetID != original.TargetID {
		t.Errorf("TargetID: got %d, want %d", decoded.TargetID, original.TargetID)
	}
	if decoded.Type != original.Type {
		t.Errorf("Type: got %d, want %d", decoded.Type, original.Type)
	}
	if decoded.Weight != original.Weight {
		t.Errorf("Weight: got %f, want %f", decoded.Weight, original.Weight)
	}
	if decoded.SessionID != original.SessionID {
		t.Errorf("SessionID: got %d, want %d", decoded.SessionID, original.SessionID)
	}
	if decoded.AgentID != original.AgentID {
		t.Errorf("AgentID: got %d, want %d", decoded.AgentID, original.AgentID)
	}
	if decoded.CreatedAt != original.CreatedAt {
		t.Errorf("CreatedAt: got %d, want %d", decoded.CreatedAt, original.CreatedAt)
	}
	if decoded.UpdatedAt != original.UpdatedAt {
		t.Errorf("UpdatedAt: got %d, want %d", decoded.UpdatedAt, original.UpdatedAt)
	}
}

func TestEdgeUnmarshalTruncated(t *testing.T) {
	edge := &Edge{}
	err := edge.UnmarshalBinary(make([]byte, 10))
	if err == nil {
		t.Error("Expected error for truncated data")
	}
}

func TestEdgeShardStoreWriteRead(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write edge
	edge := makeTestEdge(100, 200, 1)
	if err := store.Write(edge); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read by compound key
	read, err := store.GetEdge(100, 200, 1)
	if err != nil {
		t.Fatalf("GetEdge failed: %v", err)
	}

	if read.SourceID != edge.SourceID {
		t.Errorf("SourceID mismatch: got %d, want %d", read.SourceID, edge.SourceID)
	}
	if read.TargetID != edge.TargetID {
		t.Errorf("TargetID mismatch: got %d, want %d", read.TargetID, edge.TargetID)
	}
}

func TestEdgeShardStoreGetNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	_, err := store.GetEdge(999, 888, 1)
	if err != ErrEdgeNotFound {
		t.Errorf("Expected ErrEdgeNotFound, got %v", err)
	}
}

func TestEdgeShardStoreGetOutgoing(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write multiple edges from same source
	store.Write(makeTestEdge(100, 200, 1))
	store.Write(makeTestEdge(100, 201, 1))
	store.Write(makeTestEdge(100, 202, 2))
	store.Write(makeTestEdge(999, 100, 1)) // Different source

	// Get outgoing from node 100
	edges, err := store.GetOutgoing(100)
	if err != nil {
		t.Fatalf("GetOutgoing failed: %v", err)
	}

	if len(edges) != 3 {
		t.Errorf("GetOutgoing count = %d, want 3", len(edges))
	}

	// Verify all have source 100
	for _, e := range edges {
		if e.SourceID != 100 {
			t.Errorf("Edge has wrong source: %d", e.SourceID)
		}
	}
}

func TestEdgeShardStoreGetIncoming(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write multiple edges to same target
	store.Write(makeTestEdge(100, 500, 1))
	store.Write(makeTestEdge(101, 500, 1))
	store.Write(makeTestEdge(102, 500, 2))
	store.Write(makeTestEdge(103, 999, 1)) // Different target

	// Get incoming to node 500
	edges, err := store.GetIncoming(500)
	if err != nil {
		t.Fatalf("GetIncoming failed: %v", err)
	}

	if len(edges) != 3 {
		t.Errorf("GetIncoming count = %d, want 3", len(edges))
	}

	// Verify all have target 500
	for _, e := range edges {
		if e.TargetID != 500 {
			t.Errorf("Edge has wrong target: %d", e.TargetID)
		}
	}
}

func TestEdgeShardStoreUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write initial edge
	edge := makeTestEdge(100, 200, 1)
	edge.Weight = 0.5
	if err := store.Write(edge); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Update edge (same compound key)
	updatedEdge := makeTestEdge(100, 200, 1)
	updatedEdge.Weight = 0.9
	updatedEdge.UpdatedAt = uint64(time.Now().UnixNano())
	if err := store.Write(updatedEdge); err != nil {
		t.Fatalf("Write update failed: %v", err)
	}

	// Should still be one edge
	if store.Count() != 1 {
		t.Errorf("Count = %d, want 1", store.Count())
	}

	// Read and verify updated value
	read, err := store.GetEdge(100, 200, 1)
	if err != nil {
		t.Fatalf("GetEdge failed: %v", err)
	}
	if read.Weight != 0.9 {
		t.Errorf("Weight = %f, want 0.9", read.Weight)
	}
}

func TestEdgeShardStoreMultipleShards(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.shardSize = 10 // Small shard size for testing
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write edges across multiple shards
	// Shard 0: source 0-9, Shard 1: source 10-19, etc.
	store.Write(makeTestEdge(5, 100, 1))   // Shard 0
	store.Write(makeTestEdge(15, 100, 1))  // Shard 1
	store.Write(makeTestEdge(25, 100, 1))  // Shard 2
	store.Write(makeTestEdge(15, 101, 2))  // Shard 1
	store.Write(makeTestEdge(5, 200, 1))   // Shard 0

	// Get outgoing from node 5 (shard 0)
	edges5, _ := store.GetOutgoing(5)
	if len(edges5) != 2 {
		t.Errorf("GetOutgoing(5) count = %d, want 2", len(edges5))
	}

	// Get outgoing from node 15 (shard 1)
	edges15, _ := store.GetOutgoing(15)
	if len(edges15) != 2 {
		t.Errorf("GetOutgoing(15) count = %d, want 2", len(edges15))
	}

	// Get incoming to node 100 (edges from multiple shards)
	incoming, _ := store.GetIncoming(100)
	if len(incoming) != 3 {
		t.Errorf("GetIncoming(100) count = %d, want 3", len(incoming))
	}
}

func TestEdgeShardStorePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create store and write edges
	store1 := NewEdgeShardStoreFromSylkDir(sd)
	if err := store1.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	store1.Write(makeTestEdge(100, 200, 1))
	store1.Write(makeTestEdge(100, 201, 2))
	store1.Write(makeTestEdge(300, 400, 1))

	// Save and close
	if err := store1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open new store and load
	store2 := NewEdgeShardStoreFromSylkDir(sd)
	if err := store2.Init(); err != nil {
		t.Fatalf("Store2 init failed: %v", err)
	}

	// Verify edges are readable
	edges, err := store2.GetOutgoing(100)
	if err != nil {
		t.Fatalf("GetOutgoing after reload failed: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("GetOutgoing(100) after reload = %d, want 2", len(edges))
	}

	// Verify specific edge
	edge, err := store2.GetEdge(300, 400, 1)
	if err != nil {
		t.Fatalf("GetEdge after reload failed: %v", err)
	}
	if edge.SourceID != 300 || edge.TargetID != 400 {
		t.Errorf("Edge mismatch after reload")
	}
}

func TestEdgeShardStoreCount(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	if store.Count() != 0 {
		t.Errorf("Initial count = %d, want 0", store.Count())
	}

	store.Write(makeTestEdge(100, 200, 1))
	store.Write(makeTestEdge(100, 201, 1))
	store.Write(makeTestEdge(100, 202, 1))

	if store.Count() != 3 {
		t.Errorf("Count after writes = %d, want 3", store.Count())
	}

	// Duplicate key should not increase count
	store.Write(makeTestEdge(100, 200, 1))
	if store.Count() != 3 {
		t.Errorf("Count after duplicate = %d, want 3", store.Count())
	}
}

func TestEdgeShardStoreStats(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.shardSize = 100
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Create edges across 2 shards
	store.Write(makeTestEdge(50, 200, 1))   // Shard 0
	store.Write(makeTestEdge(50, 201, 1))   // Shard 0
	store.Write(makeTestEdge(150, 200, 1))  // Shard 1
	store.Write(makeTestEdge(150, 300, 1))  // Shard 1

	stats := store.Stats()
	if stats.TotalEdges != 4 {
		t.Errorf("TotalEdges = %d, want 4", stats.TotalEdges)
	}
	if stats.ShardCount != 2 {
		t.Errorf("ShardCount = %d, want 2", stats.ShardCount)
	}
	if stats.OutgoingNodes != 2 {
		t.Errorf("OutgoingNodes = %d, want 2", stats.OutgoingNodes)
	}
}

func TestEdgeKey(t *testing.T) {
	edge := makeTestEdge(100, 200, 5)
	key := edge.EdgeKey()

	if key.SourceID != 100 {
		t.Errorf("EdgeKey.SourceID = %d, want 100", key.SourceID)
	}
	if key.TargetID != 200 {
		t.Errorf("EdgeKey.TargetID = %d, want 200", key.TargetID)
	}
	if key.Type != 5 {
		t.Errorf("EdgeKey.Type = %d, want 5", key.Type)
	}
}
