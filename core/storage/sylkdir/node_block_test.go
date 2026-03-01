package sylkdir

import (
	"testing"
	"time"
)

func makeTestNode(id uint32) *Node {
	return &Node{
		ID:           id,
		CanonicalKey: "repo:path/to/file.go:FunctionName:func",
		Domain:       1, // DomainCode
		NodeType:     2, // NodeTypeFunction
		CreatedBy:    1,
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    1,
		DocRef:       0,
		SectionStart: 10,
		SectionEnd:   25,
		Name:         "FunctionName",
		Path:         "path/to/file.go",
		Package:      "mypackage",
		Signature:    "func FunctionName(ctx context.Context) error",
		SupersededBy: 0,
		Supersedes:   0,
	}
}

func TestNodeMarshalUnmarshal(t *testing.T) {
	original := makeTestNode(1)

	// Marshal
	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	// Unmarshal
	decoded := &Node{}
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	// Verify
	if decoded.ID != original.ID {
		t.Errorf("ID: got %d, want %d", decoded.ID, original.ID)
	}
	if decoded.CanonicalKey != original.CanonicalKey {
		t.Errorf("CanonicalKey: got %s, want %s", decoded.CanonicalKey, original.CanonicalKey)
	}
	if decoded.Domain != original.Domain {
		t.Errorf("Domain: got %d, want %d", decoded.Domain, original.Domain)
	}
	if decoded.NodeType != original.NodeType {
		t.Errorf("NodeType: got %d, want %d", decoded.NodeType, original.NodeType)
	}
	if decoded.Name != original.Name {
		t.Errorf("Name: got %s, want %s", decoded.Name, original.Name)
	}
	if decoded.Path != original.Path {
		t.Errorf("Path: got %s, want %s", decoded.Path, original.Path)
	}
	if decoded.Package != original.Package {
		t.Errorf("Package: got %s, want %s", decoded.Package, original.Package)
	}
	if decoded.Signature != original.Signature {
		t.Errorf("Signature: got %s, want %s", decoded.Signature, original.Signature)
	}
	if decoded.CreatedAt != original.CreatedAt {
		t.Errorf("CreatedAt: got %d, want %d", decoded.CreatedAt, original.CreatedAt)
	}
	if decoded.SessionID != original.SessionID {
		t.Errorf("SessionID: got %d, want %d", decoded.SessionID, original.SessionID)
	}
	if decoded.CreatedBy != original.CreatedBy {
		t.Errorf("CreatedBy: got %d, want %d", decoded.CreatedBy, original.CreatedBy)
	}
	if decoded.DocRef != original.DocRef {
		t.Errorf("DocRef: got %d, want %d", decoded.DocRef, original.DocRef)
	}
	if decoded.SectionStart != original.SectionStart {
		t.Errorf("SectionStart: got %d, want %d", decoded.SectionStart, original.SectionStart)
	}
	if decoded.SectionEnd != original.SectionEnd {
		t.Errorf("SectionEnd: got %d, want %d", decoded.SectionEnd, original.SectionEnd)
	}
	if decoded.SupersededBy != original.SupersededBy {
		t.Errorf("SupersededBy: got %d, want %d", decoded.SupersededBy, original.SupersededBy)
	}
	if decoded.Supersedes != original.Supersedes {
		t.Errorf("Supersedes: got %d, want %d", decoded.Supersedes, original.Supersedes)
	}
}

func TestNodeDocRefRoundTrip(t *testing.T) {
	node := makeTestNode(42)
	node.DocRef = 12345

	data, err := node.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	decoded := &Node{}
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}

	if decoded.DocRef != 12345 {
		t.Errorf("DocRef: got %d, want 12345", decoded.DocRef)
	}
	if decoded.CreatedBy != node.CreatedBy {
		t.Errorf("CreatedBy: got %d, want %d", decoded.CreatedBy, node.CreatedBy)
	}
}

func TestNodeMarshalEmptyStrings(t *testing.T) {
	original := &Node{
		ID:           1,
		CanonicalKey: "",
		Domain:       1,
		NodeType:     2,
		Name:         "",
		Path:         "",
		Package:      "",
		Signature:    "",
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    1,
		CreatedBy:    1,
	}

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	decoded := &Node{}
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	if decoded.CanonicalKey != "" {
		t.Errorf("CanonicalKey should be empty")
	}
	if decoded.Name != "" {
		t.Errorf("Name should be empty")
	}
}

func TestNodeUnmarshalTruncated(t *testing.T) {
	node := &Node{}

	// Too short for header
	err := node.UnmarshalBinary(make([]byte, 10))
	if err != ErrInvalidNodeData {
		t.Errorf("Expected ErrInvalidNodeData for truncated header, got %v", err)
	}

	// Valid header but truncated strings
	data := make([]byte, NodeHeaderSize+2)
	data[NodeHeaderSize] = 0xFF // Invalid length prefix
	data[NodeHeaderSize+1] = 0xFF
	err = node.UnmarshalBinary(data)
	if err != ErrInvalidNodeData {
		t.Errorf("Expected ErrInvalidNodeData for truncated strings, got %v", err)
	}
}

func TestNodeBlockStoreWriteRead(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewNodeBlockStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write a node
	node := makeTestNode(1)
	if err := store.Write(node); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read it back
	read, err := store.Read(1)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if read.ID != node.ID {
		t.Errorf("ID mismatch: got %d, want %d", read.ID, node.ID)
	}
	if read.CanonicalKey != node.CanonicalKey {
		t.Errorf("CanonicalKey mismatch: got %s, want %s", read.CanonicalKey, node.CanonicalKey)
	}
}

func TestNodeBlockStoreReadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewNodeBlockStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	_, err := store.Read(999)
	if err != ErrNodeNotFound {
		t.Errorf("Expected ErrNodeNotFound, got %v", err)
	}
}

func TestNodeBlockStoreMultipleBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.blockSize = 10 // Small block size for testing
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write nodes across multiple blocks
	for i := uint32(0); i < 25; i++ {
		node := makeTestNode(i)
		if err := store.Write(node); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}

	// Verify all nodes are readable
	for i := uint32(0); i < 25; i++ {
		read, err := store.Read(i)
		if err != nil {
			t.Errorf("Read %d failed: %v", i, err)
			continue
		}
		if read.ID != i {
			t.Errorf("Node %d has wrong ID: %d", i, read.ID)
		}
	}

	// Should have 3 blocks (0-9, 10-19, 20-24)
	if store.Count() != 25 {
		t.Errorf("Count = %d, want 25", store.Count())
	}
}

func TestNodeBlockStoreReadBatch(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.blockSize = 10
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	// Write 20 nodes
	for i := uint32(0); i < 20; i++ {
		if err := store.Write(makeTestNode(i)); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}

	// Read batch including some that don't exist
	ids := []uint32{5, 15, 999, 10, 0}
	results, err := store.ReadBatch(ids)
	if err != nil {
		t.Fatalf("ReadBatch failed: %v", err)
	}

	if len(results) != len(ids) {
		t.Fatalf("Results length = %d, want %d", len(results), len(ids))
	}

	// Check expected results
	if results[0] == nil || results[0].ID != 5 {
		t.Errorf("results[0] should be node 5")
	}
	if results[1] == nil || results[1].ID != 15 {
		t.Errorf("results[1] should be node 15")
	}
	if results[2] != nil {
		t.Errorf("results[2] should be nil (node 999 doesn't exist)")
	}
	if results[3] == nil || results[3].ID != 10 {
		t.Errorf("results[3] should be node 10")
	}
	if results[4] == nil || results[4].ID != 0 {
		t.Errorf("results[4] should be node 0")
	}
}

func TestNodeBlockStorePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create store and write nodes
	store1 := NewNodeBlockStoreFromSylkDir(sd)
	if err := store1.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	for i := uint32(0); i < 10; i++ {
		if err := store1.Write(makeTestNode(i)); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}

	// Save index and close
	if err := store1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open new store and load index
	store2 := NewNodeBlockStoreFromSylkDir(sd)
	if err := store2.Init(); err != nil {
		t.Fatalf("Store2 init failed: %v", err)
	}

	// Verify all nodes are readable
	for i := uint32(0); i < 10; i++ {
		read, err := store2.Read(i)
		if err != nil {
			t.Errorf("Read %d after reload failed: %v", i, err)
			continue
		}
		if read.ID != i {
			t.Errorf("Node %d has wrong ID after reload: %d", i, read.ID)
		}
	}
}

func TestNodeBlockStoreExists(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewNodeBlockStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	if store.Exists(1) {
		t.Error("Node 1 should not exist before write")
	}

	store.Write(makeTestNode(1))

	if !store.Exists(1) {
		t.Error("Node 1 should exist after write")
	}
	if store.Exists(2) {
		t.Error("Node 2 should not exist")
	}
}

func TestNodeBlockStoreCount(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	store := NewNodeBlockStoreFromSylkDir(sd)
	if err := store.Init(); err != nil {
		t.Fatalf("Store init failed: %v", err)
	}

	if store.Count() != 0 {
		t.Errorf("Initial count = %d, want 0", store.Count())
	}

	for i := uint32(0); i < 5; i++ {
		store.Write(makeTestNode(i))
	}

	if store.Count() != 5 {
		t.Errorf("Count after writes = %d, want 5", store.Count())
	}
}

func TestNodeLargeStrings(t *testing.T) {
	// Create node with large strings
	largeString := make([]byte, 10000)
	for i := range largeString {
		largeString[i] = byte('a' + (i % 26))
	}

	original := &Node{
		ID:           1,
		CanonicalKey: string(largeString),
		Domain:       1,
		NodeType:     2,
		Name:         string(largeString),
		Path:         string(largeString[:1000]),
		Package:      "pkg",
		Signature:    string(largeString[:5000]),
		CreatedAt:    uint64(time.Now().UnixNano()),
		SessionID:    1,
		CreatedBy:    1,
	}

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	decoded := &Node{}
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	if decoded.CanonicalKey != original.CanonicalKey {
		t.Errorf("Large CanonicalKey mismatch")
	}
	if decoded.Name != original.Name {
		t.Errorf("Large Name mismatch")
	}
}

func TestNodeSectionZeroMeansWholeDoc(t *testing.T) {
	node := makeTestNode(99)
	node.SectionStart = 0
	node.SectionEnd = 0

	data, err := node.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	decoded := &Node{}
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}

	if decoded.SectionStart != 0 {
		t.Errorf("SectionStart: got %d, want 0", decoded.SectionStart)
	}
	if decoded.SectionEnd != 0 {
		t.Errorf("SectionEnd: got %d, want 0", decoded.SectionEnd)
	}
	if decoded.SupersededBy != node.SupersededBy {
		t.Errorf("SupersededBy corrupted: got %d, want %d", decoded.SupersededBy, node.SupersededBy)
	}
}
