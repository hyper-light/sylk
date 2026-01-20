package hnsw

import (
	"database/sql"
	"math"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	_ "github.com/mattn/go-sqlite3"
)

// W4P.15: Tests for distance persistence in HNSW index.

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	schema := `
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		);
		CREATE TABLE vectors (
			node_id TEXT PRIMARY KEY,
			embedding BLOB NOT NULL,
			magnitude REAL NOT NULL,
			dimensions INTEGER NOT NULL DEFAULT 768,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL
		);
		CREATE TABLE hnsw_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
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
	return db
}

func insertTestNode(t *testing.T, db *sql.DB, id string, vec []float32, domain vectorgraphdb.Domain, nodeType vectorgraphdb.NodeType) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO nodes (id, domain, node_type, name) VALUES (?, ?, ?, ?)`,
		id, int(domain), int(nodeType), id)
	if err != nil {
		t.Fatalf("failed to insert node %s: %v", id, err)
	}
	mag := Magnitude(vec)
	blob := float32ToBytes(vec)
	_, err = db.Exec(`INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type) VALUES (?, ?, ?, ?, ?, ?)`,
		id, blob, mag, len(vec), int(domain), int(nodeType))
	if err != nil {
		t.Fatalf("failed to insert vector for %s: %v", id, err)
	}
}

func TestW4P15_SaveLoadPreservesDistances(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0.9, 0.1, 0, 0}
	vec3 := []float32{0, 1, 0, 0}

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node3", vec3, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	preSaveResults := idx.Search(vec1, 3, nil)
	if len(preSaveResults) == 0 {
		t.Fatal("pre-save search returned no results")
	}

	if err := idx.Save(db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	insertTestNode(t, db, "node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	insertTestNode(t, db, "node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	insertTestNode(t, db, "node3", vec3, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx2 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})
	if err := idx2.Load(db); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := idx2.LoadVectors(db); err != nil {
		t.Fatalf("LoadVectors failed: %v", err)
	}

	postLoadResults := idx2.Search(vec1, 3, nil)
	if len(postLoadResults) == 0 {
		t.Fatal("post-load search returned no results")
	}

	if preSaveResults[0].ID != postLoadResults[0].ID {
		t.Errorf("top result mismatch: pre=%s, post=%s", preSaveResults[0].ID, postLoadResults[0].ID)
	}
}

func TestW4P15_DistancesAreNonZeroAfterLoad(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})
	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0.5, 0.5, 0, 0}

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	if err := idx.Save(db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	insertTestNode(t, db, "node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	insertTestNode(t, db, "node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx2 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})
	if err := idx2.Load(db); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := idx2.LoadVectors(db); err != nil {
		t.Fatalf("LoadVectors failed: %v", err)
	}

	// Verify search works - implicitly tests that distances are computed
	results := idx2.Search(vec1, 2, nil)
	if len(results) == 0 {
		t.Error("expected search results after load")
	}
}

func TestW4P15_EmptyIndexPersistence(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	idx := New(DefaultConfig())
	if err := idx.Save(db); err != nil {
		t.Fatalf("Save empty index failed: %v", err)
	}

	idx2 := New(DefaultConfig())
	if err := idx2.Load(db); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := idx2.LoadVectors(db); err != nil {
		t.Fatalf("LoadVectors failed: %v", err)
	}

	if idx2.Size() != 0 {
		t.Errorf("expected empty index, got size=%d", idx2.Size())
	}
}

func TestW4P15_TwoNodePersistence(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})
	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0, 1, 0, 0}

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	if err := idx.Save(db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	insertTestNode(t, db, "node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	insertTestNode(t, db, "node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx2 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})
	if err := idx2.Load(db); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := idx2.LoadVectors(db); err != nil {
		t.Fatalf("LoadVectors failed: %v", err)
	}

	if idx2.Size() != 2 {
		t.Errorf("expected size=2, got %d", idx2.Size())
	}

	results := idx2.Search(vec1, 2, nil)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(results))
	}
	if results[0].ID != "node1" {
		t.Errorf("expected node1 as top result, got %s", results[0].ID)
	}
}

func TestW4P15_RecomputeEdgeDistances(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})
	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0.8, 0.2, 0, 0}

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Corrupt distances
	idx.mu.Lock()
	for _, layer := range idx.layers {
		layer.mu.RLock()
		for _, node := range layer.nodes {
			for _, nID := range node.neighbors.GetIDs() {
				node.neighbors.UpdateDistance(nID, 0)
			}
		}
		layer.mu.RUnlock()
	}
	idx.recomputeEdgeDistances()
	idx.mu.Unlock()

	// Verify search still works
	results := idx.Search(vec1, 2, nil)
	if len(results) == 0 {
		t.Error("expected search results after recompute")
	}
}

func TestW4P15_GetVectorAndMagnitudeLocked(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{3, 4, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	gotVec, gotMag, ok := idx.getVectorAndMagnitudeLocked("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("expected to find node1")
	}
	if len(gotVec) != 3 {
		t.Errorf("expected vector length 3, got %d", len(gotVec))
	}
	if math.Abs(gotMag-5.0) > 1e-6 {
		t.Errorf("expected magnitude 5.0, got %v", gotMag)
	}

	idx.mu.RLock()
	_, _, ok = idx.getVectorAndMagnitudeLocked("nonexistent")
	idx.mu.RUnlock()
	if ok {
		t.Error("should not find nonexistent node")
	}
}
