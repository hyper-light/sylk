package session

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrRegistrySessionNotFound indicates the session was not found in registry
	ErrRegistrySessionNotFound = errors.New("session not found in registry")

	// ErrRegistrySessionExists indicates a session already exists in registry
	ErrRegistrySessionExists = errors.New("session already exists in registry")

	// ErrRegistryClosed indicates the registry is closed
	ErrRegistryClosed = errors.New("session registry is closed")

	// ErrProcessNotRunning indicates the process is no longer running
	ErrProcessNotRunning = errors.New("process is not running")
)

// =============================================================================
// Configuration
// =============================================================================

const (
	DefaultRegistryDBPath       = ".sylk/state.db"
	DefaultHeartbeatInterval    = 1 * time.Second
	DefaultStaleThreshold       = 10 * time.Second
	DefaultActivityDecayRate    = 0.9
	DefaultCleanupInterval      = 30 * time.Second
	DefaultRegistryMaxOpenConns = 1
)

// RegistryConfig holds configuration for the session registry
type RegistryConfig struct {
	// DBPath is the path to the SQLite database
	DBPath string

	// HeartbeatInterval is how often sessions should send heartbeats
	HeartbeatInterval time.Duration

	// StaleThreshold is how long before a session is considered stale
	StaleThreshold time.Duration

	// ActivityDecayRate is the decay rate for activity score calculation
	ActivityDecayRate float64

	// CleanupInterval is how often to run cleanup of stale sessions
	CleanupInterval time.Duration
}

// DefaultRegistryConfig returns a RegistryConfig with sensible defaults
func DefaultRegistryConfig() RegistryConfig {
	return RegistryConfig{
		DBPath:            DefaultRegistryDBPath,
		HeartbeatInterval: DefaultHeartbeatInterval,
		StaleThreshold:    DefaultStaleThreshold,
		ActivityDecayRate: DefaultActivityDecayRate,
		CleanupInterval:   DefaultCleanupInterval,
	}
}

// =============================================================================
// Session Record
// =============================================================================

// SessionRecord represents a session in the registry
type SessionRecord struct {
	SessionID        string    `json:"session_id"`
	PID              int       `json:"pid"`
	StartTime        time.Time `json:"start_time"`
	LastHeartbeat    time.Time `json:"last_heartbeat"`
	ActivityScore    float64   `json:"activity_score"`
	RunningPipelines int       `json:"running_pipelines"`
	AllocatedSlots   int       `json:"allocated_slots"`
}

// IsStale returns true if the session hasn't sent a heartbeat within threshold
func (r *SessionRecord) IsStale(threshold time.Duration) bool {
	return time.Since(r.LastHeartbeat) > threshold
}

// =============================================================================
// Session Registry
// =============================================================================

// Registry manages cross-session coordination via SQLite
type Registry struct {
	db     *sql.DB
	config RegistryConfig

	mu     sync.RWMutex
	closed bool

	stopCleanup chan struct{}
	cleanupDone chan struct{}
}

// NewRegistry creates a new session registry
func NewRegistry(cfg RegistryConfig) (*Registry, error) {
	cfg = normalizeRegistryConfig(cfg)

	r := &Registry{
		config:      cfg,
		stopCleanup: make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}

	if err := r.initSQLite(); err != nil {
		return nil, fmt.Errorf("failed to initialize SQLite: %w", err)
	}

	go r.cleanupLoop()

	return r, nil
}

func normalizeRegistryConfig(cfg RegistryConfig) RegistryConfig {
	cfg.DBPath = defaultIfEmptyString(cfg.DBPath, filepath.Join(os.Getenv("HOME"), DefaultRegistryDBPath))
	cfg.HeartbeatInterval = defaultIfZeroDuration(cfg.HeartbeatInterval, DefaultHeartbeatInterval)
	cfg.StaleThreshold = defaultIfZeroDuration(cfg.StaleThreshold, DefaultStaleThreshold)
	cfg.ActivityDecayRate = defaultIfZeroFloat(cfg.ActivityDecayRate, DefaultActivityDecayRate)
	cfg.CleanupInterval = defaultIfZeroDuration(cfg.CleanupInterval, DefaultCleanupInterval)
	return cfg
}

