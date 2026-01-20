package vectorgraphdb

import (
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/migrations"
)

// mockSnapshot implements the Snapshot interface for testing.
type mockSnapshot struct {
	id      uint64
	results []SnapshotSearchResult
}

func (m *mockSnapshot) Search(query []float32, k int, filter *SnapshotSearchFilter) []SnapshotSearchResult {
	return m.results
}

func (m *mockSnapshot) ID() uint64 {
	return m.id
}

// mockSnapshotManager implements the SnapshotManager interface for testing.
type mockSnapshotManager struct {
	snapshots     map[uint64]*mockSnapshot
	nextID        uint64
	snapshotCount int
	onCreateFunc  func() Snapshot
}

func newMockSnapshotManager() *mockSnapshotManager {
	return &mockSnapshotManager{
		snapshots: make(map[uint64]*mockSnapshot),
		nextID:    1,
	}
}

func (m *mockSnapshotManager) CreateSnapshot() Snapshot {
	if m.onCreateFunc != nil {
		return m.onCreateFunc()
	}
	snap := &mockSnapshot{id: m.nextID}
	m.snapshots[m.nextID] = snap
	m.nextID++
	m.snapshotCount++
	return snap
}

func (m *mockSnapshotManager) ReleaseSnapshot(id uint64) {
	if _, exists := m.snapshots[id]; exists {
		delete(m.snapshots, id)
		m.snapshotCount--
	}
}

func (m *mockSnapshotManager) GCLoop(ctx interface{}) {
	// no-op for testing
}

func (m *mockSnapshotManager) SnapshotCount() int {
	return m.snapshotCount
}

// mockVectorIndex implements the VectorIndex interface for testing.
type mockVectorIndex struct {
	vectors   map[string][]float32
	domains   map[string]Domain
	nodeTypes map[string]NodeType
	inserted  map[string]bool
	deleted   map[string]bool
}

func newMockVectorIndex() *mockVectorIndex {
	return &mockVectorIndex{
		vectors:   make(map[string][]float32),
		domains:   make(map[string]Domain),
		nodeTypes: make(map[string]NodeType),
		inserted:  make(map[string]bool),
		deleted:   make(map[string]bool),
	}
}

func (m *mockVectorIndex) Search(query []float32, k int, filter *SnapshotSearchFilter) []SnapshotSearchResult {
	var results []SnapshotSearchResult
	for id := range m.vectors {
		results = append(results, SnapshotSearchResult{
			ID:         id,
			Similarity: 0.9,
			Domain:     m.domains[id],
			NodeType:   m.nodeTypes[id],
		})
		if len(results) >= k {
			break
		}
	}
	return results
}

func (m *mockVectorIndex) Insert(id string, vector []float32, domain Domain, nodeType NodeType) error {
	m.vectors[id] = vector
	m.domains[id] = domain
	m.nodeTypes[id] = nodeType
	m.inserted[id] = true
	return nil
}

func (m *mockVectorIndex) Delete(id string) error {
	delete(m.vectors, id)
	delete(m.domains, id)
	delete(m.nodeTypes, id)
	m.deleted[id] = true
	return nil
}

func (m *mockVectorIndex) DeleteBatch(ids []string) error {
	for _, id := range ids {
		delete(m.vectors, id)
		delete(m.domains, id)
		delete(m.nodeTypes, id)
		m.deleted[id] = true
	}
	return nil
}

func (m *mockVectorIndex) Size() int {
	return len(m.vectors)
}

// setupSessionViewDB sets up a test database for session view tests.
func setupSessionViewDB(t *testing.T) (*VectorGraphDB, *VersionedNodeStore, *mockVectorIndex, *mockSnapshotManager) {
	t.Helper()
	db, _ := setupTestDB(t)

	// Apply the version column migration
	migration := migrations.NewAddVersionColumnMigration(db.DB())
	if err := migration.Apply(); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	idx := newMockVectorIndex()
	snapshotMgr := newMockSnapshotManager()
	ns := NewNodeStore(db, idx)
	vns := NewVersionedNodeStore(ns)

	return db, vns, idx, snapshotMgr
}

