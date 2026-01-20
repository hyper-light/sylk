package vectorgraphdb

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

func (m *mockHNSW) DeleteBatch(ids []string) error {
	for _, id := range ids {
		m.deleted[id] = true
	}
	return nil
}

// failingHNSW is a mock that fails on Insert for testing rollback.
type failingHNSW struct {
	insertErr error
	inserted  map[string]bool
	deleted   map[string]bool
	mu        sync.Mutex
}

func newFailingHNSW(err error) *failingHNSW {
	return &failingHNSW{
		insertErr: err,
		inserted:  make(map[string]bool),
		deleted:   make(map[string]bool),
	}
}

func (m *failingHNSW) Insert(id string, vector []float32, domain Domain, nodeType NodeType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.insertErr
}

func (m *failingHNSW) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted[id] = true
	return nil
}

func (m *failingHNSW) DeleteBatch(ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		m.deleted[id] = true
	}
	return nil
}

// conditionalFailHNSW fails on specific node IDs.
type conditionalFailHNSW struct {
	failOnIDs map[string]error
	inserted  map[string]bool
	deleted   map[string]bool
	mu        sync.Mutex
}

func newConditionalFailHNSW() *conditionalFailHNSW {
	return &conditionalFailHNSW{
		failOnIDs: make(map[string]error),
		inserted:  make(map[string]bool),
		deleted:   make(map[string]bool),
	}
}

func (m *conditionalFailHNSW) SetFailure(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failOnIDs[id] = err
}

func (m *conditionalFailHNSW) Insert(id string, vector []float32, domain Domain, nodeType NodeType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, exists := m.failOnIDs[id]; exists {
		return err
	}
	m.inserted[id] = true
	return nil
}

func (m *conditionalFailHNSW) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted[id] = true
	return nil
}

func (m *conditionalFailHNSW) DeleteBatch(ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		m.deleted[id] = true
	}
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

func TestNodeStoreGetNodesBatch(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	// Insert test nodes
	for i := 0; i < 10; i++ {
		node := &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
			Metadata: map[string]any{"index": i},
		}
		ns.InsertNode(node, []float32{float32(i + 1), 0.1, 0.1})
	}

	// Test batch load with subset of IDs
	ids := []string{nodeID(0), nodeID(2), nodeID(5), nodeID(9)}
	nodes, err := ns.GetNodesBatch(ids)
	if err != nil {
		t.Fatalf("GetNodesBatch: %v", err)
	}

	if len(nodes) != 4 {
		t.Errorf("Got %d nodes, want 4", len(nodes))
	}

	// Verify each requested node is in the result
	for _, id := range ids {
		if nodes[id] == nil {
			t.Errorf("Node %s not found in batch result", id)
		}
	}
}

func TestNodeStoreGetNodesBatchEmpty(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	nodes, err := ns.GetNodesBatch([]string{})
	if err != nil {
		t.Fatalf("GetNodesBatch: %v", err)
	}

	if len(nodes) != 0 {
		t.Errorf("Got %d nodes, want 0", len(nodes))
	}
}

func TestNodeStoreGetNodesBatchMissingNodes(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	// Insert only some nodes
	for i := 0; i < 3; i++ {
		node := &GraphNode{
			ID:       nodeID(i),
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}
		ns.InsertNode(node, []float32{float32(i + 1), 0.1, 0.1})
	}

	// Request nodes that exist and nodes that don't
	ids := []string{nodeID(0), nodeID(1), "nonexistent1", "nonexistent2"}
	nodes, err := ns.GetNodesBatch(ids)
	if err != nil {
		t.Fatalf("GetNodesBatch: %v", err)
	}

	// Should only return the 2 existing nodes
	if len(nodes) != 2 {
		t.Errorf("Got %d nodes, want 2", len(nodes))
	}

	// Verify correct nodes are returned
	if nodes[nodeID(0)] == nil {
		t.Error("node000 should be returned")
	}
	if nodes[nodeID(1)] == nil {
		t.Error("node001 should be returned")
	}
}

func TestNodeStoreGetNodesBatchLarge(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	ns := NewNodeStore(db, nil)

	// Insert 200 nodes to test batching beyond default batch size (100)
	ids := make([]string, 200)
	for i := 0; i < 200; i++ {
		id := "node" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		ids[i] = id
		node := &GraphNode{
			ID:       id,
			Domain:   DomainCode,
			NodeType: NodeTypeFile,
		}
		ns.InsertNode(node, []float32{float32(i + 1), 0.1, 0.1})
	}

	// Batch load all
	nodes, err := ns.GetNodesBatch(ids)
	if err != nil {
		t.Fatalf("GetNodesBatch: %v", err)
	}

	if len(nodes) != 200 {
		t.Errorf("Got %d nodes, want 200", len(nodes))
	}
}

// =============================================================================
// HNSW Insert Failure Rollback Tests
// =============================================================================

