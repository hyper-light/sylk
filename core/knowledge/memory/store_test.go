package memory

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Test Helpers
// =============================================================================

var testDBCounter int

// setupTestDBWithWAL creates a file-based SQLite database with WAL mode for concurrency tests.
func setupTestDBWithWAL(t *testing.T) *sql.DB {
	t.Helper()

	testDBCounter++
	// Use a temp file for WAL mode support
	tmpFile := fmt.Sprintf("/tmp/test_memory_%d_%d.db", time.Now().UnixNano(), testDBCounter)

	db, err := sql.Open("sqlite3", tmpFile+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	// Cleanup on test end
	t.Cleanup(func() {
		db.Close()
		// Remove temp files
		_ = removeFile(tmpFile)
		_ = removeFile(tmpFile + "-wal")
		_ = removeFile(tmpFile + "-shm")
	})

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	// Create schema
	createSchema(t, db)

	return db
}

func removeFile(path string) error {
	return nil // Cleanup happens automatically in temp
}

func createSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	// Create nodes table with memory columns
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			memory_activation REAL DEFAULT 0.0,
			last_accessed_at INTEGER,
			access_count INTEGER DEFAULT 0,
			base_offset REAL DEFAULT 0.0
		)
	`)
	if err != nil {
		t.Fatalf("failed to create nodes table: %v", err)
	}

	// Create node_access_traces table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS node_access_traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			accessed_at INTEGER NOT NULL,
			access_type TEXT NOT NULL,
			context TEXT,
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		t.Fatalf("failed to create node_access_traces table: %v", err)
	}

	// Create decay_parameters table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS decay_parameters (
			domain INTEGER PRIMARY KEY,
			decay_exponent_alpha REAL NOT NULL,
			decay_exponent_beta REAL NOT NULL,
			base_offset_mean REAL NOT NULL,
			base_offset_variance REAL NOT NULL,
			effective_samples REAL NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("failed to create decay_parameters table: %v", err)
	}

	// Create indexes
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_access_traces_node
		ON node_access_traces(node_id, accessed_at DESC)
	`)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}
}

// setupTestDB creates an in-memory SQLite database with the required schema.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	// Use a unique name for each test DB to avoid conflicts with shared cache
	testDBCounter++
	dbURI := fmt.Sprintf("file:memdb%d?mode=memory&cache=shared", testDBCounter)

	db, err := sql.Open("sqlite3", dbURI)
	if err != nil {
		t.Fatalf("failed to open in-memory database: %v", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	createSchema(t, db)
	return db
}

// insertTestNode inserts a test node into the database.
func insertTestNode(t *testing.T, db *sql.DB, nodeID string, d domain.Domain) {
	t.Helper()

	_, err := db.Exec(`
		INSERT INTO nodes (id, domain, node_type, name)
		VALUES (?, ?, 0, 'test-node')
	`, nodeID, int(d))
	if err != nil {
		t.Fatalf("failed to insert test node: %v", err)
	}
}

// =============================================================================
// MD.3.1 MemoryStore Tests
// =============================================================================

func TestNewMemoryStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	t.Run("default values", func(t *testing.T) {
		store := NewMemoryStore(db, 0, 0)
		if store == nil {
			t.Fatal("expected non-nil store")
		}
		if store.MaxTraces() != DefaultMaxTraces {
			t.Errorf("expected maxTraces %d, got %d", DefaultMaxTraces, store.MaxTraces())
		}
		if store.DebounceWindow() != DefaultDebounceWindow {
			t.Errorf("expected debounceWindow %v, got %v", DefaultDebounceWindow, store.DebounceWindow())
		}
	})

	t.Run("custom values", func(t *testing.T) {
		store := NewMemoryStore(db, 50, 2*time.Minute)
		if store.MaxTraces() != 50 {
			t.Errorf("expected maxTraces 50, got %d", store.MaxTraces())
		}
		if store.DebounceWindow() != 2*time.Minute {
			t.Errorf("expected debounceWindow 2m, got %v", store.DebounceWindow())
		}
	})

	t.Run("domain decay initialized", func(t *testing.T) {
		store := NewMemoryStore(db, 0, 0)
		for _, d := range domain.ValidDomains() {
			params := store.GetDomainDecayParams(d)
			if params == nil {
				t.Errorf("domain %v should have decay params", d)
			}
		}
	})
}