// insertSessionTestNode inserts a test node with optional session ID.
func insertSessionTestNode(t *testing.T, vns *VersionedNodeStore, id, sessionID string) *GraphNode {
	t.Helper()
	node := &GraphNode{
		ID:        id,
		Domain:    DomainCode,
		NodeType:  NodeTypeFile,
		Name:      id,
		Content:   "test content for " + id,
		SessionID: sessionID,
		Metadata:  map[string]any{"test": "data"},
	}
	embedding := []float32{1, 0, 0, 0}

	if err := vns.InsertNode(node, embedding); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	return node
}

func TestSessionScopedView_IsolationLevelString(t *testing.T) {
	tests := []struct {
		level IsolationLevel
		want  string
	}{
		{IsolationReadCommitted, "read_committed"},
		{IsolationRepeatableRead, "repeatable_read"},
		{IsolationSessionLocal, "session_local"},
		{IsolationLevel(99), "unknown"},
	}

	for _, tc := range tests {
		got := tc.level.String()
		if got != tc.want {
			t.Errorf("IsolationLevel(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestSessionScopedView_NewSessionScopedView(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)

	if view.SessionID() != "session-1" {
		t.Errorf("SessionID() = %q, want %q", view.SessionID(), "session-1")
	}
	if view.Isolation() != IsolationReadCommitted {
		t.Errorf("Isolation() = %v, want %v", view.Isolation(), IsolationReadCommitted)
	}
	if view.IsClosed() {
		t.Error("expected view to not be closed")
	}
}

func TestSessionScopedView_ReadCommitted_SeesAllData(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	// Insert nodes from different sessions
	insertSessionTestNode(t, vns, "global-node", "")
	insertSessionTestNode(t, vns, "session1-node", "session-1")
	insertSessionTestNode(t, vns, "session2-node", "session-2")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	nodes, err := view.QueryNodes(NodeFilter{Limit: 100})
	if err != nil {
		t.Fatalf("QueryNodes: %v", err)
	}

	// ReadCommitted should see all committed data
	if len(nodes) < 3 {
		t.Errorf("expected at least 3 nodes, got %d", len(nodes))
	}

	// Verify we can see nodes from other sessions
	foundSession2 := false
	for _, n := range nodes {
		if n.ID == "session2-node" {
			foundSession2 = true
			break
		}
	}
	if !foundSession2 {
		t.Error("ReadCommitted should see nodes from other sessions")
	}
}

func TestSessionScopedView_RepeatableRead_UsesSnapshot(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	// Insert initial data
	insertSessionTestNode(t, vns, "initial-node", "")

	// Configure snapshot to return results
	snapshotMgr.onCreateFunc = func() Snapshot {
		snap := &mockSnapshot{
			id: 1,
			results: []SnapshotSearchResult{
				{ID: "initial-node", Similarity: 0.95},
			},
		}
		snapshotMgr.snapshots[1] = snap
		snapshotMgr.snapshotCount++
		return snap
	}

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationRepeatableRead,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	// Insert more data after view creation
	insertSessionTestNode(t, vns, "after-snapshot-node", "")

	// Search should use snapshot (not see new data in live index)
	results := view.Search([]float32{1, 0, 0, 0}, 10)

	// The initial node should be visible via snapshot
	foundInitial := false
	for _, r := range results {
		if r.Node.ID == "initial-node" {
			foundInitial = true
			break
		}
	}
	if !foundInitial {
		t.Error("RepeatableRead should see snapshot data")
	}
}

func TestSessionScopedView_SessionLocal_FiltersBySession(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	// Insert nodes with different session ownership
	insertSessionTestNode(t, vns, "global-node", "")
	insertSessionTestNode(t, vns, "my-node", "session-1")
	insertSessionTestNode(t, vns, "other-node", "session-2")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationSessionLocal,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	nodes, err := view.QueryNodes(NodeFilter{Limit: 100})
	if err != nil {
		t.Fatalf("QueryNodes: %v", err)
	}

	// SessionLocal should only see own session + global
	for _, n := range nodes {
		if n.SessionID != "" && n.SessionID != "session-1" {
			t.Errorf("SessionLocal should not see node from session %q", n.SessionID)
		}
	}

	// Should see global node
	foundGlobal := false
	foundMy := false
	foundOther := false
	for _, n := range nodes {
		switch n.ID {
		case "global-node":
			foundGlobal = true
		case "my-node":
			foundMy = true
		case "other-node":
			foundOther = true
		}
	}

	if !foundGlobal {
		t.Error("SessionLocal should see global nodes")
	}
	if !foundMy {
		t.Error("SessionLocal should see own session nodes")
	}
	if foundOther {
		t.Error("SessionLocal should not see other session nodes")
	}
}

func TestSessionScopedView_Close_ReleasesSnapshot(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	// Insert data to make snapshot non-empty
	idx.Insert("v1", []float32{1, 0, 0, 0}, DomainCode, NodeTypeFile)

	initialCount := snapshotMgr.SnapshotCount()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationRepeatableRead,
	}

	view := NewSessionScopedView(cfg)

	// Snapshot should be created
	if snapshotMgr.SnapshotCount() <= initialCount {
		t.Error("expected snapshot to be created for RepeatableRead")
	}

	// Close should release the snapshot
	if err := view.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !view.IsClosed() {
		t.Error("expected view to be closed")
	}
}

func TestSessionScopedView_DoubleClose_IsSafe(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationRepeatableRead,
	}

	view := NewSessionScopedView(cfg)

	// First close
	err1 := view.Close()
	if err1 != nil {
		t.Fatalf("first Close: %v", err1)
	}

	// Second close should be safe (idempotent)
	err2 := view.Close()
	if err2 != nil {
		t.Fatalf("second Close: %v", err2)
	}

	if !view.IsClosed() {
		t.Error("expected view to remain closed")
	}
}

func TestSessionScopedView_BeginWrite_ReturnsOptimisticTx(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	tx := view.BeginWrite(vns)
	if tx == nil {
		t.Fatal("BeginWrite returned nil")
	}

	if tx.SessionID() != "session-1" {
		t.Errorf("tx.SessionID() = %q, want %q", tx.SessionID(), "session-1")
	}

	// GetTransaction should return the same transaction
	gotTx := view.GetTransaction()
	if gotTx != tx {
		t.Error("GetTransaction should return the same transaction")
	}
}

func TestSessionScopedView_CommitWrite(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	insertSessionTestNode(t, vns, "node1", "session-1")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	tx := view.BeginWrite(vns)
	if tx == nil {
		t.Fatal("BeginWrite returned nil")
	}

	node, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("tx.Read: %v", err)
	}

	node.Content = "updated via view"
	if err := tx.Write(node); err != nil {
		t.Fatalf("tx.Write: %v", err)
	}

	if err := view.CommitWrite(); err != nil {
		t.Fatalf("CommitWrite: %v", err)
	}

	// Verify the change was committed
	updated, err := vns.GetNode("node1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if updated.Content != "updated via view" {
		t.Errorf("Content = %q, want %q", updated.Content, "updated via view")
	}

	// Transaction should be cleared
	if view.GetTransaction() != nil {
		t.Error("expected transaction to be cleared after commit")
	}
}

