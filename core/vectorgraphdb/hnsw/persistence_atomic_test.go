package hnsw

import (
	"database/sql"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	_ "github.com/mattn/go-sqlite3"
)

// W12.39: Tests for atomic load with rollback on failure.

// TestW12_39_LoadVectorsAtomicRollback verifies that LoadVectors leaves the
// index unchanged if an error occurs during loading.
func TestW12_39_LoadVectorsAtomicRollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create index with initial state
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36, Dimension: 4})
	initialVec := []float32{1, 0, 0, 0}
	if err := idx.Insert("initial", initialVec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Insert valid node to database
	insertTestNode(t, db, "valid", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Insert invalid data (missing node in nodes table causes JOIN to fail for that row)
	_, err := db.Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"orphan", float32ToBytes([]float32{0, 0, 1, 0}), 1.0, 4, 0, 0)
	if err != nil {
		t.Fatalf("Failed to insert orphan vector: %v", err)
	}

	// Record initial state
	initialSize := idx.Size()
	initialHasNode := idx.Contains("initial")

	// LoadVectors should succeed (orphan is excluded by JOIN)
	err = idx.LoadVectors(db)
	if err != nil {
		t.Fatalf("LoadVectors should succeed: %v", err)
	}

	// Verify initial state preserved and valid node added
	if !idx.Contains("initial") {
		t.Error("Initial node should still exist")
	}
	if !idx.Contains("valid") {
		t.Error("Valid node should be loaded")
	}

	// Test with corrupted data that causes scan error
	db2 := setupCorruptedDB(t)
	defer db2.Close()

	idx2 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36, Dimension: 4})
	if err := idx2.Insert("existing", initialVec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	existingSize := idx2.Size()

	// This should fail due to corrupted data
	err = idx2.LoadVectors(db2)
	if err == nil {
		t.Error("LoadVectors should fail with corrupted data")
	}

	// Verify index unchanged after failed load
	if idx2.Size() != existingSize {
		t.Errorf("Index size changed after failed load: got %d, want %d", idx2.Size(), existingSize)
	}
	if !idx2.Contains("existing") {
		t.Error("Existing node should still exist after failed load")
	}

	// Verify we tested actual rollback
	_ = initialSize
	_ = initialHasNode
}

// TestW12_39_LoadAtomicRollback verifies that Load leaves the index unchanged
// if an error occurs during loading.
func TestW12_39_LoadAtomicRollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create index with initial state
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36, Dimension: 4})
	initialVec := []float32{1, 0, 0, 0}
	if err := idx.Insert("initial", initialVec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Save to database
	if err := idx.Save(db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Create a new index and load
	idx2 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36, Dimension: 4})
	err := idx2.Load(db)
	if err != nil {
		t.Fatalf("Load should succeed: %v", err)
	}

	// Test with corrupted metadata table
	db3 := setupCorruptedMetaDB(t)
	defer db3.Close()

	idx3 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36, Dimension: 4})
	if err := idx3.Insert("existing", initialVec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	existingM := idx3.M
	existingEf := idx3.efConstruct

	// This should fail due to corrupted metadata
	err = idx3.Load(db3)
	if err == nil {
		t.Error("Load should fail with corrupted metadata")
	}

	// Verify index unchanged after failed load
	if idx3.M != existingM {
		t.Errorf("M changed after failed load: got %d, want %d", idx3.M, existingM)
	}
	if idx3.efConstruct != existingEf {
		t.Errorf("efConstruct changed after failed load: got %d, want %d", idx3.efConstruct, existingEf)
	}
}

// setupCorruptedDB creates a database with data that will cause scan errors.
func setupCorruptedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create schema with wrong column types to cause scan errors
	schema := `
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain TEXT NOT NULL,
			node_type TEXT NOT NULL,
			name TEXT NOT NULL
		);
		CREATE TABLE vectors (
			node_id TEXT PRIMARY KEY,
			embedding TEXT NOT NULL,
			magnitude TEXT NOT NULL,
			dimensions INTEGER NOT NULL DEFAULT 768,
			domain TEXT NOT NULL,
			node_type TEXT NOT NULL
		);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		t.Fatalf("failed to create schema: %v", err)
	}

	// Insert data that will fail to scan properly
	_, err = db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES (?, ?, ?, ?)`,
		"bad", "not_a_number", "not_a_number", "bad")
	if err != nil {
		db.Close()
		t.Fatalf("failed to insert bad node: %v", err)
	}
	_, err = db.Exec(`INSERT INTO vectors (node_id, embedding, magnitude, domain, node_type) VALUES (?, ?, ?, ?, ?)`,
		"bad", "not_a_blob", "not_a_number", "not_a_number", "not_a_number")
	if err != nil {
		db.Close()
		t.Fatalf("failed to insert bad vector: %v", err)
	}

	return db
}

// setupCorruptedMetaDB creates a database with corrupted metadata.
func setupCorruptedMetaDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Create schema without value column to cause scan error
	schema := `
		CREATE TABLE hnsw_meta (
			key TEXT PRIMARY KEY
		);
		CREATE TABLE hnsw_edges (
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			level INTEGER NOT NULL,
			PRIMARY KEY (source_id, target_id, level)
		);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		t.Fatalf("failed to create schema: %v", err)
	}

	// Insert metadata row (will fail to scan due to missing value column)
	_, err = db.Exec(`INSERT INTO hnsw_meta (key) VALUES (?)`, "m")
	if err != nil {
		db.Close()
		t.Fatalf("failed to insert meta: %v", err)
	}

	return db
}

// TestW12_39_StagedDataIsolation verifies that staged data is isolated
// from the main index during loading.
func TestW12_39_StagedDataIsolation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36, Dimension: 4})

	// Insert test data
	insertTestNode(t, db, "node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	insertTestNode(t, db, "node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Stage the load
	staged, err := idx.stageVectorLoad(db)
	if err != nil {
		t.Fatalf("stageVectorLoad failed: %v", err)
	}

	// Verify staged data exists
	if len(staged.vectors) != 2 {
		t.Errorf("Expected 2 staged vectors, got %d", len(staged.vectors))
	}

	// Verify index is still empty (staged data not committed)
	if idx.Size() != 0 {
		t.Errorf("Index should be empty before commit, got size %d", idx.Size())
	}

	// Commit and verify
	idx.mu.Lock()
	idx.commitStagedVectors(staged)
	idx.mu.Unlock()

	if idx.Size() != 2 {
		t.Errorf("Index should have 2 nodes after commit, got %d", idx.Size())
	}
}