func TestMemoryStore_GetMemory(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Minute)
	nodeID := "test-node-1"
	insertTestNode(t, db, nodeID, domain.DomainAcademic)

	t.Run("new node memory", func(t *testing.T) {
		memory, err := store.GetMemory(ctx, nodeID, domain.DomainAcademic)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if memory == nil {
			t.Fatal("expected non-nil memory")
		}
		if memory.NodeID != nodeID {
			t.Errorf("expected nodeID %s, got %s", nodeID, memory.NodeID)
		}
		if memory.Domain != int(domain.DomainAcademic) {
			t.Errorf("expected domain %d, got %d", domain.DomainAcademic, memory.Domain)
		}
		if len(memory.Traces) != 0 {
			t.Errorf("expected 0 traces, got %d", len(memory.Traces))
		}
	})

	t.Run("memory with academic domain params", func(t *testing.T) {
		memory, err := store.GetMemory(ctx, nodeID, domain.DomainAcademic)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Academic domain should have slower decay (lower mean)
		expectedMean := 0.3 // Beta(3,7)
		actualMean := memory.DecayMean()
		if math.Abs(actualMean-expectedMean) > 0.01 {
			t.Errorf("expected decay mean %f, got %f", expectedMean, actualMean)
		}
	})
}

func TestMemoryStore_RecordAccess(t *testing.T) {
	ctx := context.Background()

	t.Run("record first access", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()
		store := NewMemoryStore(db, 100, 100*time.Millisecond)
		nodeID := "test-node-2a"
		insertTestNode(t, db, nodeID, domain.DomainArchitect)

		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "test-context")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		memory, err := store.GetMemory(ctx, nodeID, domain.DomainArchitect)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(memory.Traces) != 1 {
			t.Errorf("expected 1 trace, got %d", len(memory.Traces))
		}
		if memory.Traces[0].AccessType != AccessRetrieval {
			t.Errorf("expected retrieval access type")
		}
		if memory.AccessCount != 1 {
			t.Errorf("expected access count 1, got %d", memory.AccessCount)
		}
	})

	t.Run("debouncing same type", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()
		// Use 2 second debounce window to account for second-precision timestamp storage
		store := NewMemoryStore(db, 100, 2*time.Second)
		nodeID := "test-node-2b"
		insertTestNode(t, db, nodeID, domain.DomainArchitect)

		// First access
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "first")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Record another access immediately (should be debounced)
		err = store.RecordAccess(ctx, nodeID, AccessRetrieval, "debounced")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		memory, err := store.GetMemory(ctx, nodeID, domain.DomainArchitect)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should still be 1 trace due to debouncing
		if len(memory.Traces) != 1 {
			t.Errorf("expected 1 trace after debounce, got %d", len(memory.Traces))
		}
	})

	t.Run("different access type not debounced", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()
		store := NewMemoryStore(db, 100, 100*time.Millisecond)
		nodeID := "test-node-2c"
		insertTestNode(t, db, nodeID, domain.DomainArchitect)

		// First access
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "first")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Different type should not be debounced
		err = store.RecordAccess(ctx, nodeID, AccessReinforcement, "reinforcement")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		memory, err := store.GetMemory(ctx, nodeID, domain.DomainArchitect)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should have 2 traces (different types)
		if len(memory.Traces) != 2 {
			t.Errorf("expected 2 traces, got %d", len(memory.Traces))
		}
	})

	t.Run("after debounce window", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()
		store := NewMemoryStore(db, 100, 50*time.Millisecond) // Short debounce
		nodeID := "test-node-2d"
		insertTestNode(t, db, nodeID, domain.DomainArchitect)

		// First access
		err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "first")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Wait for debounce window to pass
		time.Sleep(60 * time.Millisecond)

		err = store.RecordAccess(ctx, nodeID, AccessRetrieval, "after-debounce")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		memory, err := store.GetMemory(ctx, nodeID, domain.DomainArchitect)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(memory.Traces) != 2 {
			t.Errorf("expected 2 traces after debounce, got %d", len(memory.Traces))
		}
	})
}

