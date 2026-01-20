// Package session provides session management for the Sylk application,
// including multi-session WAL (Write-Ahead Log) management for durability.
//
// # Multi-Session WAL Manager
//
// The MultiSessionWALManager coordinates WAL instances across multiple sessions,
// providing isolation and recovery capabilities for each session independently.
//
// ## Session Isolation
//
// Each session maintains its own WAL directory under the base directory:
//
//	{BaseDir}/{sessionID}/wal/
//
// This ensures that session data is isolated and can be recovered independently.
//
// ## Recovery
//
// On startup, the manager can recover all sessions that were not properly closed:
// 1. Query the metadata database for unrecovered sessions
// 2. Open each session's WAL
// 3. Mark sessions as recovered after successful initialization
//
// ## Metadata Storage
//
// Session metadata (creation time, last activity, recovery status) is stored
// in a shared SQLite database at {SharedDBDir}/wal_metadata.db.
package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

var (
	ErrWALManagerClosed  = errors.New("WAL manager is closed")
	ErrSessionWALExists  = errors.New("session WAL already exists")
	ErrSessionWALMissing = errors.New("session WAL not found")
)

type WALManagerConfig struct {
	BaseDir     string
	SharedDBDir string
	WALConfig   concurrency.WALConfig
}

func DefaultWALManagerConfig() WALManagerConfig {
	homeDir, _ := os.UserHomeDir()
	return WALManagerConfig{
		BaseDir:     filepath.Join(homeDir, ".sylk", "sessions"),
		SharedDBDir: filepath.Join(homeDir, ".sylk", "shared"),
		WALConfig:   concurrency.DefaultWALConfig(),
	}
}

type MultiSessionWALManager struct {
	config WALManagerConfig
	db     *sql.DB

	sessionWALs map[string]*concurrency.WriteAheadLog
	mu          sync.RWMutex
	closed      atomic.Bool
}

type SessionWALInfo struct {
	SessionID    string
	WALDir       string
	CreatedAt    time.Time
	LastActive   time.Time
	Recovered    bool
	SegmentCount int
}

func NewMultiSessionWALManager(cfg WALManagerConfig) (*MultiSessionWALManager, error) {
	cfg = normalizeWALManagerConfig(cfg)

	if err := os.MkdirAll(cfg.BaseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base dir: %w", err)
	}

	if err := os.MkdirAll(cfg.SharedDBDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create shared dir: %w", err)
	}

	db, err := openWALManagerDB(cfg.SharedDBDir)
	if err != nil {
		return nil, err
	}

	return &MultiSessionWALManager{
		config:      cfg,
		db:          db,
		sessionWALs: make(map[string]*concurrency.WriteAheadLog),
	}, nil
}

func normalizeWALManagerConfig(cfg WALManagerConfig) WALManagerConfig {
	if cfg.BaseDir == "" {
		homeDir, _ := os.UserHomeDir()
		cfg.BaseDir = filepath.Join(homeDir, ".sylk", "sessions")
	}
	if cfg.SharedDBDir == "" {
		homeDir, _ := os.UserHomeDir()
		cfg.SharedDBDir = filepath.Join(homeDir, ".sylk", "shared")
	}
	return cfg
}