func TestSessionScopedView_RollbackWrite(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	insertSessionTestNode(t, vns, "node1", "session-1")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	tx := view.BeginWrite(vns)
	node, _ := tx.Read("node1")
	node.Content = "should not persist"
	tx.Write(node)

	view.RollbackWrite()

	// Verify the change was not committed
	unchanged, err := vns.GetNode("node1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if unchanged.Content == "should not persist" {
		t.Error("rollback should have discarded changes")
	}

	// Transaction should be cleared
	if view.GetTransaction() != nil {
		t.Error("expected transaction to be cleared after rollback")
	}
}

func TestSessionScopedView_QueryNodes_AfterClose(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	view.Close()

	_, err := view.QueryNodes(NodeFilter{Limit: 10})
	if err != ErrViewClosed {
		t.Errorf("QueryNodes after close: expected ErrViewClosed, got %v", err)
	}
}

func TestSessionScopedView_Search_AfterClose(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	view.Close()

	results := view.Search([]float32{1, 0, 0, 0}, 10)
	if results != nil {
		t.Error("Search after close should return nil")
	}
}

func TestSessionScopedView_BeginWrite_AfterClose(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	view.Close()

	tx := view.BeginWrite(vns)
	if tx != nil {
		t.Error("BeginWrite after close should return nil")
	}
}

func TestSessionScopedView_CommitWrite_AfterClose(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	view.Close()

	err := view.CommitWrite()
	if err != ErrViewClosed {
		t.Errorf("CommitWrite after close: expected ErrViewClosed, got %v", err)
	}
}

