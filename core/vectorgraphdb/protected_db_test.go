package vectorgraphdb

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func setupProtectedDB(t *testing.T) (*ProtectedVectorDB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Create hnsw_nodes table for testing (not in main schema)
	_, err = db.DB().Exec(`CREATE TABLE IF NOT EXISTS hnsw_nodes (
		node_id TEXT PRIMARY KEY
	)`)
	if err != nil {
		t.Fatalf("failed to create hnsw_nodes table: %v", err)
	}

	cfg := DefaultProtectionConfig()
	cfg.SnapshotGCInterval = 100 * time.Millisecond
	cfg.IntegrityConfig.PeriodicInterval = 100 * time.Millisecond

	deps := ProtectedDBDeps{
		DB:          db,
		VectorIndex: newMockVectorIndex(),
		SnapshotMgr: newMockSnapshotManager(),
	}

	pdb, err := NewProtectedVectorDB(deps, cfg)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create protected db: %v", err)
	}

	return pdb, dbPath
}

func TestNewProtectedVectorDB_CreatesValidInstance(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	if pdb.DB() == nil {
		t.Error("DB() returned nil")
	}
	if pdb.SnapshotManager() == nil {
		t.Error("SnapshotManager() returned nil")
	}
	if pdb.NodeStore() == nil {
		t.Error("NodeStore() returned nil")
	}
	if pdb.Validator() == nil {
		t.Error("Validator() returned nil")
	}
}

func TestNewProtectedVectorDB_ValidatesConfig(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Invalid config: negative snapshot retention
	cfg := DefaultProtectionConfig()
	cfg.SnapshotRetention = -1 * time.Second

	deps := ProtectedDBDeps{
		DB:          db,
		VectorIndex: newMockVectorIndex(),
		SnapshotMgr: newMockSnapshotManager(),
	}

	_, err = NewProtectedVectorDB(deps, cfg)
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}

func TestBeginSession_CreatesSessionScopedView(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	view, err := pdb.BeginSession("session1")
	if err != nil {
		t.Fatalf("BeginSession: %v", err)
	}
	defer pdb.EndSession("session1")

	if view == nil {
		t.Fatal("BeginSession returned nil view")
	}
	if view.SessionID() != "session1" {
		t.Errorf("SessionID = %s, want session1", view.SessionID())
	}
	if view.Isolation() != IsolationReadCommitted {
		t.Errorf("Isolation = %v, want %v", view.Isolation(), IsolationReadCommitted)
	}
}

func TestBeginSessionWithIsolation_CreatesViewWithSpecificIsolation(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	view, err := pdb.BeginSessionWithIsolation("session1", IsolationRepeatableRead)
	if err != nil {
		t.Fatalf("BeginSessionWithIsolation: %v", err)
	}
	defer pdb.EndSession("session1")

	if view.Isolation() != IsolationRepeatableRead {
		t.Errorf("Isolation = %v, want %v", view.Isolation(), IsolationRepeatableRead)
	}
}

func TestBeginSession_RejectsduplicateSession(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	_, err := pdb.BeginSession("session1")
	if err != nil {
		t.Fatalf("BeginSession: %v", err)
	}
	defer pdb.EndSession("session1")

	_, err = pdb.BeginSession("session1")
	if err != ErrSessionAlreadyExists {
		t.Errorf("expected ErrSessionAlreadyExists, got %v", err)
	}
}

func TestGetSession_ReturnsExistingSession(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	created, err := pdb.BeginSession("session1")
	if err != nil {
		t.Fatalf("BeginSession: %v", err)
	}
	defer pdb.EndSession("session1")

	retrieved, err := pdb.GetSession("session1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if retrieved != created {
		t.Error("GetSession returned different view instance")
	}
}

func TestGetSession_ReturnsErrorForNonExistent(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	_, err := pdb.GetSession("nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestEndSession_RemovesSession(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	_, err := pdb.BeginSession("session1")
	if err != nil {
		t.Fatalf("BeginSession: %v", err)
	}

	err = pdb.EndSession("session1")
	if err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	_, err = pdb.GetSession("session1")
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound after EndSession, got %v", err)
	}
}

func TestEndSession_ReturnsErrorForNonExistent(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	err := pdb.EndSession("nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSessionCount_TracksActiveSessions(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	if pdb.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", pdb.SessionCount())
	}

	pdb.BeginSession("session1")
	if pdb.SessionCount() != 1 {
		t.Errorf("SessionCount = %d, want 1", pdb.SessionCount())
	}

	pdb.BeginSession("session2")
	if pdb.SessionCount() != 2 {
		t.Errorf("SessionCount = %d, want 2", pdb.SessionCount())
	}

	pdb.EndSession("session1")
	if pdb.SessionCount() != 1 {
		t.Errorf("SessionCount = %d, want 1", pdb.SessionCount())
	}
}

func TestValidateIntegrity_RunsChecks(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	result, err := pdb.ValidateIntegrity()
	if err != nil {
		t.Fatalf("ValidateIntegrity: %v", err)
	}

	if result == nil {
		t.Fatal("ValidateIntegrity returned nil result")
	}
	if result.ChecksRun == 0 {
		t.Error("expected at least one check to run")
	}
}

func TestStart_BeginsBackgroundTasks(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	pdb.Start(ctx)

	// Give background tasks time to start
	time.Sleep(50 * time.Millisecond)

	// Starting again should be a no-op (idempotent)
	pdb.Start(ctx)
}

func TestClose_StopsAllTasks(t *testing.T) {
	pdb, _ := setupProtectedDB(t)

	ctx := context.Background()
	pdb.Start(ctx)

	// Create some sessions
	pdb.BeginSession("session1")
	pdb.BeginSession("session2")

	err := pdb.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify sessions are closed
	if pdb.SessionCount() != 0 {
		t.Errorf("SessionCount after Close = %d, want 0", pdb.SessionCount())
	}
}

func TestConcurrentSessionCreation(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	var wg sync.WaitGroup
	numSessions := 50
	errCh := make(chan error, numSessions)

	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "session" + string(rune('A'+id%26)) + string(rune('0'+id/26))
			_, err := pdb.BeginSession(sessionID)
			if err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent session creation error: %v", err)
	}

	if pdb.SessionCount() != numSessions {
		t.Errorf("SessionCount = %d, want %d", pdb.SessionCount(), numSessions)
	}
}

func TestProtectedVectorDB_ConfigAccessor(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	cfg := pdb.Config()
	if cfg.DefaultIsolation != IsolationReadCommitted {
		t.Errorf("DefaultIsolation = %v, want %v", cfg.DefaultIsolation, IsolationReadCommitted)
	}
}

func TestProtectedVectorDB_ValidateAndRepair(t *testing.T) {
	pdb, _ := setupProtectedDB(t)
	defer pdb.Close()

	// Use SQLite-compatible checks only (skip superseded_cycle which uses PostgreSQL syntax)
	sqliteChecks := sqliteCompatibleChecks()
	result, err := pdb.Validator().ValidateAndRepairWithChecks(sqliteChecks)
	if err != nil {
		t.Fatalf("ValidateAndRepair: %v", err)
	}

	if result == nil {
		t.Fatal("ValidateAndRepair returned nil result")
	}
}

// sqliteCompatibleChecks returns checks that work with SQLite
func sqliteCompatibleChecks() []InvariantCheck {
	var checks []InvariantCheck
	for _, check := range StandardChecks() {
		// Skip superseded_cycle which uses PostgreSQL's ARRAY and ANY() functions
		if check.Name == "superseded_cycle" {
			continue
		}
		checks = append(checks, check)
	}
	return checks
}
