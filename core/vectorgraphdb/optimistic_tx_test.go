package vectorgraphdb

import (
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/migrations"
)

// setupOptimisticTxDB sets up a test database for optimistic transaction tests.
func setupOptimisticTxDB(t *testing.T) (*VectorGraphDB, *VersionedNodeStore) {
	t.Helper()
	db, _ := setupTestDB(t)

	// Apply the version column migration
	migration := migrations.NewAddVersionColumnMigration(db.DB())
	if err := migration.Apply(); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	ns := NewNodeStore(db, nil)
	vns := NewVersionedNodeStore(ns)

	return db, vns
}

// insertOptimisticTestNode inserts a test node for optimistic transaction tests.
func insertOptimisticTestNode(t *testing.T, vns *VersionedNodeStore, id string) *GraphNode {
	t.Helper()
	node := &GraphNode{
		ID:       id,
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Name:     id,
		Content:  "test content for " + id,
		Metadata: map[string]any{"test": "data"},
	}
	embedding := []float32{1, 0, 0, 0}

	if err := vns.InsertNode(node, embedding); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	return node
}

func TestOptimisticTx_SuccessfulCommit(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")

	// Read the node
	node, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Modify and write
	node.Content = "updated content"
	if err := tx.Write(node); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Commit should succeed
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if !tx.IsCommitted() {
		t.Error("expected transaction to be committed")
	}

	// Verify the change was applied
	updated, err := vns.GetNodeWithVersion("node1")
	if err != nil {
		t.Fatalf("GetNodeWithVersion: %v", err)
	}
	if updated.Content != "updated content" {
		t.Errorf("expected content 'updated content', got %q", updated.Content)
	}
	if updated.Version != 2 {
		t.Errorf("expected version 2, got %d", updated.Version)
	}
}

func TestOptimisticTx_CommitFailsOnStaleRead(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	// Start transaction 1
	tx1 := vns.BeginOptimistic("session1")
	node1, err := tx1.Read("node1")
	if err != nil {
		t.Fatalf("tx1 Read: %v", err)
	}

	// Start transaction 2 and commit a change
	tx2 := vns.BeginOptimistic("session2")
	node2, err := tx2.Read("node1")
	if err != nil {
		t.Fatalf("tx2 Read: %v", err)
	}
	node2.Content = "tx2 update"
	if err := tx2.Write(node2); err != nil {
		t.Fatalf("tx2 Write: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2 Commit: %v", err)
	}

	// Invalidate cache to ensure tx1 sees fresh version
	vns.InvalidateVersionCache("node1")

	// Now tx1 tries to commit - should fail with stale read
	node1.Content = "tx1 update"
	if err := tx1.Write(node1); err != nil {
		t.Fatalf("tx1 Write: %v", err)
	}

	err = tx1.Commit()
	if err == nil {
		t.Fatal("expected Commit to fail with stale read")
	}

	if !errors.Is(err, ErrOCCConflictStale) {
		t.Errorf("expected ErrOCCConflictStale, got %v", err)
	}

	conflictErr, ok := GetConflictError(err)
	if !ok {
		t.Fatal("expected ConflictError")
	}
	if conflictErr.NodeID != "node1" {
		t.Errorf("expected nodeID 'node1', got %q", conflictErr.NodeID)
	}
	if conflictErr.SessionID != "session1" {
		t.Errorf("expected sessionID 'session1', got %q", conflictErr.SessionID)
	}
}

func TestOptimisticTx_CommitFailsOnConcurrentWrite(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	// Start transaction 1, read and prepare write
	tx1 := vns.BeginOptimistic("session1")
	node1, err := tx1.Read("node1")
	if err != nil {
		t.Fatalf("tx1 Read: %v", err)
	}
	node1.Content = "tx1 update"
	if err := tx1.Write(node1); err != nil {
		t.Fatalf("tx1 Write: %v", err)
	}

	// Directly update the version in the database to simulate concurrent write
	_, err = db.DB().Exec("UPDATE nodes SET version = version + 1, content = 'concurrent update' WHERE id = ?", "node1")
	if err != nil {
		t.Fatalf("direct update: %v", err)
	}
	vns.InvalidateVersionCache("node1")

	// tx1 commit should fail - the read set validation should catch this
	err = tx1.Commit()
	if err == nil {
		t.Fatal("expected Commit to fail")
	}

	if !errors.Is(err, ErrOCCConflictStale) {
		t.Errorf("expected ErrOCCConflictStale, got %v", err)
	}
}