func openWALManagerDB(sharedDir string) (*sql.DB, error) {
	dbPath := filepath.Join(sharedDir, "wal_metadata.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := createWALManagerSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func createWALManagerSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS session_wals (
			session_id TEXT PRIMARY KEY,
			wal_dir TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_active TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			recovered INTEGER DEFAULT 0
		)
	`)
	return err
}

func (m *MultiSessionWALManager) GetOrCreateWAL(sessionID string) (*concurrency.WriteAheadLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed.Load() {
		return nil, ErrWALManagerClosed
	}

	if wal, exists := m.sessionWALs[sessionID]; exists {
		return wal, nil
	}

	return m.createSessionWAL(sessionID)
}

func (m *MultiSessionWALManager) createSessionWAL(sessionID string) (*concurrency.WriteAheadLog, error) {
	walDir := m.getSessionWALDir(sessionID)

	if err := os.MkdirAll(walDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create WAL dir for session %s at %s: %w", sessionID, walDir, err)
	}

	if err := m.registerSession(sessionID, walDir); err != nil {
		return nil, fmt.Errorf("failed to register session %s: %w", sessionID, err)
	}

	wal, err := m.openWAL(walDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL for session %s at %s: %w", sessionID, walDir, err)
	}

	m.sessionWALs[sessionID] = wal
	return wal, nil
}

func (m *MultiSessionWALManager) getSessionWALDir(sessionID string) string {
	return filepath.Join(m.config.BaseDir, sessionID, "wal")
}

func (m *MultiSessionWALManager) registerSession(sessionID string, walDir string) error {
	now := time.Now()
	_, err := m.db.Exec(`
		INSERT INTO session_wals (session_id, wal_dir, created_at, last_active, recovered)
		VALUES (?, ?, ?, ?, 0)
		ON CONFLICT (session_id) DO UPDATE SET last_active = ?
	`, sessionID, walDir, now, now, now)
	if err != nil {
		return fmt.Errorf("failed to insert/update session_wals for session %s: %w", sessionID, err)
	}
	return nil
}

func (m *MultiSessionWALManager) openWAL(walDir string) (*concurrency.WriteAheadLog, error) {
	cfg := m.config.WALConfig
	cfg.Dir = walDir
	return concurrency.NewWriteAheadLog(cfg)
}

func (m *MultiSessionWALManager) GetWAL(sessionID string) (*concurrency.WriteAheadLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed.Load() {
		return nil, ErrWALManagerClosed
	}

	wal, exists := m.sessionWALs[sessionID]
	if !exists {
		return nil, ErrSessionWALMissing
	}

	return wal, nil
}

func (m *MultiSessionWALManager) RecoverAllSessions(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed.Load() {
		return ErrWALManagerClosed
	}

	sessions, err := m.getUnrecoveredSessions(ctx)
	if err != nil {
		return err
	}

	for _, info := range sessions {
		m.recoverSession(ctx, info)
	}

	return nil
}

func (m *MultiSessionWALManager) getUnrecoveredSessions(ctx context.Context) ([]SessionWALInfo, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT session_id, wal_dir, created_at, last_active
		FROM session_wals
		WHERE recovered = 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return m.scanSessionInfoRows(rows)
}

// scanSessionInfoRows scans rows from unrecovered sessions query.
// W3L.10: Collect and return scan errors instead of silently ignoring them.
func (m *MultiSessionWALManager) scanSessionInfoRows(rows *sql.Rows) ([]SessionWALInfo, error) {
	// W3L.10: Pre-allocate slice with reasonable initial capacity
	sessions := make([]SessionWALInfo, 0, 16)
	var scanErrors []error

	for rows.Next() {
		var info SessionWALInfo
		err := rows.Scan(&info.SessionID, &info.WALDir, &info.CreatedAt, &info.LastActive)
		if err != nil {
			// W3L.10: Collect scan errors for debugging instead of silently ignoring
			scanErrors = append(scanErrors, fmt.Errorf("scan session info: %w", err))
			continue
		}
		sessions = append(sessions, info)
	}

	// Return rows.Err() which has priority, but log scan errors if any occurred
	if rowsErr := rows.Err(); rowsErr != nil {
		return sessions, rowsErr
	}

	// If we had scan errors but rows.Err() is nil, return first scan error
	if len(scanErrors) > 0 {
		return sessions, fmt.Errorf("partial scan failure (%d errors): %w", len(scanErrors), scanErrors[0])
	}

	return sessions, nil
}

func (m *MultiSessionWALManager) recoverSession(ctx context.Context, info SessionWALInfo) {
	_ = ctx
	_, err := m.createSessionWAL(info.SessionID)
	if err != nil {
		return
	}

	m.markSessionRecovered(info.SessionID)
}

func (m *MultiSessionWALManager) markSessionRecovered(sessionID string) {
	m.db.Exec(`UPDATE session_wals SET recovered = 1 WHERE session_id = ?`, sessionID)
}

func (m *MultiSessionWALManager) UpdateActivity(sessionID string) error {
	if m.closed.Load() {
		return ErrWALManagerClosed
	}

	_, err := m.db.Exec(`
		UPDATE session_wals SET last_active = ? WHERE session_id = ?
	`, time.Now(), sessionID)
	if err != nil && m.closed.Load() {
		return ErrWALManagerClosed
	}
	return err
}

func (m *MultiSessionWALManager) GetSessionInfo(sessionID string) (*SessionWALInfo, error) {
	if m.closed.Load() {
		return nil, ErrWALManagerClosed
	}

	var info SessionWALInfo
	err := m.db.QueryRow(`
		SELECT session_id, wal_dir, created_at, last_active, recovered
		FROM session_wals
		WHERE session_id = ?
	`, sessionID).Scan(&info.SessionID, &info.WALDir, &info.CreatedAt, &info.LastActive, &info.Recovered)

	if err == sql.ErrNoRows {
		return nil, ErrSessionWALMissing
	}
	if err != nil {
		if m.closed.Load() {
			return nil, ErrWALManagerClosed
		}
		return nil, err
	}

	return &info, nil
}

func (m *MultiSessionWALManager) ListSessions(ctx context.Context) ([]SessionWALInfo, error) {
	if m.closed.Load() {
		return nil, ErrWALManagerClosed
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT session_id, wal_dir, created_at, last_active, recovered
		FROM session_wals
		ORDER BY last_active DESC
	`)
	if err != nil {
		if m.closed.Load() {
			return nil, ErrWALManagerClosed
		}
		return nil, err
	}
	defer rows.Close()

	return m.scanFullSessionInfoRows(rows)
}

func (m *MultiSessionWALManager) scanFullSessionInfoRows(rows *sql.Rows) ([]SessionWALInfo, error) {
	var sessions []SessionWALInfo

	for rows.Next() {
		var info SessionWALInfo
		err := rows.Scan(&info.SessionID, &info.WALDir, &info.CreatedAt, &info.LastActive, &info.Recovered)
		if err != nil {
			continue
		}
		sessions = append(sessions, info)
	}

	return sessions, rows.Err()
}

func (m *MultiSessionWALManager) RemoveSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed.Load() {
		return ErrWALManagerClosed
	}

	if wal, exists := m.sessionWALs[sessionID]; exists {
		wal.Close()
		delete(m.sessionWALs, sessionID)
	}

	_, err := m.db.Exec(`DELETE FROM session_wals WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete session %s from metadata: %w", sessionID, err)
	}

	sessionDir := filepath.Join(m.config.BaseDir, sessionID)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("failed to remove session directory %s: %w", sessionDir, err)
	}
	return nil
}

func (m *MultiSessionWALManager) ActiveSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessionWALs)
}

func (m *MultiSessionWALManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed.Load() {
		return ErrWALManagerClosed
	}
	m.closed.Store(true)

	for _, wal := range m.sessionWALs {
		wal.Close()
	}
	m.sessionWALs = nil

	return m.db.Close()
}