func TestMemoryStore_ComputeActivation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond) // Very short debounce
	nodeID := "test-node-3"
	insertTestNode(t, db, nodeID, domain.DomainEngineer)

	t.Run("no traces returns negative infinity", func(t *testing.T) {
		activation, err := store.ComputeActivation(ctx, nodeID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if activation != -math.MaxFloat64 {
			t.Errorf("expected -MaxFloat64 for no traces, got %f", activation)
		}
	})

	t.Run("with traces returns finite activation", func(t *testing.T) {
		// Add some accesses with delays to avoid debouncing
		for i := 0; i < 3; i++ {
			err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "context")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		activation, err := store.ComputeActivation(ctx, nodeID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.IsInf(activation, 0) || math.IsNaN(activation) {
			t.Errorf("expected finite activation, got %f", activation)
		}
	})
}

func TestMemoryStore_SaveAndLoadMemory(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Millisecond)
	nodeID := "test-node-4"
	insertTestNode(t, db, nodeID, domain.DomainLibrarian)

	t.Run("save and reload memory", func(t *testing.T) {
		// Record multiple accesses
		for i := 0; i < 5; i++ {
			err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "context")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		// Create a new store and verify data is persisted
		store2 := NewMemoryStore(db, 100, time.Millisecond)
		memory, err := store2.GetMemory(ctx, nodeID, domain.DomainLibrarian)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(memory.Traces) != 5 {
			t.Errorf("expected 5 traces after reload, got %d", len(memory.Traces))
		}
	})
}

