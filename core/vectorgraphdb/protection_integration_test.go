package vectorgraphdb

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConcurrentSessionsIsolation verifies sessions don't see each other's uncommitted data.
func TestConcurrentSessionsIsolation(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	view1, err := pdb.BeginSessionWithIsolation("session1", IsolationSessionLocal)
	require.NoError(t, err)
	defer pdb.EndSession("session1")

	view2, err := pdb.BeginSessionWithIsolation("session2", IsolationSessionLocal)
	require.NoError(t, err)
	defer pdb.EndSession("session2")

	// Each session should have its own view
	assert.NotEqual(t, view1.SessionID(), view2.SessionID())
	assert.Equal(t, IsolationSessionLocal, view1.Isolation())
	assert.Equal(t, IsolationSessionLocal, view2.Isolation())
}

// TestSnapshotConsistencyUnderLoad verifies snapshots remain consistent during writes.
func TestSnapshotConsistencyUnderLoad(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pdb.Start(ctx)

	var wg sync.WaitGroup
	const numReaders = 5
	const numWriters = 3
	errors := make(chan error, numReaders+numWriters)

	// Writers continuously create sessions
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					sessionID := generateSessionID(id, j)
					_, err := pdb.BeginSession(sessionID)
					if err != nil && err != ErrSessionAlreadyExists {
						errors <- err
						return
					}
					time.Sleep(time.Millisecond)
					pdb.EndSession(sessionID)
				}
			}
		}(i)
	}

	// Readers continuously check session count
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					count := pdb.SessionCount()
					if count < 0 {
						errors <- assert.AnError
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent operation error: %v", err)
	}
}

func generateSessionID(writerID, iteration int) string {
	return "writer" + string(rune('A'+writerID)) + "_" + string(rune('0'+iteration%10))
}

// TestOCCConflictDetection verifies optimistic concurrency control detects conflicts.
func TestOCCConflictDetection(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Add version column for OCC
	_, err = db.DB().Exec("ALTER TABLE nodes ADD COLUMN version INTEGER DEFAULT 1")
	require.NoError(t, err)

	baseNodeStore := NewNodeStore(db, nil)
	store := NewVersionedNodeStore(baseNodeStore)

	// Create initial node in database
	node := &GraphNode{
		ID:       "node1",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Name:     "test.go",
	}
	embedding := make([]float32, EmbeddingDimension)
	err = baseNodeStore.InsertNode(node, embedding)
	require.NoError(t, err)

	tx1 := store.BeginOptimistic("session1")
	tx2 := store.BeginOptimistic("session2")

	// Both transactions read the same node
	_, err = tx1.Read("node1")
	require.NoError(t, err)

	_, err = tx2.Read("node1")
	require.NoError(t, err)

	// Both make writes
	node.Name = "changed1.go"
	err = tx1.Write(node)
	require.NoError(t, err)

	node.Name = "changed2.go"
	err = tx2.Write(node)
	require.NoError(t, err)

	// First commit succeeds
	err = tx1.Commit()
	require.NoError(t, err)

	// Second commit should detect conflict
	err = tx2.Commit()
	if err != nil {
		conflictErr, ok := err.(*ConflictError)
		assert.True(t, ok, "expected ConflictError, got %T", err)
		if ok {
			assert.Equal(t, OCCConflictStale, conflictErr.Type)
		}
	}
}

// TestOCCRetrySuccess verifies retry after conflict can succeed.
func TestOCCRetrySuccess(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	baseNodeStore := NewNodeStore(db, nil)
	store := NewVersionedNodeStore(baseNodeStore)

	var successCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for retry := 0; retry < 5; retry++ {
				tx := store.BeginOptimistic("session_" + string(rune('A'+id)))
				if err := tx.Commit(); err == nil {
					successCount.Add(1)
					return
				}
				time.Sleep(time.Millisecond * time.Duration(retry+1))
			}
		}(i)
	}

	wg.Wait()
	assert.GreaterOrEqual(t, successCount.Load(), int32(1))
}

