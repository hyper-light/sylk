package orchestrator

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store provides SQLite persistence for orchestrator state.
// WAL-mode SQLite with connection pooling — mirrors core/vectorgraphdb/db.go.
type Store struct {
	db   *sql.DB
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
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_synchronous=normal", cfg.Path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("orchestrator store: open: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("orchestrator store: ping: %w", err)
	}

	return &Store{db: db, path: cfg.Path}, nil
}

// Migrate executes the embedded schema.sql.
func (s *Store) Migrate() error {
	_, err := s.db.Exec(schemaSQL)
	if err != nil {
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
