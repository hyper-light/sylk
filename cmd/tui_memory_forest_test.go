package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/session"
)

func TestBuildMemoryForest_BootstrapsSessionScopedSchema(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()

	forest, contentStore, vectorDB, err := buildMemoryForest(projectRoot, nil)
	if err != nil {
		t.Fatalf("buildMemoryForest() error = %v", err)
	}
	defer forest.Close()
	defer contentStore.Close()
	defer vectorDB.Close()

	basePath := filepath.Join(projectRoot, ".sylk", "sessions", "default", "state", "memory_forest")
	if _, err := os.Stat(filepath.Join(basePath, "content.sqlite")); err != nil {
		t.Fatalf("memory forest db missing at session path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".sylk", "cache", "memory_forest")); !os.IsNotExist(err) {
		t.Fatalf("legacy cache memory forest path should not exist, stat err = %v", err)
	}

	db := vectorDB.DB()
	assertSQLiteTableExists(t, db, "nodes")
	assertSQLiteTableExists(t, db, "forest_events")
	assertSQLiteTableExists(t, db, "content_entries")
	assertSQLiteTableExists(t, db, "node_access_traces")
	assertSQLiteTableExists(t, db, "decay_parameters")

	assertSQLiteColumnExists(t, db, "nodes", "memory_activation")
	assertSQLiteColumnExists(t, db, "nodes", "last_accessed_at")
	assertSQLiteColumnExists(t, db, "nodes", "access_count")
	assertSQLiteColumnExists(t, db, "nodes", "base_offset")
}

func TestBuildMemoryForest_ReopensSessionScopedSchema(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()

	forest, contentStore, vectorDB, err := buildMemoryForest(projectRoot, nil)
	if err != nil {
		t.Fatalf("first buildMemoryForest() error = %v", err)
	}
	if err := forest.Close(); err != nil {
		t.Fatalf("first forest.Close() error = %v", err)
	}
	if err := contentStore.Close(); err != nil {
		t.Fatalf("first contentStore.Close() error = %v", err)
	}
	if err := vectorDB.Close(); err != nil {
		t.Fatalf("first vectorDB.Close() error = %v", err)
	}

	reopenedForest, reopenedContentStore, reopenedVectorDB, err := buildMemoryForest(projectRoot, nil)
	if err != nil {
		t.Fatalf("reopen buildMemoryForest() error = %v", err)
	}
	defer reopenedForest.Close()
	defer reopenedContentStore.Close()
	defer reopenedVectorDB.Close()

	assertSQLiteTableExists(t, reopenedVectorDB.DB(), "nodes")
	assertSQLiteTableExists(t, reopenedVectorDB.DB(), "forest_events")
}

func TestBuildMemoryForestForSession_UsesRequestedSessionPath(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	sessionID := "run-session-123"

	forest, contentStore, vectorDB, err := buildMemoryForestForSession(projectRoot, sessionID, nil)
	if err != nil {
		t.Fatalf("buildMemoryForestForSession() error = %v", err)
	}
	defer forest.Close()
	defer contentStore.Close()
	defer vectorDB.Close()

	basePath := filepath.Join(projectRoot, ".sylk", "sessions", sessionID, "state", "memory_forest")
	if _, err := os.Stat(filepath.Join(basePath, "content.sqlite")); err != nil {
		t.Fatalf("memory forest db missing at requested session path: %v", err)
	}
}

func TestTUIRunSessionConfig_MintsIndependentRuntimeSession(t *testing.T) {
	t.Parallel()

	cfg := tuiRunSessionConfig()
	if cfg.ID != "" {
		t.Fatalf("tuiRunSessionConfig().ID = %q, want empty for generated ID", cfg.ID)
	}
	if cfg.Name != "default" {
		t.Fatalf("tuiRunSessionConfig().Name = %q, want default", cfg.Name)
	}

	first := session.NewSession(cfg)
	second := session.NewSession(cfg)
	if first.ID() == "default" || second.ID() == "default" {
		t.Fatalf("TUI run sessions must not reuse storage default ID: first=%q second=%q", first.ID(), second.ID())
	}
	if first.ID() == second.ID() {
		t.Fatalf("TUI run sessions should mint independent IDs, both got %q", first.ID())
	}
}

func TestOnDemandDepsDefaultSessionID_UsesRunSessionFallback(t *testing.T) {
	t.Parallel()

	deps := onDemandAgentCreatorDeps{fallbackSessionID: "run-session-123"}
	if got := deps.defaultSessionID(); got != "run-session-123" {
		t.Fatalf("defaultSessionID() = %q, want run-session-123", got)
	}
}

func assertSQLiteTableExists(t *testing.T, db *sql.DB, table string) {
	t.Helper()

	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&name)
	if err != nil {
		t.Fatalf("expected table %q to exist: %v", table, err)
	}
}

func assertSQLiteColumnExists(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	t.Fatalf("expected column %q on table %q", column, table)
}
