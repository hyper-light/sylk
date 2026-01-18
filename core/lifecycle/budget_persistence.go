package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/resources"
)

// FileHandleBudgetProvider defines the interface for budget operations needed by persistence.
// This allows mocking the budget in tests.
type FileHandleBudgetProvider interface {
	// RegisterSession creates or retrieves a session budget
	RegisterSession(sessionID string) *resources.SessionFileBudget
	// GetSession returns the session budget for the given ID, or nil if not found
	GetSession(sessionID string) *resources.SessionFileBudget
	// RangeSessions iterates over all sessions, calling fn for each
	RangeSessions(fn func(sessionID string, session *resources.SessionFileBudget) bool)
}

// BudgetRow represents a persisted budget row
type BudgetRow struct {
	SessionID string
	Allocated int64
	Used      int64
	UpdatedAt string
}

// FileHandleBudgetPersistence saves/restores budget state to SQLite
type FileHandleBudgetPersistence struct {
	db     *sql.DB
	budget FileHandleBudgetProvider
	mu     sync.Mutex
}

// NewFileHandleBudgetPersistence creates a new persistence layer for file handle budgets.
// It ensures the required table exists in the database.
func NewFileHandleBudgetPersistence(db *sql.DB, budget FileHandleBudgetProvider) (*FileHandleBudgetPersistence, error) {
	if err := createBudgetTable(db); err != nil {
		return nil, err
	}

	return &FileHandleBudgetPersistence{
		db:     db,
		budget: budget,
	}, nil
}

func createBudgetTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS file_handle_budget (
			session_id TEXT PRIMARY KEY,
			allocated INTEGER NOT NULL,
			used INTEGER NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create budget table: %w", err)
	}
	return nil
}

// Save persists current budget state atomically
func (p *FileHandleBudgetPersistence) Save() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tx, err := p.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := p.clearAndInsert(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func (p *FileHandleBudgetPersistence) clearAndInsert(tx *sql.Tx) error {
	if _, err := tx.Exec("DELETE FROM file_handle_budget"); err != nil {
		return fmt.Errorf("clear budget: %w", err)
	}

	return p.insertAllSessions(tx)
}

func (p *FileHandleBudgetPersistence) insertAllSessions(tx *sql.Tx) error {
	stmt, err := tx.Prepare(`
		INSERT INTO file_handle_budget (session_id, allocated, used, updated_at)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	var insertErr error

	p.budget.RangeSessions(func(id string, sb *resources.SessionFileBudget) bool {
		_, err := stmt.Exec(id, sb.Allocated(), sb.Used(), now)
		if err != nil {
			insertErr = fmt.Errorf("insert budget: %w", err)
			return false
		}
		return true
	})

	return insertErr
}

// Restore loads budget state from database on startup
func (p *FileHandleBudgetPersistence) Restore() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	rows, err := p.queryAllRows()
	if err != nil {
		return err
	}
	defer rows.Close()

	return p.restoreFromRows(rows)
}

func (p *FileHandleBudgetPersistence) queryAllRows() (*sql.Rows, error) {
	rows, err := p.db.Query(`
		SELECT session_id, allocated, used, updated_at
		FROM file_handle_budget
	`)
	if err != nil {
		return nil, fmt.Errorf("query budget: %w", err)
	}
	return rows, nil
}

func (p *FileHandleBudgetPersistence) restoreFromRows(rows *sql.Rows) error {
	for rows.Next() {
		row, err := scanBudgetRow(rows)
		if err != nil {
			return fmt.Errorf("scan budget: %w", err)
		}
		p.restoreSession(row)
	}
	return rows.Err()
}

func scanBudgetRow(rows *sql.Rows) (BudgetRow, error) {
	var row BudgetRow
	err := rows.Scan(&row.SessionID, &row.Allocated, &row.Used, &row.UpdatedAt)
	return row, err
}

func (p *FileHandleBudgetPersistence) restoreSession(row BudgetRow) {
	session := p.budget.RegisterSession(row.SessionID)
	// Expand the session to match the persisted allocated amount
	session.TryExpand(row.Allocated)
}

// PeriodicSave saves budget state at regular intervals.
// It runs in the background and respects ctx.Done() for graceful shutdown.
// A final save is performed when the context is cancelled.
func (p *FileHandleBudgetPersistence) PeriodicSave(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = p.Save() // Best effort save
		case <-ctx.Done():
			_ = p.Save() // Final save on shutdown
			return
		}
	}
}

// LoadAllRows returns all persisted budget rows (useful for testing/debugging)
func (p *FileHandleBudgetPersistence) LoadAllRows() ([]BudgetRow, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rows, err := p.queryAllRows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectRows(rows)
}

func collectRows(rows *sql.Rows) ([]BudgetRow, error) {
	var result []BudgetRow
	for rows.Next() {
		row, err := scanBudgetRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan budget: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
