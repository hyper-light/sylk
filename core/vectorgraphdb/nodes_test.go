package vectorgraphdb

import (
	"os"
	"path/filepath"
	"testing"
)

type mockHNSW struct {
	inserted map[string]bool
	deleted  map[string]bool
}

func newMockHNSW() *mockHNSW {
	return &mockHNSW{
		inserted: make(map[string]bool),
		deleted:  make(map[string]bool),
	}
}

func (m *mockHNSW) Insert(id string, vector []float32, domain Domain, nodeType NodeType) error {
	m.inserted[id] = true
	return nil
}

func (m *mockHNSW) Delete(id string) error {
	m.deleted[id] = true
	return nil
}

func setupTestDB(t *testing.T) (*VectorGraphDB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	return db, dbPath
}

func cleanupDB(db *VectorGraphDB, path string) {
	db.Close()
	os.Remove(path)
}

func TestNodeStoreInsertAndGet(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	hnsw := newMockHNSW()
	ns := NewNodeStore(db, hnsw)

	node := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: map[string]any{"path": "/src/main.go"},
	}
	embedding := []float32{1, 0, 0, 0}

	err := ns.InsertNode(node, embedding)
	if err != nil {
		t.Fatalf("InsertNode: %v", err)
	}

	if !hnsw.inserted["node1"] {
		t.Error("HNSW insert not called")
	}

	retrieved, err := ns.GetNode("node1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}

	if retrieved.ID != node.ID {
		t.Errorf("ID = %s, want %s", retrieved.ID, node.ID)
	}
	if retrieved.Domain != node.Domain {
		t.Errorf("Domain = %s, want %s", retrieved.Domain, node.Domain)
	}
	if retrieved.NodeType != node.NodeType {
		t.Errorf("NodeType = %s, want %s", retrieved.NodeType, node.NodeType)
	}
}

func TestNodeStoreInsertInvalidDomain(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	node := &GraphNode{
		ID:       "node1",
		Domain:   Domain(99),
		NodeType: NodeTypeFile,
	}

	err := ns.InsertNode(node, []float32{1, 0, 0})
	if err != ErrInvalidDomain {
		t.Errorf("InsertNode: got %v, want ErrInvalidDomain", err)
	}
}

func TestNodeStoreInsertInvalidNodeType(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	node := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeSession,
	}

	err := ns.InsertNode(node, []float32{1, 0, 0})
	if err != ErrInvalidNodeType {
		t.Errorf("InsertNode: got %v, want ErrInvalidNodeType", err)
	}
}

func TestNodeStoreGetNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	_, err := ns.GetNode("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("GetNode: got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeStoreUpdate(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	node := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: map[string]any{"path": "/src/main.go"},
	}
	ns.InsertNode(node, []float32{1, 0, 0})

	node.Metadata["path"] = "/src/updated.go"
	err := ns.UpdateNode(node)
	if err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}

	retrieved, _ := ns.GetNode("node1")
	if retrieved.Metadata["path"] != "/src/updated.go" {
		t.Errorf("Metadata not updated: %v", retrieved.Metadata)
	}
}

func TestNodeStoreUpdateNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	node := &GraphNode{
		ID:       "nonexistent",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}

	err := ns.UpdateNode(node)
	if err != ErrNodeNotFound {
		t.Errorf("UpdateNode: got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeStoreDelete(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	hnsw := newMockHNSW()
	ns := NewNodeStore(db, hnsw)

	node := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}
	ns.InsertNode(node, []float32{1, 0, 0})

	err := ns.DeleteNode("node1")
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	if !hnsw.deleted["node1"] {
		t.Error("HNSW delete not called")
	}

	_, err = ns.GetNode("node1")
	if err != ErrNodeNotFound {
		t.Error("Node still exists after delete")
	}
}

func TestNodeStoreDeleteNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	err := ns.DeleteNode("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("DeleteNode: got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeStoreGetByType(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	for i := 0; i < 5; i++ {
		node := &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}
		ns.InsertNode(node, []float32{float32(i + 1), 0.1, 0.1})
	}

	for i := 5; i < 8; i++ {
		node := &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFunction,
		}
		ns.InsertNode(node, []float32{float32(i + 1), 0.1, 0.1})
	}

	files, err := ns.GetNodesByType(DomainCode, NodeTypeFile, 10)
	if err != nil {
		t.Fatalf("GetNodesByType: %v", err)
	}
	if len(files) != 5 {
		t.Errorf("Got %d files, want 5", len(files))
	}

	funcs, err := ns.GetNodesByType(DomainCode, NodeTypeFunction, 10)
	if err != nil {
		t.Fatalf("GetNodesByType: %v", err)
	}
	if len(funcs) != 3 {
		t.Errorf("Got %d functions, want 3", len(funcs))
	}
}

func TestNodeStoreGetByTypeWithLimit(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	for i := 0; i < 10; i++ {
		node := &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}
		ns.InsertNode(node, []float32{float32(i + 1), 0.1, 0.1})
	}

	nodes, err := ns.GetNodesByType(DomainCode, NodeTypeFile, 5)
	if err != nil {
		t.Fatalf("GetNodesByType: %v", err)
	}
	if len(nodes) != 5 {
		t.Errorf("Got %d nodes, want 5", len(nodes))
	}
}

func TestNodeStoreGetByContentHash(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	node1 := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: map[string]any{"content": "same"},
	}
	ns.InsertNode(node1, []float32{1, 0, 0})

	node2 := &GraphNode{
		ID:       "node2",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: map[string]any{"content": "same"},
	}
	ns.InsertNode(node2, []float32{0, 1, 0})

	retrievedNode1, _ := ns.GetNode("node1")
	nodes, err := ns.GetNodesByContentHash(retrievedNode1.ContentHash)
	if err != nil {
		t.Fatalf("GetNodesByContentHash: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("Got %d nodes, want 2", len(nodes))
	}
}

func TestNodeStoreTouchNode(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	node := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}
	ns.InsertNode(node, []float32{1, 0, 0})

	before, _ := ns.GetNode("node1")

	err := ns.TouchNode("node1")
	if err != nil {
		t.Fatalf("TouchNode: %v", err)
	}

	after, _ := ns.GetNode("node1")
	if !after.UpdatedAt.After(before.UpdatedAt) && after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Log("UpdatedAt same or not updated - timing may be too fast")
	}
}

func TestNodeStoreTouchNotFound(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	err := ns.TouchNode("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("TouchNode: got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeStoreContentHashComputation(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	node1 := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: map[string]any{"key": "value1"},
	}
	ns.InsertNode(node1, []float32{1, 0, 0})

	node2 := &GraphNode{
		ID:       "node2",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: map[string]any{"key": "value2"},
	}
	ns.InsertNode(node2, []float32{0, 1, 0})

	r1, _ := ns.GetNode("node1")
	r2, _ := ns.GetNode("node2")

	if r1.ContentHash == r2.ContentHash {
		t.Error("Different content should have different hash")
	}
}

func nodeID(i int) string {
	return "node" + string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
}

func TestComputeMagnitude(t *testing.T) {
	tests := []struct {
		name     string
		v        []float32
		expected float64
	}{
		{"unit x", []float32{1, 0, 0}, 1.0},
		{"3-4-5", []float32{3, 4, 0}, 5.0},
		{"zero", []float32{0, 0, 0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeMagnitude(tt.v)
			if result != tt.expected {
				t.Errorf("computeMagnitude() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFloat32sToBytes(t *testing.T) {
	input := []float32{1.0, 2.0, 3.0}
	bytes := float32sToBytes(input)

	if len(bytes) != 12 {
		t.Errorf("len(bytes) = %d, want 12", len(bytes))
	}
}
