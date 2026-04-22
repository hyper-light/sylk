package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/activity/activitystore"
	"github.com/adalundhe/sylk/core/database"
)

//go:embed schema.sql
var schemaSQL string

// Store provides SQLite persistence for orchestrator state.
// WAL-mode SQLite with connection pooling — mirrors core/vectorgraphdb/db.go.
type Store struct {
	db   *database.BunSQLiteDB
	path string

	// holdNotify carries one channel per session that closes whenever
	// execution_holds is written for that session. The dispatch gate
	// (validation_gate.go) reads it via HoldsNotifyChannel to unblock
	// waiters — no polling, no drift. Every hold write path
	// (CreateExecutionHold, UpdateExecutionHold, SupersedePriorPlans)
	// calls notifyHoldsChanged which closes the current channel and
	// mints a fresh one.
	holdNotifyMu sync.Mutex
	holdNotify   map[string]chan struct{}
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

// OpenStore opens or creates the orchestrator SQLite database. Pragma and
// retry policy come from database.DefaultBunSQLiteConfig — only pool sizing
// is overridden here. Without an explicit WriteRetryMax the orchestrator
// previously got zero retries on SQLITE_BUSY, which (combined with the
// historical DSN bug that dropped busy_timeout entirely) produced
// sub-millisecond "database is locked" failures under coord_* bursts.
func OpenStore(cfg StoreConfig) (*Store, error) {
	bunCfg := database.DefaultBunSQLiteConfig(cfg.Path)
	if cfg.MaxOpenConns > 0 {
		bunCfg.MaxOpen = cfg.MaxOpenConns
	}
	if cfg.MaxIdleConns > 0 {
		bunCfg.MaxIdle = cfg.MaxIdleConns
	}
	if cfg.ConnMaxLifetime > 0 {
		bunCfg.MaxLifetime = cfg.ConnMaxLifetime
	}

	db, err := database.OpenBunSQLite(bunCfg)
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
	if err := s.ensureDAGExecutionsSchema(); err != nil {
		return fmt.Errorf("orchestrator store: migrate dag executions: %w", err)
	}
	if err := s.ensurePlanHandoffReceiptSchema(); err != nil {
		return fmt.Errorf("orchestrator store: migrate handoff receipts: %w", err)
	}
	if err := s.ensureExecutionHoldSchema(); err != nil {
		return fmt.Errorf("orchestrator store: migrate execution holds: %w", err)
	}
	// Activity Fabric: every sovereign store on this BunSQLite handle
	// emits projections into agent_activity. The schema lives here so
	// it inherits the WAL + busy_timeout + writeMu policy.
	store := activitystore.NewSQLiteStore(s.db)
	if err := store.EnsureSchema(context.Background()); err != nil {
		return fmt.Errorf("orchestrator store: migrate agent_activity: %w", err)
	}
	return nil
}

// ActivityStore returns a fabric-aware SQLiteStore wrapping this
// orchestrator's BunSQLite handle. The TUI wiring uses this to install
// the dual-tier sink as the process-wide DefaultSink at startup.
func (s *Store) ActivityStore() *activitystore.SQLiteStore {
	if s == nil {
		return nil
	}
	return activitystore.NewSQLiteStore(s.db)
}

func (s *Store) ensurePlanHandoffReceiptSchema() error {
	if err := s.ensurePlanHandoffReceiptColumn("requester_agent_id", "TEXT"); err != nil {
		return err
	}
	// plan_id predates requester_agent_id in the schema, but legacy
	// databases created before the column migration still lack it. Guard
	// the index creation behind an explicit column-existence check so
	// the plan-scoped index only lands once plan_id is present.
	if err := s.ensurePlanHandoffReceiptColumn("plan_id", "TEXT"); err != nil {
		return err
	}
	if s == nil || s.db == nil || s.db.SQLDB() == nil {
		return nil
	}
	if _, err := s.db.SQLDB().Exec(
		`CREATE INDEX IF NOT EXISTS idx_handoff_receipts_plan ON plan_handoff_receipts(session_id, plan_id, revision)`,
	); err != nil {
		return fmt.Errorf("create plan_handoff_receipts plan index: %w", err)
	}
	return nil
}

// ensureDAGExecutionsSchema adds the plan_id column to legacy
// dag_executions tables and creates the plan-scoped index. Placing the
// index in schema.sql fails on databases that predate the column.
func (s *Store) ensureDAGExecutionsSchema() error {
	if s == nil || s.db == nil || s.db.SQLDB() == nil {
		return nil
	}
	if err := s.ensureDAGExecutionsColumn("plan_id", "TEXT"); err != nil {
		return err
	}
	if _, err := s.db.SQLDB().Exec(
		`CREATE INDEX IF NOT EXISTS idx_dag_exec_plan ON dag_executions(plan_id)`,
	); err != nil {
		return fmt.Errorf("create dag_executions plan index: %w", err)
	}
	return nil
}

func (s *Store) ensureDAGExecutionsColumn(name, definition string) error {
	exists, err := s.dagExecutionsColumnExists(name)
	if err != nil || exists {
		return err
	}
	_, err = s.db.SQLDB().Exec(
		fmt.Sprintf("ALTER TABLE dag_executions ADD COLUMN %s %s", name, definition),
	)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	return nil
}

func (s *Store) dagExecutionsColumnExists(name string) (bool, error) {
	rows, err := s.db.SQLDB().Query("PRAGMA table_info(dag_executions)")
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

// ensureExecutionHoldSchema adds the plan_id / blocks_dag_ids /
// exempt_dag_ids columns to existing execution_holds tables, and
// supersedes any pre-existing active rows because they lack the plan
// scope required by the new dispatch gate. Superseding is the safe
// default: a legacy row's remediation context is lost (no plan_id),
// so its blocking effect can't be accurately applied. Users whose
// prior session was genuinely mid-remediation see those prior holds
// in the audit trail but no longer block fresh plan submissions.
// See docs/EXECUTION_HOLDS.md.
func (s *Store) ensureExecutionHoldSchema() error {
	if s == nil || s.db == nil || s.db.SQLDB() == nil {
		return nil
	}
	for _, col := range []struct {
		name       string
		definition string
	}{
		{"plan_id", "TEXT"},
		{"blocks_dag_ids", "TEXT"},
		{"exempt_dag_ids", "TEXT"},
	} {
		if err := s.ensureExecutionHoldColumn(col.name, col.definition); err != nil {
			return err
		}
	}
	// Supersede any active holds carried over from the pre-plan-scope
	// era. New holds created after this migration will carry a real
	// plan_id; bypassing these legacy rows now eliminates the exact
	// bug class the redesign exists to prevent (stale holds blocking
	// fresh runs).
	if _, err := s.db.SQLDB().Exec(
		`UPDATE execution_holds
		    SET status = ?, released_at = CURRENT_TIMESTAMP,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE status = ? AND (plan_id IS NULL OR plan_id = '')`,
		string(ExecutionHoldStatusSuperseded),
		string(ExecutionHoldStatusActive),
	); err != nil {
		return fmt.Errorf("supersede legacy execution holds: %w", err)
	}
	// Create the plan-scoped index AFTER the column exists. This was
	// previously in schema.sql but failed on legacy tables where
	// CREATE TABLE IF NOT EXISTS was a no-op and plan_id did not exist.
	if _, err := s.db.SQLDB().Exec(
		`CREATE INDEX IF NOT EXISTS idx_execution_holds_plan ON execution_holds(session_id, plan_id, status)`,
	); err != nil {
		return fmt.Errorf("create execution_holds plan index: %w", err)
	}
	return nil
}

func (s *Store) ensureExecutionHoldColumn(name, definition string) error {
	exists, err := s.executionHoldColumnExists(name)
	if err != nil || exists {
		return err
	}
	_, err = s.db.SQLDB().Exec(
		fmt.Sprintf("ALTER TABLE execution_holds ADD COLUMN %s %s", name, definition),
	)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	return nil
}

func (s *Store) executionHoldColumnExists(name string) (bool, error) {
	rows, err := s.db.SQLDB().Query("PRAGMA table_info(execution_holds)")
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
