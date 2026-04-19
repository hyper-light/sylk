package ui

import (
	"context"
	"database/sql"
	"testing"

	"github.com/adalundhe/sylk/core/forest"
	_ "github.com/mattn/go-sqlite3"
)

// TestMemoryForest_SatisfiesMemoryViewService is the UI-01 runtime
// companion to the compile-time interface assertion. Constructs a
// live *forest.MemoryForest against an in-memory SQLite DB and
// confirms its Snapshot call returns a valid ViewSnapshot through
// the MemoryViewService surface. A regression that left the field
// unbound (the audit-claimed bug) would surface here as a nil
// pointer deref instead of in production's blank memory panel.
func TestMemoryForest_SatisfiesMemoryViewService(t *testing.T) {
	db, err := sql.Open("sqlite3", "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	// The forest schema depends on a `nodes` table (see
	// core/forest/service_test.go). We seed the minimum it expects
	// so New() succeeds; the Snapshot call works on an empty forest.
	if _, err := db.Exec(`CREATE TABLE nodes (
		id TEXT PRIMARY KEY,
		domain INTEGER NOT NULL,
		node_type INTEGER NOT NULL,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}

	live, err := forest.New(forest.Config{DB: db})
	if err != nil {
		t.Fatalf("forest.New: %v", err)
	}
	defer live.Close()

	// Assign through the interface-typed field — exactly the
	// production path in cmd/tui.go:1557.
	var svc MemoryViewService = live
	if svc == nil {
		t.Fatal("Forest binding failed — interface value nil")
	}

	snap, err := svc.Snapshot(context.Background(), forest.ViewSnapshotInput{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap == nil {
		t.Fatal("Snapshot returned nil — memory panel would render blank")
	}
}

// TestDeps_ForestFieldAcceptsMemoryForest locks the struct-field
// type contract: Deps.Forest must be assignable from a live
// *forest.MemoryForest. A regression that narrowed the field to a
// more restrictive interface would fail this test at build time.
func TestDeps_ForestFieldAcceptsMemoryForest(t *testing.T) {
	var deps Deps
	var f *forest.MemoryForest
	deps.Forest = f // compile-time assignment check; nil is fine
	_ = deps
}