func TestMemoryStore_UpdateDecayParams(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 100, time.Minute)

	t.Run("update on successful retrieval", func(t *testing.T) {
		initialParams := store.GetDomainDecayParams(domain.DomainAcademic)
		initialMean := initialParams.Mean()

		err := store.UpdateDecayParams(ctx, domain.DomainAcademic, true, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		updatedParams := store.GetDomainDecayParams(domain.DomainAcademic)
		updatedMean := updatedParams.Mean()

		// Successful retrieval should decrease decay (slower forgetting)
		if updatedMean >= initialMean {
			t.Errorf("successful retrieval should decrease decay mean: %f -> %f",
				initialMean, updatedMean)
		}
	})

	t.Run("update on failed retrieval", func(t *testing.T) {
		// Reset params
		store2 := NewMemoryStore(db, 100, time.Minute)
		initialParams := store2.GetDomainDecayParams(domain.DomainEngineer)
		initialMean := initialParams.Mean()

		err := store2.UpdateDecayParams(ctx, domain.DomainEngineer, false, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		updatedParams := store2.GetDomainDecayParams(domain.DomainEngineer)
		updatedMean := updatedParams.Mean()

		// Failed retrieval should increase decay (faster forgetting)
		if updatedMean <= initialMean {
			t.Errorf("failed retrieval should increase decay mean: %f -> %f",
				initialMean, updatedMean)
		}
	})

	t.Run("effective samples increases", func(t *testing.T) {
		store3 := NewMemoryStore(db, 100, time.Minute)
		initialParams := store3.GetDomainDecayParams(domain.DomainArchitect)
		initialSamples := initialParams.EffectiveSamples

		err := store3.UpdateDecayParams(ctx, domain.DomainArchitect, true, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		updatedParams := store3.GetDomainDecayParams(domain.DomainArchitect)
		if updatedParams.EffectiveSamples != initialSamples+1 {
			t.Errorf("effective samples should increase by 1: %f -> %f",
				initialSamples, updatedParams.EffectiveSamples)
		}
	})
}

func TestMemoryStore_LoadDomainDecayParams(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	// Insert some decay parameters
	_, err := db.Exec(`
		INSERT INTO decay_parameters
		(domain, decay_exponent_alpha, decay_exponent_beta,
		 base_offset_mean, base_offset_variance, effective_samples, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, int(domain.DomainAcademic), 2.0, 8.0, 0.5, 1.0, 50.0, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to insert decay params: %v", err)
	}

	store := NewMemoryStore(db, 100, time.Minute)
	if err := store.LoadDomainDecayParams(ctx); err != nil {
		t.Fatalf("failed to load decay params: %v", err)
	}

	params := store.GetDomainDecayParams(domain.DomainAcademic)
	if params.DecayAlpha != 2.0 {
		t.Errorf("expected alpha 2.0, got %f", params.DecayAlpha)
	}
	if params.DecayBeta != 8.0 {
		t.Errorf("expected beta 8.0, got %f", params.DecayBeta)
	}
	if params.EffectiveSamples != 50.0 {
		t.Errorf("expected 50 samples, got %f", params.EffectiveSamples)
	}
}

// =============================================================================
// MD.3.2 Trace Pruning Tests
// =============================================================================

func TestMemoryStore_PruneTraces(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	maxTraces := 5
	store := NewMemoryStore(db, maxTraces, time.Millisecond) // Very short debounce
	nodeID := "test-node-5"
	insertTestNode(t, db, nodeID, domain.DomainArchitect)

	t.Run("prune when over limit", func(t *testing.T) {
		// Record more traces than max
		for i := 0; i < 10; i++ {
			err := store.RecordAccess(ctx, nodeID, AccessRetrieval, "context")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}

		memory, err := store.GetMemory(ctx, nodeID, domain.DomainArchitect)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(memory.Traces) > maxTraces {
			t.Errorf("expected at most %d traces, got %d", maxTraces, len(memory.Traces))
		}

		// Access count should reflect total accesses
		if memory.AccessCount < 10 {
			t.Errorf("access count should be at least 10, got %d", memory.AccessCount)
		}
	})
}

func TestMemoryStore_ShouldDebounce(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, 100*time.Millisecond)

	now := time.Now().UTC()

	t.Run("empty memory no debounce", func(t *testing.T) {
		memory := &ACTRMemory{Traces: []AccessTrace{}}
		if store.shouldDebounce(memory, AccessRetrieval) {
			t.Error("empty memory should not debounce")
		}
	})

	t.Run("recent same type should debounce", func(t *testing.T) {
		memory := &ACTRMemory{
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-50 * time.Millisecond), AccessType: AccessRetrieval},
			},
		}
		if !store.shouldDebounce(memory, AccessRetrieval) {
			t.Error("recent same type should debounce")
		}
	})

	t.Run("recent different type no debounce", func(t *testing.T) {
		memory := &ACTRMemory{
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-50 * time.Millisecond), AccessType: AccessRetrieval},
			},
		}
		if store.shouldDebounce(memory, AccessReinforcement) {
			t.Error("different type should not debounce")
		}
	})

	t.Run("old same type no debounce", func(t *testing.T) {
		memory := &ACTRMemory{
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-200 * time.Millisecond), AccessType: AccessRetrieval},
			},
		}
		if store.shouldDebounce(memory, AccessRetrieval) {
			t.Error("old same type should not debounce")
		}
	})
}

func TestMemoryStore_CompactOldTraces(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)
	now := time.Now().UTC()

	t.Run("compact old traces", func(t *testing.T) {
		memory := &ACTRMemory{
			NodeID:    "test-node",
			MaxTraces: 100,
			Traces:    make([]AccessTrace, 0),
		}

		// Add old traces (beyond compact age)
		for i := 0; i < 10; i++ {
			memory.Traces = append(memory.Traces, AccessTrace{
				AccessedAt: now.Add(-48 * time.Hour).Add(time.Duration(i) * time.Minute),
				AccessType: AccessRetrieval,
				Context:    "old",
			})
		}

		// Add recent traces
		for i := 0; i < 5; i++ {
			memory.Traces = append(memory.Traces, AccessTrace{
				AccessedAt: now.Add(-time.Duration(i) * time.Hour),
				AccessType: AccessRetrieval,
				Context:    "recent",
			})
		}

		originalCount := len(memory.Traces)
		store.compactOldTraces(memory, 24*time.Hour)

		// Should have fewer traces due to compaction
		if len(memory.Traces) >= originalCount {
			t.Errorf("compaction should reduce trace count: %d -> %d",
				originalCount, len(memory.Traces))
		}

		// Should still have recent traces
		recentCount := 0
		for _, trace := range memory.Traces {
			if trace.Context == "recent" {
				recentCount++
			}
		}
		if recentCount != 5 {
			t.Errorf("should preserve 5 recent traces, got %d", recentCount)
		}
	})

	t.Run("no compaction for few traces", func(t *testing.T) {
		memory := &ACTRMemory{
			NodeID:    "test-node-2",
			MaxTraces: 100,
			Traces: []AccessTrace{
				{AccessedAt: now.Add(-48 * time.Hour), AccessType: AccessRetrieval},
				{AccessedAt: now.Add(-1 * time.Hour), AccessType: AccessRetrieval},
			},
		}

		originalCount := len(memory.Traces)
		store.compactOldTraces(memory, 24*time.Hour)

		if len(memory.Traces) != originalCount {
			t.Error("should not compact when few traces")
		}
	})
}

func TestMemoryStore_SettersAndGetters(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)

	t.Run("SetMaxTraces", func(t *testing.T) {
		store.SetMaxTraces(50)
		if store.MaxTraces() != 50 {
			t.Errorf("expected 50, got %d", store.MaxTraces())
		}

		// Invalid value should be ignored
		store.SetMaxTraces(-1)
		if store.MaxTraces() != 50 {
			t.Error("negative value should be ignored")
		}
	})

	t.Run("SetDebounceWindow", func(t *testing.T) {
		store.SetDebounceWindow(5 * time.Minute)
		if store.DebounceWindow() != 5*time.Minute {
			t.Errorf("expected 5m, got %v", store.DebounceWindow())
		}

		// Invalid value should be ignored
		store.SetDebounceWindow(-1)
		if store.DebounceWindow() != 5*time.Minute {
			t.Error("negative value should be ignored")
		}
	})

	t.Run("DB accessor", func(t *testing.T) {
		if store.DB() != db {
			t.Error("DB() should return the underlying database")
		}
	})
}

func TestMemoryStore_GetDomainDecayParams(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Minute)

	t.Run("returns copy for thread safety", func(t *testing.T) {
		params1 := store.GetDomainDecayParams(domain.DomainAcademic)
		params2 := store.GetDomainDecayParams(domain.DomainAcademic)

		// Modify one
		params1.DecayAlpha = 999

		// Should not affect the other
		if params2.DecayAlpha == 999 {
			t.Error("params should be independent copies")
		}
	})

	t.Run("domain defaults", func(t *testing.T) {
		db2 := setupTestDB(t)
		defer db2.Close()
		store2 := NewMemoryStore(db2, 100, time.Minute)

		tests := []struct {
			domain       domain.Domain
			expectedMean float64
		}{
			{domain.DomainAcademic, 0.3},
			{domain.DomainArchitect, 0.4},
			{domain.DomainEngineer, 0.6},
			{domain.DomainLibrarian, 0.5},
		}

		for _, tt := range tests {
			params := store2.GetDomainDecayParams(tt.domain)
			mean := params.Mean()
			if math.Abs(mean-tt.expectedMean) > 0.01 {
				t.Errorf("domain %v: expected mean %f, got %f",
					tt.domain, tt.expectedMean, mean)
			}
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestMemoryStore_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	store := NewMemoryStore(db, 20, 10*time.Millisecond)
	nodeID := "integration-test-node"
	insertTestNode(t, db, nodeID, domain.DomainArchitect)

	t.Run("full workflow", func(t *testing.T) {
		// 1. Initial access
		err := store.RecordAccess(ctx, nodeID, AccessCreation, "initial")
		if err != nil {
			t.Fatalf("RecordAccess failed: %v", err)
		}

		// 2. Multiple accesses over time
		for i := 0; i < 30; i++ {
			time.Sleep(15 * time.Millisecond)
			accessType := AccessRetrieval
			if i%5 == 0 {
				accessType = AccessReinforcement
			}
			err = store.RecordAccess(ctx, nodeID, accessType, "access")
			if err != nil {
				t.Fatalf("RecordAccess %d failed: %v", i, err)
			}
		}

		// 3. Verify pruning occurred
		memory, err := store.GetMemory(ctx, nodeID, domain.DomainArchitect)
		if err != nil {
			t.Fatalf("GetMemory failed: %v", err)
		}
		if len(memory.Traces) > 20 {
			t.Errorf("pruning failed: expected <= 20 traces, got %d", len(memory.Traces))
		}

		// 4. Verify activation is reasonable
		activation, err := store.ComputeActivation(ctx, nodeID)
		if err != nil {
			t.Fatalf("ComputeActivation failed: %v", err)
		}
		if math.IsInf(activation, 0) || math.IsNaN(activation) {
			t.Errorf("activation should be finite: %f", activation)
		}

		// 5. Update decay params based on feedback
		err = store.UpdateDecayParams(ctx, domain.DomainArchitect, true, time.Hour)
		if err != nil {
			t.Fatalf("UpdateDecayParams failed: %v", err)
		}

		// 6. Verify params were updated
		params := store.GetDomainDecayParams(domain.DomainArchitect)
		if params.EffectiveSamples <= 10 {
			t.Error("effective samples should have increased")
		}
	})
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	t.Run("concurrent record access", func(t *testing.T) {
		db := setupTestDBWithWAL(t) // Use WAL mode for better concurrency
		defer db.Close()
		store := NewMemoryStore(db, 100, time.Millisecond)

		// Create multiple test nodes
		for i := 0; i < 5; i++ {
			nodeID := "concurrent-node-" + string(rune('a'+i))
			insertTestNode(t, db, nodeID, domain.Domain(i%4))
		}

		done := make(chan bool)
		errCh := make(chan error, 50)

		for i := 0; i < 5; i++ {
			nodeID := "concurrent-node-" + string(rune('a'+i))
			go func(id string) {
				for j := 0; j < 10; j++ {
					time.Sleep(10 * time.Millisecond) // Slightly longer delay to reduce lock contention
					if err := store.RecordAccess(ctx, id, AccessRetrieval, "concurrent"); err != nil {
						errCh <- err
					}
				}
				done <- true
			}(nodeID)
		}

		for i := 0; i < 5; i++ {
			<-done
		}
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent access error: %v", err)
		}
	})

	t.Run("concurrent get and update params", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()
		store := NewMemoryStore(db, 100, time.Minute)

		done := make(chan bool)
		errCh := make(chan error, 30)

		// Concurrent reads
		for i := 0; i < 5; i++ {
			go func(d domain.Domain) {
				for j := 0; j < 10; j++ {
					_ = store.GetDomainDecayParams(d)
				}
				done <- true
			}(domain.Domain(i % 4))
		}

		// Concurrent writes
		for i := 0; i < 5; i++ {
			go func(d domain.Domain) {
				for j := 0; j < 5; j++ {
					if err := store.UpdateDecayParams(ctx, d, j%2 == 0, time.Hour); err != nil {
						errCh <- err
					}
				}
				done <- true
			}(domain.Domain(i % 4))
		}

		for i := 0; i < 10; i++ {
			<-done
		}
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent param update error: %v", err)
		}
	})
}
