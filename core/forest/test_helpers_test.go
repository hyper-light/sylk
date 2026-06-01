package forest

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestForest(t testing.TB) (*MemoryForest, *sql.DB) {
	return newTestForestWithConfig(t, Config{})
}

func newTestForestWithConfig(t testing.TB, cfg Config) (*MemoryForest, *sql.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", stableID("forest-test", t.Name()))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		);
	`); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}
	cfg.DB = db
	cfg.SynchronousProjection = true
	forest, err := New(cfg)
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	return forest, db
}

func newAsyncTestForestWithConfig(t *testing.T, cfg Config) (*MemoryForest, *sql.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", stableID("forest-test-async", t.Name()))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL,
			name TEXT NOT NULL
		);
	`); err != nil {
		t.Fatalf("seed nodes table: %v", err)
	}
	cfg.DB = db
	forest, err := New(cfg)
	if err != nil {
		t.Fatalf("new forest: %v", err)
	}
	t.Cleanup(func() { _ = forest.Close() })
	return forest, db
}

func waitForForestCondition(t *testing.T, timeout time.Duration, check func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ok, err := check()
		if err != nil {
			if strings.Contains(err.Error(), "database table is locked") {
				err = nil
			} else {
				t.Fatalf("wait condition: %v", err)
			}
		}
		if err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not met before timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