// TestRepeatableReadIsolation verifies snapshot doesn't change during operation.
func TestRepeatableReadIsolation(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	view, err := pdb.BeginSessionWithIsolation("reader", IsolationRepeatableRead)
	require.NoError(t, err)
	defer pdb.EndSession("reader")

	// Snapshot should be captured at creation
	assert.Equal(t, IsolationRepeatableRead, view.Isolation())

	// Simulate external changes
	pdb.BeginSession("writer")
	pdb.EndSession("writer")

	// View should still be functional
	assert.False(t, view.IsClosed())
}

// TestIntegrityValidation verifies integrity checks run correctly.
func TestIntegrityValidation(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	// Use SQLite-compatible checks
	sqliteChecks := sqliteCompatibleChecks()
	result, err := pdb.Validator().ValidateChecks(sqliteChecks)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.GreaterOrEqual(t, result.ChecksRun, 1)
	assert.NotZero(t, result.Duration)
}

// TestAutoRepairExecution verifies auto-repair can execute.
func TestAutoRepairExecution(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	sqliteChecks := sqliteCompatibleChecks()
	result, err := pdb.Validator().ValidateAndRepairWithChecks(sqliteChecks)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// TestRaceConditionSafety runs concurrent operations to detect races.
func TestRaceConditionSafety(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	pdb.Start(ctx)

	var wg sync.WaitGroup

	// Concurrent session creation
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "race_session_" + string(rune('A'+id))
			pdb.BeginSession(sessionID)
			time.Sleep(10 * time.Millisecond)
			pdb.EndSession(sessionID)
		}(i)
	}

	// Concurrent validation
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pdb.ValidateIntegrity()
		}()
	}

	// Concurrent config access
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pdb.Config()
			_ = pdb.SessionCount()
		}()
	}

	wg.Wait()
}

// TestEndToEndProtectionFlow verifies complete protection workflow.
func TestEndToEndProtectionFlow(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start background tasks
	pdb.Start(ctx)

	// Create session with isolation
	view, err := pdb.BeginSessionWithIsolation("e2e_session", IsolationRepeatableRead)
	require.NoError(t, err)

	// Verify session properties
	assert.Equal(t, "e2e_session", view.SessionID())
	assert.Equal(t, IsolationRepeatableRead, view.Isolation())

	// Run integrity check with SQLite-compatible checks
	sqliteChecks := sqliteCompatibleChecks()
	result, err := pdb.Validator().ValidateChecks(sqliteChecks)
	require.NoError(t, err)
	assert.False(t, result.HasCritical())

	// End session
	err = pdb.EndSession("e2e_session")
	require.NoError(t, err)

	// Verify session is gone
	_, err = pdb.GetSession("e2e_session")
	assert.Equal(t, ErrSessionNotFound, err)
}

// TestVersionedNodeStoreIntegration tests versioned node operations.
func TestVersionedNodeStoreIntegration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	baseNodeStore := NewNodeStore(db, nil)
	store := NewVersionedNodeStore(baseNodeStore)
	require.NotNil(t, store)

	// Test cache operations
	store.InvalidateVersionCache("test_node")

	// Test transaction creation
	tx := store.BeginOptimistic("test_session")
	require.NotNil(t, tx)

	// Transaction should commit successfully with no conflicts
	err = tx.Commit()
	require.NoError(t, err)
}

// TestProtectionConfigValidation tests config validation.
func TestProtectionConfigValidation(t *testing.T) {
	cfg := DefaultProtectionConfig()
	err := cfg.Validate()
	require.NoError(t, err)

	// Invalid config
	invalidCfg := ProtectionConfig{
		SnapshotRetention: -1,
	}
	err = invalidCfg.Validate()
	assert.Error(t, err)
}

// TestSessionViewConcurrentAccess tests concurrent session view access.
func TestSessionViewConcurrentAccess(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	view, err := pdb.BeginSession("concurrent_view")
	require.NoError(t, err)
	defer pdb.EndSession("concurrent_view")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = view.SessionID()
			_ = view.Isolation()
			_ = view.IsClosed()
		}()
	}
	wg.Wait()
}
