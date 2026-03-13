package orchestrator

import (
	_ "embed"
	"fmt"
	"strings"
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
	if err := s.ensurePlanHandoffReceiptSchema(); err != nil {
		return fmt.Errorf("orchestrator store: migrate handoff receipts: %w", err)
	}
	return nil
}

func (s *Store) ensurePlanHandoffReceiptSchema() error {
	return s.ensurePlanHandoffReceiptColumn("requester_agent_id", "TEXT")
}

func (s *Store) ensurePlanHandoffReceiptColumn(name, definition string) error {
	if s == nil || s.db == nil || s.db.SQLDB() == nil {
		return nil
	}
	exists, err := s.planHandoffReceiptColumnExists(name)
	if err != nil || exists {
		return err
	}
	_, err = s.db.SQLDB().Exec(
		fmt.Sprintf("ALTER TABLE plan_handoff_receipts ADD COLUMN %s %s", name, definition),
	)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	return nil
}

func (s *Store) planHandoffReceiptColumnExists(name string) (bool, error) {
	rows, err := s.db.SQLDB().Query("PRAGMA table_info(plan_handoff_receipts)")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var (
		cid        int
		column     string
		kind       string
		notNull    int
		defaultV   any
		primaryKey int
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &column, &kind, &notNull, &defaultV, &primaryKey); err != nil {
			return false, err
		}
		if strings.EqualFold(strings.TrimSpace(column), strings.TrimSpace(name)) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Close closes the database connection.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