func TestSessionScopedView_BeginWrite_NilStore(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	tx := view.BeginWrite(nil)
	if tx != nil {
		t.Error("BeginWrite with nil store should return nil")
	}
}

func TestSessionScopedView_Search_ReadCommitted_UsesLiveIndex(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	// Insert initial data
	insertSessionTestNode(t, vns, "node1", "")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	// Insert more data after view creation
	insertSessionTestNode(t, vns, "node2", "")

	// ReadCommitted should see new data via live index
	results := view.Search([]float32{1, 0, 0, 0}, 10)

	foundNode2 := false
	for _, r := range results {
		if r.Node.ID == "node2" {
			foundNode2 = true
			break
		}
	}

	if !foundNode2 {
		t.Error("ReadCommitted should see newly inserted data via live index")
	}
}

func TestSessionScopedView_Search_SessionLocal_FiltersResults(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	// Insert nodes with different session ownership
	insertSessionTestNode(t, vns, "global-node", "")
	insertSessionTestNode(t, vns, "my-node", "session-1")
	insertSessionTestNode(t, vns, "other-node", "session-2")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationSessionLocal,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	results := view.Search([]float32{1, 0, 0, 0}, 10)

	// Should not see other session's nodes
	for _, r := range results {
		if r.Node.SessionID != "" && r.Node.SessionID != "session-1" {
			t.Errorf("SessionLocal search should not return nodes from session %q", r.Node.SessionID)
		}
	}
}

func TestSessionScopedView_QueryNodes_WithDomainFilter(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	// Insert a node
	insertSessionTestNode(t, vns, "code-node", "")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	nodes, err := view.QueryNodes(NodeFilter{
		Domains:   []Domain{DomainCode},
		NodeTypes: []NodeType{NodeTypeFile},
		Limit:     100,
	})
	if err != nil {
		t.Fatalf("QueryNodes: %v", err)
	}

	if len(nodes) == 0 {
		t.Error("expected to find nodes with domain filter")
	}

	for _, n := range nodes {
		if n.Domain != DomainCode {
			t.Errorf("expected Domain %v, got %v", DomainCode, n.Domain)
		}
	}
}

func TestSessionScopedView_Close_RollsBackPendingTransaction(t *testing.T) {
	db, vns, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	insertSessionTestNode(t, vns, "node1", "session-1")

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)

	// Start a transaction but don't commit
	tx := view.BeginWrite(vns)
	node, _ := tx.Read("node1")
	node.Content = "uncommitted change"
	tx.Write(node)

	// Close should rollback
	view.Close()

	// Verify the change was not committed
	unchanged, err := vns.GetNode("node1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if unchanged.Content == "uncommitted change" {
		t.Error("Close should have rolled back pending transaction")
	}
}

func TestSessionScopedView_CommitWrite_NoTransaction(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	// CommitWrite without BeginWrite should be a no-op
	err := view.CommitWrite()
	if err != nil {
		t.Errorf("CommitWrite without transaction: expected nil, got %v", err)
	}
}

func TestSessionScopedView_RollbackWrite_NoTransaction(t *testing.T) {
	db, _, idx, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   idx,
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	// Should not panic
	view.RollbackWrite()
}

func TestSessionScopedView_Search_NilLiveIndex(t *testing.T) {
	db, _, _, snapshotMgr := setupSessionViewDB(t)
	defer db.Close()

	cfg := SessionViewConfig{
		SessionID:   "session-1",
		DB:          db,
		SnapshotMgr: snapshotMgr,
		LiveIndex:   nil, // nil live index
		Isolation:   IsolationReadCommitted,
	}

	view := NewSessionScopedView(cfg)
	defer view.Close()

	// Should not panic, return nil
	results := view.Search([]float32{1, 0, 0, 0}, 10)
	if results != nil {
		t.Error("Search with nil live index should return nil")
	}
}

func TestSessionScopedView_IsValid(t *testing.T) {
	tests := []struct {
		level IsolationLevel
		valid bool
	}{
		{IsolationReadCommitted, true},
		{IsolationRepeatableRead, true},
		{IsolationSessionLocal, true},
		{IsolationLevel(99), false},
	}

	for _, tc := range tests {
		got := tc.level.IsValid()
		if got != tc.valid {
			t.Errorf("IsolationLevel(%d).IsValid() = %v, want %v", tc.level, got, tc.valid)
		}
	}
}