func TestOptimisticTx_CommitFailsOnDeletedNode(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	// Start transaction and read
	tx := vns.BeginOptimistic("session1")
	node, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Delete the node outside the transaction
	if err := vns.DeleteNode("node1"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	vns.InvalidateVersionCache("node1")

	// Prepare write and try to commit
	node.Content = "updated"
	if err := tx.Write(node); err != nil {
		t.Fatalf("Write: %v", err)
	}

	err = tx.Commit()
	if err == nil {
		t.Fatal("expected Commit to fail on deleted node")
	}

	if !errors.Is(err, ErrOCCConflictDeleted) {
		t.Errorf("expected ErrOCCConflictDeleted, got %v", err)
	}
}

func TestOptimisticTx_RollbackClearsState(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")

	// Read and write
	node, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	node.Content = "should not persist"
	if err := tx.Write(node); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if tx.ReadSetSize() == 0 {
		t.Error("expected non-empty read set before rollback")
	}
	if tx.WriteSetSize() == 0 {
		t.Error("expected non-empty write set before rollback")
	}

	// Rollback
	tx.Rollback()

	if tx.ReadSetSize() != 0 {
		t.Errorf("expected empty read set after rollback, got %d", tx.ReadSetSize())
	}
	if tx.WriteSetSize() != 0 {
		t.Errorf("expected empty write set after rollback, got %d", tx.WriteSetSize())
	}
	if !tx.IsRolledBack() {
		t.Error("expected transaction to be rolled back")
	}

	// Verify original content unchanged
	original, err := vns.GetNode("node1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if original.Content == "should not persist" {
		t.Error("rollback should not have persisted changes")
	}
}

func TestOptimisticTx_DoubleCommitReturnsError(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	tx := vns.BeginOptimistic("session1")

	// First commit (empty transaction is fine)
	if err := tx.Commit(); err != nil {
		t.Fatalf("first Commit: %v", err)
	}

	// Second commit should fail
	err := tx.Commit()
	if err != ErrAlreadyCommitted {
		t.Errorf("expected ErrAlreadyCommitted, got %v", err)
	}
}

func TestOptimisticTx_MultipleReadsSameNode(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")

	// Read the same node multiple times
	node1, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}

	node2, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}

	// Should have same ID
	if node1.ID != node2.ID {
		t.Error("multiple reads should return same node")
	}

	// Read set should only have one entry
	if tx.ReadSetSize() != 1 {
		t.Errorf("expected read set size 1, got %d", tx.ReadSetSize())
	}

	// Commit should still work
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestOptimisticTx_MultipleWritesSameNode(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")

	// Read the node
	node, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Write multiple times
	node.Content = "first update"
	if err := tx.Write(node); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	node.Content = "second update"
	if err := tx.Write(node); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	node.Content = "final update"
	if err := tx.Write(node); err != nil {
		t.Fatalf("third Write: %v", err)
	}

	// Write set should only have one entry (last write wins)
	if tx.WriteSetSize() != 1 {
		t.Errorf("expected write set size 1, got %d", tx.WriteSetSize())
	}

	// Commit
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify final value
	updated, err := vns.GetNode("node1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if updated.Content != "final update" {
		t.Errorf("expected 'final update', got %q", updated.Content)
	}
}

func TestOptimisticTx_EmptyTransactionCommits(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	tx := vns.BeginOptimistic("session1")

	// Commit empty transaction
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit empty transaction: %v", err)
	}

	if !tx.IsCommitted() {
		t.Error("expected transaction to be committed")
	}
}

func TestOptimisticTx_ReadAfterWrite(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")

	// Read
	node, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Write
	node.Content = "updated in tx"
	if err := tx.Write(node); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read again - should return the buffered write
	readAgain, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("Read after Write: %v", err)
	}

	if readAgain.Content != "updated in tx" {
		t.Errorf("expected buffered write content, got %q", readAgain.Content)
	}
}

