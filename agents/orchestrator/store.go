package orchestrator

import (
	_ "embed"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/database"
)

//go:embed schema.sql
var schemaSQL string

// Store provides SQLite persistence for orchestrator state.
// WAL-mode SQLite with connection pooling — mirrors core/vectorgraphdb/db.go.
type Store struct {
	db   *database.BunSQLiteDB
	path string
}

// StoreConfig configures the SQLite store.
type StoreConfig struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DefaultStoreConfig returns defaults for orchestrator DB.
// Lower than vectorgraphdb (10 vs 25) — less write concurrency.
func DefaultStoreConfig(dbPath string) StoreConfig {
	return StoreConfig{
		Path:            dbPath,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
	}
}

// OpenStore opens or creates the orchestrator SQLite database.
func OpenStore(cfg StoreConfig) (*Store, error) {
	db, err := database.OpenBunSQLite(database.BunSQLiteConfig{
		Path:        cfg.Path,
		MaxOpen:     cfg.MaxOpenConns,
		MaxIdle:     cfg.MaxIdleConns,
		MaxLifetime: cfg.ConnMaxLifetime,
		BusyTimeout: 5 * time.Second,
		EnableWAL:   true,
		ForeignKeys: true,
		CacheSize:   -2000,
		Synchronous: "normal",
	})
	if err != nil {
		return nil, fmt.Errorf("orchestrator store: open: %w", err)
	}

	return &Store{db: db, path: cfg.Path}, nil
}

// Migrate executes the embedded schema.sql.
func (s *Store) Migrate() error {
	if err := s.db.ExecSchema(nil, schemaSQL); err != nil {
		return fmt.Errorf("orchestrator store: migrate: %w", err)
	}
	return nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