func defaultIfEmptyString(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func defaultIfZeroDuration(val, def time.Duration) time.Duration {
	if val == 0 {
		return def
	}
	return val
}

func defaultIfZeroFloat(val, def float64) float64 {
	if val == 0 {
		return def
	}
	return val
}

func (r *Registry) initSQLite() error {
	if err := r.ensureDBDirectory(); err != nil {
		return err
	}

	db, err := r.openDatabase()
	if err != nil {
		return err
	}

	if err := r.configureAndCreateSchema(db); err != nil {
		db.Close()
		return err
	}

	r.db = db
	return nil
}

func (r *Registry) ensureDBDirectory() error {
	dir := filepath.Dir(r.config.DBPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

func (r *Registry) openDatabase() (*sql.DB, error) {
	db, err := sql.Open("sqlite", r.config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(DefaultRegistryMaxOpenConns)
	return db, nil
}

func (r *Registry) configureAndCreateSchema(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("failed to enable WAL: %w", err)
	}
	return r.createSchema(db)
}

func (r *Registry) createSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		pid INTEGER NOT NULL,
		start_time TIMESTAMP NOT NULL,
		last_heartbeat TIMESTAMP NOT NULL,
		activity_score REAL NOT NULL DEFAULT 0.0,
		running_pipelines INTEGER NOT NULL DEFAULT 0,
		allocated_slots INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_heartbeat ON sessions(last_heartbeat);
	CREATE INDEX IF NOT EXISTS idx_sessions_activity ON sessions(activity_score DESC);

	CREATE TABLE IF NOT EXISTS allocations (
		session_id TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		allocated_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session_id, resource_type),
		FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
	);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// =============================================================================
// Session Lifecycle
// =============================================================================

// Register adds a session to the registry
func (r *Registry) Register(sessionID string) error {
	if err := r.checkClosed(); err != nil {
		return err
	}

	now := time.Now()
	pid := os.Getpid()

	_, err := r.db.Exec(`
		INSERT INTO sessions (session_id, pid, start_time, last_heartbeat, activity_score)
		VALUES (?, ?, ?, ?, 0.0)
	`, sessionID, pid, now, now)

	return r.handleInsertError(err)
}

func (r *Registry) handleInsertError(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueConstraintError(err) {
		return ErrRegistrySessionExists
	}
	return fmt.Errorf("failed to register session: %w", err)
}

// Unregister removes a session from the registry
func (r *Registry) Unregister(sessionID string) error {
	if err := r.checkClosed(); err != nil {
		return err
	}

	result, err := r.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("failed to unregister session: %w", err)
	}

	return r.checkRowsAffected(result, ErrRegistrySessionNotFound)
}

func (r *Registry) checkClosed() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return ErrRegistryClosed
	}
	return nil
}

func (r *Registry) checkRowsAffected(result sql.Result, notFoundErr error) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return notFoundErr
	}
	return nil
}

// Heartbeat updates session liveness and activity score
func (r *Registry) Heartbeat(sessionID string, runningPipelines, recentInvocations int) error {
	if err := r.checkClosed(); err != nil {
		return err
	}

	activityScore := r.calculateActivityScore(runningPipelines, recentInvocations)
	now := time.Now()

	result, err := r.db.Exec(`
		UPDATE sessions
		SET last_heartbeat = ?, activity_score = ?, running_pipelines = ?
		WHERE session_id = ?
	`, now, activityScore, runningPipelines, sessionID)

	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	return r.checkRowsAffected(result, ErrRegistrySessionNotFound)
}

func (r *Registry) calculateActivityScore(runningPipelines, recentInvocations int) float64 {
	return float64(runningPipelines) + float64(recentInvocations)*r.config.ActivityDecayRate
}

func (r *Registry) Touch(sessionID string) error {
	if err := r.checkClosed(); err != nil {
		return err
	}

	result, err := r.db.Exec(`
		UPDATE sessions
		SET last_heartbeat = ?
		WHERE session_id = ?
	`, time.Now(), sessionID)
	if err != nil {
		return fmt.Errorf("failed to touch session: %w", err)
	}

	return r.checkRowsAffected(result, ErrRegistrySessionNotFound)
}

// =============================================================================
// Session Discovery
// =============================================================================