func TestOptimisticTx_ReadNonExistentNode(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	tx := vns.BeginOptimistic("session1")

	_, err := tx.Read("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestOptimisticTx_OperationsAfterCommit(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Read after commit should fail
	_, err := tx.Read("node1")
	if err != ErrAlreadyCommitted {
		t.Errorf("Read after commit: expected ErrAlreadyCommitted, got %v", err)
	}

	// Write after commit should fail
	err = tx.Write(&GraphNode{ID: "node1"})
	if err != ErrAlreadyCommitted {
		t.Errorf("Write after commit: expected ErrAlreadyCommitted, got %v", err)
	}
}

func TestOptimisticTx_OperationsAfterRollback(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")
	tx.Rollback()

	// Read after rollback should fail
	_, err := tx.Read("node1")
	if err != ErrAlreadyRolledBack {
		t.Errorf("Read after rollback: expected ErrAlreadyRolledBack, got %v", err)
	}

	// Write after rollback should fail
	err = tx.Write(&GraphNode{ID: "node1"})
	if err != ErrAlreadyRolledBack {
		t.Errorf("Write after rollback: expected ErrAlreadyRolledBack, got %v", err)
	}

	// Commit after rollback should fail
	err = tx.Commit()
	if err != ErrAlreadyRolledBack {
		t.Errorf("Commit after rollback: expected ErrAlreadyRolledBack, got %v", err)
	}
}

func TestOptimisticTx_SessionID(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	tx := vns.BeginOptimistic("my-session-123")

	if tx.SessionID() != "my-session-123" {
		t.Errorf("expected session ID 'my-session-123', got %q", tx.SessionID())
	}
}

func TestOptimisticTx_MultipleNodesTransaction(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")
	insertOptimisticTestNode(t, vns, "node2")
	insertOptimisticTestNode(t, vns, "node3")

	tx := vns.BeginOptimistic("session1")

	// Read all nodes
	node1, _ := tx.Read("node1")
	node2, _ := tx.Read("node2")
	node3, _ := tx.Read("node3")

	// Modify all
	node1.Content = "updated1"
	node2.Content = "updated2"
	node3.Content = "updated3"

	tx.Write(node1)
	tx.Write(node2)
	tx.Write(node3)

	if tx.ReadSetSize() != 3 {
		t.Errorf("expected read set size 3, got %d", tx.ReadSetSize())
	}
	if tx.WriteSetSize() != 3 {
		t.Errorf("expected write set size 3, got %d", tx.WriteSetSize())
	}

	// Commit
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify all changes
	for _, tc := range []struct {
		id      string
		content string
	}{
		{"node1", "updated1"},
		{"node2", "updated2"},
		{"node3", "updated3"},
	} {
		node, _ := vns.GetNode(tc.id)
		if node.Content != tc.content {
			t.Errorf("node %s: expected content %q, got %q", tc.id, tc.content, node.Content)
		}
	}
}

func TestOptimisticTx_ConcurrentTransactions(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	// Create nodes for concurrent access
	for i := 0; i < 10; i++ {
		insertOptimisticTestNode(t, vns, nodeID(i))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// Run concurrent transactions on different nodes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx := vns.BeginOptimistic("session-" + nodeID(idx))

			node, err := tx.Read(nodeID(idx))
			if err != nil {
				errCh <- err
				return
			}

			node.Content = "concurrent update " + nodeID(idx)
			if err := tx.Write(node); err != nil {
				errCh <- err
				return
			}

			if err := tx.Commit(); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent transaction error: %v", err)
	}
}

func TestOptimisticTx_ConflictDetection(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "shared")

	successCount := 0
	conflictCount := 0
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Multiple transactions trying to update the same node
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			tx := vns.BeginOptimistic("session-" + nodeID(idx))
			node, err := tx.Read("shared")
			if err != nil {
				return
			}

			node.Content = "update from " + nodeID(idx)
			tx.Write(node)

			err = tx.Commit()
			mu.Lock()
			if err == nil {
				successCount++
			} else if IsConflictError(err) {
				conflictCount++
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// At least one should succeed, others may conflict
	if successCount == 0 {
		t.Error("expected at least one transaction to succeed")
	}

	t.Logf("success: %d, conflicts: %d", successCount, conflictCount)
}

func TestOptimisticTx_ReadOnlyTransaction(t *testing.T) {
	db, vns := setupOptimisticTxDB(t)
	defer db.Close()

	insertOptimisticTestNode(t, vns, "node1")

	tx := vns.BeginOptimistic("session1")

	// Only read, no writes
	node, err := tx.Read("node1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if node.ID != "node1" {
		t.Errorf("expected node ID 'node1', got %q", node.ID)
	}

	// Commit read-only transaction
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Version should not have changed
	updated, _ := vns.GetNodeWithVersion("node1")
	if updated.Version != 1 {
		t.Errorf("expected version 1 (unchanged), got %d", updated.Version)
	}
}