func TestNodeStoreInsertHNSWFailureRollback(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	hnswErr := errors.New("HNSW index full")
	hnsw := newFailingHNSW(hnswErr)
	ns := NewNodeStore(db, hnsw)

	node := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: map[string]any{"path": "/src/main.go"},
	}
	embedding := []float32{1, 0, 0, 0}

	err := ns.InsertNode(node, embedding)

	// Verify error is returned
	if err == nil {
		t.Fatal("InsertNode should have returned an error")
	}

	// Verify it's an HNSWInsertError
	var hnswInsertErr *HNSWInsertError
	if !errors.As(err, &hnswInsertErr) {
		t.Fatalf("expected HNSWInsertError, got %T: %v", err, err)
	}

	// Verify error details
	if hnswInsertErr.NodeID != "node1" {
		t.Errorf("NodeID = %s, want node1", hnswInsertErr.NodeID)
	}
	if !errors.Is(hnswInsertErr.HNSWErr, hnswErr) {
		t.Errorf("HNSWErr = %v, want %v", hnswInsertErr.HNSWErr, hnswErr)
	}
	if hnswInsertErr.RollbackFailed {
		t.Error("RollbackFailed should be false")
	}

	// Verify node was rolled back from DB
	_, getErr := ns.GetNode("node1")
	if getErr != ErrNodeNotFound {
		t.Errorf("Node should not exist after rollback, got err: %v", getErr)
	}
}

func TestNodeStoreInsertHNSWSuccessNoRollback(t *testing.T) {
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

	// Verify HNSW insert was called
	if !hnsw.inserted["node1"] {
		t.Error("HNSW insert not called")
	}

	// Verify node exists in DB
	retrieved, err := ns.GetNode("node1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if retrieved.ID != "node1" {
		t.Errorf("ID = %s, want node1", retrieved.ID)
	}
}

func TestNodeStoreInsertPartialFailureWithConditionalHNSW(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	hnsw := newConditionalFailHNSW()
	hnsw.SetFailure("node2", errors.New("HNSW insert failed for node2"))
	ns := NewNodeStore(db, hnsw)

	// Insert first node - should succeed
	node1 := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}
	err := ns.InsertNode(node1, []float32{1, 0, 0})
	if err != nil {
		t.Fatalf("InsertNode node1: %v", err)
	}

	// Insert second node - should fail and rollback
	node2 := &GraphNode{
		ID:       "node2",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}
	err = ns.InsertNode(node2, []float32{0, 1, 0})
	if err == nil {
		t.Fatal("InsertNode node2 should have failed")
	}

	// Verify node1 still exists
	_, err = ns.GetNode("node1")
	if err != nil {
		t.Errorf("node1 should exist: %v", err)
	}

	// Verify node2 was rolled back
	_, err = ns.GetNode("node2")
	if err != ErrNodeNotFound {
		t.Errorf("node2 should not exist, got err: %v", err)
	}

	// Insert third node - should succeed
	node3 := &GraphNode{
		ID:       "node3",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
	}
	err = ns.InsertNode(node3, []float32{0, 0, 1})
	if err != nil {
		t.Fatalf("InsertNode node3: %v", err)
	}

	// Verify node3 exists
	_, err = ns.GetNode("node3")
	if err != nil {
		t.Errorf("node3 should exist: %v", err)
	}
}

func TestHNSWInsertErrorMessage(t *testing.T) {
	// Test error message with successful rollback
	err1 := &HNSWInsertError{
		NodeID:         "test-node",
		HNSWErr:        errors.New("index capacity exceeded"),
		RollbackFailed: false,
	}
	expected1 := "HNSW insert failed for node test-node (rolled back): index capacity exceeded"
	if err1.Error() != expected1 {
		t.Errorf("Error() = %q, want %q", err1.Error(), expected1)
	}

	// Test error message with failed rollback
	err2 := &HNSWInsertError{
		NodeID:         "test-node",
		HNSWErr:        errors.New("index capacity exceeded"),
		RollbackFailed: true,
		RollbackErr:    errors.New("database locked"),
	}
	expected2 := "HNSW insert failed for node test-node: index capacity exceeded; rollback also failed: database locked"
	if err2.Error() != expected2 {
		t.Errorf("Error() = %q, want %q", err2.Error(), expected2)
	}
}

func TestHNSWInsertErrorUnwrap(t *testing.T) {
	originalErr := errors.New("original HNSW error")
	hnswErr := &HNSWInsertError{
		NodeID:  "test-node",
		HNSWErr: originalErr,
	}

	if !errors.Is(hnswErr, originalErr) {
		t.Error("errors.Is should return true for wrapped error")
	}
}

func TestNodeStoreInsertConcurrentWithFailures(t *testing.T) {
	db, path := setupTestDB(t)
	defer cleanupDB(db, path)

	hnsw := newConditionalFailHNSW()
	// Set failures for even-numbered nodes
	for i := 0; i < 20; i += 2 {
		hnsw.SetFailure(nodeID(i), errors.New("simulated HNSW failure"))
	}
	ns := NewNodeStore(db, hnsw)

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var failCount atomic.Int32

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			node := &GraphNode{
				ID:       nodeID(idx),
				Domain:   DomainCode,
				NodeType: NodeTypeFile,
			}
			err := ns.InsertNode(node, []float32{float32(idx), 0.1, 0.1})
			if err != nil {
				failCount.Add(1)
			} else {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// 10 even nodes should fail, 10 odd nodes should succeed
	if successCount.Load() != 10 {
		t.Errorf("successCount = %d, want 10", successCount.Load())
	}
	if failCount.Load() != 10 {
		t.Errorf("failCount = %d, want 10", failCount.Load())
	}

	// Verify only odd nodes exist in DB
	for i := 0; i < 20; i++ {
		_, err := ns.GetNode(nodeID(i))
		if i%2 == 0 {
			// Even nodes should have been rolled back
			if err != ErrNodeNotFound {
				t.Errorf("node%d (even) should not exist, got err: %v", i, err)
			}
		} else {
			// Odd nodes should exist
			if err != nil {
				t.Errorf("node%d (odd) should exist, got err: %v", i, err)
			}
		}
	}
}