// GetActiveSessions returns all non-stale sessions with valid PIDs
func (r *Registry) GetActiveSessions() ([]SessionRecord, error) {
	if err := r.checkClosed(); err != nil {
		return nil, err
	}

	return r.queryActiveSessions()
}

func (r *Registry) queryActiveSessions() ([]SessionRecord, error) {
	cutoff := time.Now().Add(-r.config.StaleThreshold)

	rows, err := r.db.Query(`
		SELECT session_id, pid, start_time, last_heartbeat, activity_score,
		       running_pipelines, allocated_slots
		FROM sessions
		WHERE last_heartbeat > ?
		ORDER BY activity_score DESC
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	return r.scanAndValidateSessions(rows)
}

func (r *Registry) scanAndValidateSessions(rows *sql.Rows) ([]SessionRecord, error) {
	sessions, deadSessions, err := r.collectSessions(rows)
	if err != nil {
		return nil, err
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	if err := r.removeDeadSessions(deadSessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (r *Registry) collectSessions(rows *sql.Rows) ([]SessionRecord, []string, error) {
	var sessions []SessionRecord
	var deadSessions []string

	for rows.Next() {
		record, err := r.scanSessionRow(rows)
		if err != nil {
			return nil, nil, err
		}

		sessions, deadSessions = r.classifySession(record, sessions, deadSessions)
	}

	return sessions, deadSessions, nil
}

func (r *Registry) classifySession(record SessionRecord, sessions []SessionRecord, deadSessions []string) ([]SessionRecord, []string) {
	if r.isProcessRunning(record.PID, record.StartTime) {
		return append(sessions, record), deadSessions
	}
	return sessions, append(deadSessions, record.SessionID)
}

func (r *Registry) scanSessionRow(rows *sql.Rows) (SessionRecord, error) {
	var record SessionRecord
	err := rows.Scan(
		&record.SessionID,
		&record.PID,
		&record.StartTime,
		&record.LastHeartbeat,
		&record.ActivityScore,
		&record.RunningPipelines,
		&record.AllocatedSlots,
	)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("failed to scan session: %w", err)
	}
	return record, nil
}

// removeDeadSessions deletes sessions whose processes are no longer running.
// Returns an error if any database delete operation fails (W12.29 fix).
func (r *Registry) removeDeadSessions(sessionIDs []string) error {
	for _, id := range sessionIDs {
		_, err := r.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, id)
		if err != nil {
			return fmt.Errorf("failed to remove dead session %s: %w", id, err)
		}
	}
	return nil
}

// GetSession returns a single session by ID
func (r *Registry) GetSession(sessionID string) (*SessionRecord, error) {
	if err := r.checkClosed(); err != nil {
		return nil, err
	}

	record, err := r.querySession(sessionID)
	if err != nil {
		return nil, err
	}

	return r.validateSessionProcess(sessionID, record)
}

func (r *Registry) querySession(sessionID string) (*SessionRecord, error) {
	row := r.db.QueryRow(`
		SELECT session_id, pid, start_time, last_heartbeat, activity_score,
		       running_pipelines, allocated_slots
		FROM sessions
		WHERE session_id = ?
	`, sessionID)

	var record SessionRecord
	err := row.Scan(
		&record.SessionID,
		&record.PID,
		&record.StartTime,
		&record.LastHeartbeat,
		&record.ActivityScore,
		&record.RunningPipelines,
		&record.AllocatedSlots,
	)

	if err == sql.ErrNoRows {
		return nil, ErrRegistrySessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return &record, nil
}

// validateSessionProcess checks if the session's process is still running.
// If not, deletes the session from the database and returns ErrProcessNotRunning.
// Returns database errors to caller for proper handling (W12.29 fix).
func (r *Registry) validateSessionProcess(sessionID string, record *SessionRecord) (*SessionRecord, error) {
	if !r.isProcessRunning(record.PID, record.StartTime) {
		_, err := r.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, sessionID)
		if err != nil {
			return nil, fmt.Errorf("failed to delete dead session %s: %w", sessionID, err)
		}
		return nil, ErrProcessNotRunning
	}
	return record, nil
}

// CleanupStaleSessions removes dead or stale sessions
func (r *Registry) CleanupStaleSessions() error {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return ErrRegistryClosed
	}
	r.mu.RUnlock()

	cutoff := time.Now().Add(-r.config.StaleThreshold)

	_, err := r.db.Exec(`DELETE FROM sessions WHERE last_heartbeat < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup stale sessions: %w", err)
	}

	return r.cleanupDeadProcesses()
}

func (r *Registry) cleanupDeadProcesses() error {
	rows, err := r.db.Query(`SELECT session_id, pid, start_time FROM sessions`)
	if err != nil {
		return fmt.Errorf("failed to query sessions for cleanup: %w", err)
	}
	defer rows.Close()

	deadSessions := r.findDeadSessions(rows)
	return r.removeDeadSessions(deadSessions)
}

func (r *Registry) findDeadSessions(rows *sql.Rows) []string {
	var deadSessions []string

	for rows.Next() {
		sessionID := r.checkSessionProcess(rows)
		if sessionID != "" {
			deadSessions = append(deadSessions, sessionID)
		}
	}

	return deadSessions
}

func (r *Registry) checkSessionProcess(rows *sql.Rows) string {
	var sessionID string
	var pid int
	var startTime time.Time

	if err := rows.Scan(&sessionID, &pid, &startTime); err != nil {
		return ""
	}

	if !r.isProcessRunning(pid, startTime) {
		return sessionID
	}
	return ""
}

// =============================================================================
// Process Validation
// =============================================================================

func (r *Registry) isProcessRunning(pid int, startTime time.Time) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		return false
	}

	return r.validateProcessStartTime(pid, startTime)
}

func (r *Registry) validateProcessStartTime(pid int, _ time.Time) bool {
	// On Unix, we can't easily get process start time without /proc
	// For now, we assume if the process responds to signal 0, it's valid
	// A more robust implementation would check /proc/<pid>/stat on Linux
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// =============================================================================
// Allocation Management
// =============================================================================

// UpdateAllocatedSlots updates the allocated slots for a session
func (r *Registry) UpdateAllocatedSlots(sessionID string, slots int) error {
	if err := r.checkClosed(); err != nil {
		return err
	}

	result, err := r.db.Exec(`
		UPDATE sessions SET allocated_slots = ? WHERE session_id = ?
	`, slots, sessionID)

	if err != nil {
		return fmt.Errorf("failed to update allocated slots: %w", err)
	}

	return r.checkRowsAffected(result, ErrRegistrySessionNotFound)
}

// SetResourceAllocation sets the allocation for a specific resource type
func (r *Registry) SetResourceAllocation(sessionID, resourceType string, count int) error {
	if err := r.checkClosed(); err != nil {
		return err
	}

	_, err := r.db.Exec(`
		INSERT INTO allocations (session_id, resource_type, allocated_count)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id, resource_type) DO UPDATE SET allocated_count = ?
	`, sessionID, resourceType, count, count)

	if err != nil {
		return fmt.Errorf("failed to set resource allocation: %w", err)
	}

	return nil
}

// GetResourceAllocation gets the allocation for a specific resource type
func (r *Registry) GetResourceAllocation(sessionID, resourceType string) (int, error) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return 0, ErrRegistryClosed
	}
	r.mu.RUnlock()

	var count int
	err := r.db.QueryRow(`
		SELECT allocated_count FROM allocations
		WHERE session_id = ? AND resource_type = ?
	`, sessionID, resourceType).Scan(&count)

	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get resource allocation: %w", err)
	}

	return count, nil
}

// =============================================================================
// Cleanup Loop
// =============================================================================

func (r *Registry) cleanupLoop() {
	defer close(r.cleanupDone)

	timer := time.NewTimer(r.config.CleanupInterval)
	defer timer.Stop()

	for {
		select {
		case <-r.stopCleanup:
			return
		case <-timer.C:
			r.CleanupStaleSessions()
			timer.Reset(r.config.CleanupInterval)
		}
	}
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close closes the registry and its database connection
func (r *Registry) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRegistryClosed
	}
	r.closed = true
	r.mu.Unlock()

	close(r.stopCleanup)
	<-r.cleanupDone

	if r.db != nil {
		return r.db.Close()
	}

	return nil
}

// =============================================================================
// Helpers
// =============================================================================

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return containsString(err.Error(), "UNIQUE constraint failed")
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
