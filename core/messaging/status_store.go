package messaging

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"
	_ "modernc.org/sqlite"
)

// =============================================================================
// Status Store - Tiered Message Status Tracking
// =============================================================================
//
// StatusStore provides tiered storage for message status tracking:
// - Hot (L1): Ristretto cache for fast access to recent/active messages
// - Cold (L2): SQLite for durable storage of completed/evicted messages
//
// When messages are evicted from Ristretto (based on size/count limits),
// they are automatically moved to SQLite cold storage.

const (
	// DefaultStatusStorePath is the default SQLite database location
	DefaultStatusStorePath = ".sylk/message_status.db"

	// Default cache configuration
	defaultNumCounters = 1e6  // 1M counters for admission policy
	defaultMaxCost     = 1e8  // 100MB max cache size
	defaultBufferItems = 64   // Buffer for async operations
)

// StatusRecord is the minimal representation of message status for storage.
// We don't store the full Message[T] - just the tracking metadata.
type StatusRecord struct {
	ID            string        `json:"id"`
	CorrelationID string        `json:"correlation_id,omitempty"`
	ParentID      string        `json:"parent_id,omitempty"`
	Source        string        `json:"source"`
	Target        string        `json:"target,omitempty"`
	Type          MessageType   `json:"type"`
	Status        MessageStatus `json:"status"`
	Priority      Priority      `json:"priority"`
	Attempt       int           `json:"attempt"`
	MaxAttempts   int           `json:"max_attempts,omitempty"`
	Timestamp     time.Time     `json:"timestamp"`
	Deadline      *time.Time    `json:"deadline,omitempty"`
	TTL           time.Duration `json:"ttl,omitempty"`
	ProcessedAt   *time.Time    `json:"processed_at,omitempty"`
	Error         string        `json:"error,omitempty"`
	ArchivedAt    *time.Time    `json:"archived_at,omitempty"`
}

// Cost returns the estimated memory cost for Ristretto
func (r *StatusRecord) Cost() int64 {
	// Base struct size + string lengths
	cost := int64(200) // Base overhead
	cost += int64(len(r.ID))
	cost += int64(len(r.CorrelationID))
	cost += int64(len(r.ParentID))
	cost += int64(len(r.Source))
	cost += int64(len(r.Target))
	cost += int64(len(r.Error))
	return cost
}

// StatusStore provides tiered message status storage
type StatusStore struct {
	// Hot storage (L1) - Ristretto cache
	cache *ristretto.Cache

	// Cold storage (L2) - SQLite
	db   *sql.DB
	path string

	// Pending evictions buffer (for batch writes to SQLite)
	evictionMu sync.Mutex
	evictions  []*StatusRecord

	// Configuration
	config StatusStoreConfig

	// Metrics (internal with mutex)
	metrics internalMetrics
}

// StatusStoreConfig configures the status store
type StatusStoreConfig struct {
	// SQLite path (empty = default)
	DBPath string

	// Ristretto configuration
	NumCounters int64 // Number of keys to track frequency
	MaxCost     int64 // Maximum cache size in bytes
	BufferItems int64 // Number of keys per Get buffer

	// Eviction batch size (flush to SQLite when reached)
	EvictionBatchSize int

	// Whether to persist completed messages
	PersistCompleted bool

	// TTL for records in cold storage (0 = forever)
	ColdStorageTTL time.Duration
}

// DefaultStatusStoreConfig returns sensible defaults
func DefaultStatusStoreConfig() StatusStoreConfig {
	return StatusStoreConfig{
		DBPath:            DefaultStatusStorePath,
		NumCounters:       int64(defaultNumCounters),
		MaxCost:           int64(defaultMaxCost),
		BufferItems:       defaultBufferItems,
		EvictionBatchSize: 100,
		PersistCompleted:  true,
		ColdStorageTTL:    7 * 24 * time.Hour, // 7 days
	}
}

// StatusStoreMetrics is returned by Stats() - snapshot of metrics
type StatusStoreMetrics struct {
	HotHits         int64 `json:"hot_hits"`
	HotMisses       int64 `json:"hot_misses"`
	ColdHits        int64 `json:"cold_hits"`
	ColdMisses      int64 `json:"cold_misses"`
	Evictions       int64 `json:"evictions"`
	TotalStored     int64 `json:"total_stored"`
	TotalArchived   int64 `json:"total_archived"`
	ActiveMessages  int64 `json:"active_messages"`
	FailedMessages  int64 `json:"failed_messages"`
	ExpiredMessages int64 `json:"expired_messages"`
}

// internalMetrics holds metrics with mutex for thread-safe updates
type internalMetrics struct {
	mu              sync.RWMutex
	HotHits         int64
	HotMisses       int64
	ColdHits        int64
	ColdMisses      int64
	Evictions       int64
	TotalStored     int64
	TotalArchived   int64
	ActiveMessages  int64
	FailedMessages  int64
	ExpiredMessages int64
}

// NewStatusStore creates a new tiered status store
func NewStatusStore(cfg StatusStoreConfig) (*StatusStore, error) {
	if cfg.DBPath == "" {
		cfg.DBPath = DefaultStatusStorePath
	}
	if cfg.NumCounters == 0 {
		cfg.NumCounters = int64(defaultNumCounters)
	}
	if cfg.MaxCost == 0 {
		cfg.MaxCost = int64(defaultMaxCost)
	}
	if cfg.BufferItems == 0 {
		cfg.BufferItems = defaultBufferItems
	}
	if cfg.EvictionBatchSize == 0 {
		cfg.EvictionBatchSize = 100
	}

	store := &StatusStore{
		config:    cfg,
		evictions: make([]*StatusRecord, 0, cfg.EvictionBatchSize),
	}

	// Initialize SQLite (cold storage)
	if err := store.initSQLite(cfg.DBPath); err != nil {
		return nil, fmt.Errorf("failed to initialize SQLite: %w", err)
	}

	// Initialize Ristretto (hot storage) with eviction callback
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: cfg.NumCounters,
		MaxCost:     cfg.MaxCost,
		BufferItems: cfg.BufferItems,
		OnEvict:     store.onEvict,
		OnReject:    store.onReject,
	})
	if err != nil {
		store.db.Close()
		return nil, fmt.Errorf("failed to initialize Ristretto cache: %w", err)
	}
	store.cache = cache

	return store, nil
}

// initSQLite initializes the SQLite database
func (s *StatusStore) initSQLite(path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Open database
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Enable WAL mode
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return fmt.Errorf("failed to enable WAL: %w", err)
	}

	// Create schema
	schema := `
	CREATE TABLE IF NOT EXISTS message_status (
		id TEXT PRIMARY KEY,
		correlation_id TEXT,
		parent_id TEXT,
		source TEXT NOT NULL,
		target TEXT,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		priority INTEGER NOT NULL,
		attempt INTEGER NOT NULL,
		max_attempts INTEGER,
		timestamp TIMESTAMP NOT NULL,
		deadline TIMESTAMP,
		ttl INTEGER,
		processed_at TIMESTAMP,
		error TEXT,
		archived_at TIMESTAMP NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_status_correlation ON message_status(correlation_id);
	CREATE INDEX IF NOT EXISTS idx_status_source ON message_status(source);
	CREATE INDEX IF NOT EXISTS idx_status_target ON message_status(target);
	CREATE INDEX IF NOT EXISTS idx_status_status ON message_status(status);
	CREATE INDEX IF NOT EXISTS idx_status_timestamp ON message_status(timestamp);
	CREATE INDEX IF NOT EXISTS idx_status_archived ON message_status(archived_at);
	`

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return fmt.Errorf("failed to create schema: %w", err)
	}

	s.db = db
	s.path = path
	return nil
}

// onEvict is called when Ristretto evicts an item
func (s *StatusStore) onEvict(item *ristretto.Item) {
	record, ok := item.Value.(*StatusRecord)
	if !ok {
		return
	}

	s.evictionMu.Lock()
	s.evictions = append(s.evictions, record)

	// Flush if batch size reached
	if len(s.evictions) >= s.config.EvictionBatchSize {
		batch := s.evictions
		s.evictions = make([]*StatusRecord, 0, s.config.EvictionBatchSize)
		s.evictionMu.Unlock()

		// Async flush to SQLite
		go s.flushEvictions(batch)
	} else {
		s.evictionMu.Unlock()
	}

	s.metrics.mu.Lock()
	s.metrics.Evictions++
	s.metrics.mu.Unlock()
}

// onReject is called when Ristretto rejects an item (shouldn't happen with our config)
func (s *StatusStore) onReject(item *ristretto.Item) {
	// Immediately archive rejected items
	record, ok := item.Value.(*StatusRecord)
	if !ok {
		return
	}

	go s.archiveRecord(record)
}

// flushEvictions writes a batch of evicted records to SQLite
func (s *StatusStore) flushEvictions(records []*StatusRecord) {
	if len(records) == 0 {
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		return // Log error in production
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO message_status
		(id, correlation_id, parent_id, source, target, type, status, priority,
		 attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return
	}
	defer stmt.Close()

	now := time.Now()
	for _, record := range records {
		record.ArchivedAt = &now

		var ttlNanos int64
		if record.TTL > 0 {
			ttlNanos = int64(record.TTL)
		}

		_, err := stmt.Exec(
			record.ID, record.CorrelationID, record.ParentID,
			record.Source, record.Target, record.Type, record.Status, record.Priority,
			record.Attempt, record.MaxAttempts, record.Timestamp, record.Deadline,
			ttlNanos, record.ProcessedAt, record.Error, record.ArchivedAt,
		)
		if err != nil {
			continue // Log error in production
		}
	}

	tx.Commit()

	s.metrics.mu.Lock()
	s.metrics.TotalArchived += int64(len(records))
	s.metrics.mu.Unlock()
}

// archiveRecord writes a single record to SQLite
func (s *StatusStore) archiveRecord(record *StatusRecord) {
	now := time.Now()
	record.ArchivedAt = &now

	var ttlNanos int64
	if record.TTL > 0 {
		ttlNanos = int64(record.TTL)
	}

	_, _ = s.db.Exec(`
		INSERT OR REPLACE INTO message_status
		(id, correlation_id, parent_id, source, target, type, status, priority,
		 attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.ID, record.CorrelationID, record.ParentID,
		record.Source, record.Target, record.Type, record.Status, record.Priority,
		record.Attempt, record.MaxAttempts, record.Timestamp, record.Deadline,
		ttlNanos, record.ProcessedAt, record.Error, record.ArchivedAt,
	)

	s.metrics.mu.Lock()
	s.metrics.TotalArchived++
	s.metrics.mu.Unlock()
}

// =============================================================================
// Public API
// =============================================================================

// Track stores or updates a message's status record.
// Creates a StatusRecord from the Message[T] envelope.
// Records are written to both hot cache (for fast lookup) and cold storage (for durability/queries).
func Track[T any](s *StatusStore, msg *Message[T]) {
	record := &StatusRecord{
		ID:            msg.ID,
		CorrelationID: msg.CorrelationID,
		ParentID:      msg.ParentID,
		Source:        msg.Source,
		Target:        msg.Target,
		Type:          msg.Type,
		Status:        msg.Status,
		Priority:      msg.Priority,
		Attempt:       msg.Attempt,
		MaxAttempts:   msg.MaxAttempts,
		Timestamp:     msg.Timestamp,
		Deadline:      msg.Deadline,
		TTL:           msg.TTL,
		ProcessedAt:   msg.ProcessedAt,
		Error:         msg.Error,
	}

	// Write to hot cache for fast lookups
	s.cache.Set(msg.ID, record, record.Cost())

	// Also write to cold storage for durability and queryability
	go s.persistRecord(record)

	s.metrics.mu.Lock()
	s.metrics.TotalStored++
	if !msg.Status.IsTerminal() {
		s.metrics.ActiveMessages++
	} else if msg.Status == StatusFailed {
		s.metrics.FailedMessages++
	} else if msg.Status == StatusExpired {
		s.metrics.ExpiredMessages++
	}
	s.metrics.mu.Unlock()
}

// persistRecord writes a record to SQLite (without archived_at timestamp)
func (s *StatusStore) persistRecord(record *StatusRecord) {
	var ttlNanos int64
	if record.TTL > 0 {
		ttlNanos = int64(record.TTL)
	}

	_, _ = s.db.Exec(`
		INSERT OR REPLACE INTO message_status
		(id, correlation_id, parent_id, source, target, type, status, priority,
		 attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.ID, record.CorrelationID, record.ParentID,
		record.Source, record.Target, record.Type, record.Status, record.Priority,
		record.Attempt, record.MaxAttempts, record.Timestamp, record.Deadline,
		ttlNanos, record.ProcessedAt, record.Error, time.Now(),
	)
}

// Get retrieves a status record by message ID.
// Checks hot storage first, then cold storage.
func (s *StatusStore) Get(id string) (*StatusRecord, bool) {
	// Check hot storage
	if val, ok := s.cache.Get(id); ok {
		s.metrics.mu.Lock()
		s.metrics.HotHits++
		s.metrics.mu.Unlock()
		return val.(*StatusRecord), true
	}

	s.metrics.mu.Lock()
	s.metrics.HotMisses++
	s.metrics.mu.Unlock()

	// Check cold storage
	record, err := s.getFromCold(id)
	if err == nil && record != nil {
		s.metrics.mu.Lock()
		s.metrics.ColdHits++
		s.metrics.mu.Unlock()

		// Promote back to hot storage
		s.cache.Set(id, record, record.Cost())
		return record, true
	}

	s.metrics.mu.Lock()
	s.metrics.ColdMisses++
	s.metrics.mu.Unlock()

	return nil, false
}

// getFromCold retrieves a record from SQLite
func (s *StatusStore) getFromCold(id string) (*StatusRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, correlation_id, parent_id, source, target, type, status, priority,
		       attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at
		FROM message_status WHERE id = ?
	`, id)

	return scanStatusRecord(row)
}

// GetByCorrelation retrieves all records with a given correlation ID
func (s *StatusStore) GetByCorrelation(correlationID string) ([]*StatusRecord, error) {
	// Query cold storage (comprehensive)
	rows, err := s.db.Query(`
		SELECT id, correlation_id, parent_id, source, target, type, status, priority,
		       attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at
		FROM message_status WHERE correlation_id = ?
		ORDER BY timestamp ASC
	`, correlationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*StatusRecord
	seen := make(map[string]bool)

	for rows.Next() {
		record, err := scanStatusRecordRows(rows)
		if err != nil {
			continue
		}
		records = append(records, record)
		seen[record.ID] = true
	}

	// Also check hot storage for any not yet evicted
	// (Ristretto doesn't support iteration, so we rely on cold storage being authoritative)

	return records, rows.Err()
}

// GetByStatus retrieves records by status
func (s *StatusStore) GetByStatus(status MessageStatus, limit int) ([]*StatusRecord, error) {
	query := `
		SELECT id, correlation_id, parent_id, source, target, type, status, priority,
		       attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at
		FROM message_status WHERE status = ?
		ORDER BY timestamp DESC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*StatusRecord
	for rows.Next() {
		record, err := scanStatusRecordRows(rows)
		if err != nil {
			continue
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

// GetActive retrieves all non-terminal status records
func (s *StatusStore) GetActive(limit int) ([]*StatusRecord, error) {
	query := `
		SELECT id, correlation_id, parent_id, source, target, type, status, priority,
		       attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at
		FROM message_status
		WHERE status NOT IN (?, ?, ?, ?)
		ORDER BY priority DESC, timestamp ASC
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, StatusCompleted, StatusFailed, StatusExpired, StatusCancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*StatusRecord
	for rows.Next() {
		record, err := scanStatusRecordRows(rows)
		if err != nil {
			continue
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

// UpdateStatus updates just the status field for a message
func (s *StatusStore) UpdateStatus(id string, status MessageStatus, errorMsg string) error {
	var processedAt *time.Time
	if status.IsTerminal() {
		now := time.Now()
		processedAt = &now
	}

	// Update hot storage if present
	if val, ok := s.cache.Get(id); ok {
		record := val.(*StatusRecord)
		record.Status = status
		record.Error = errorMsg
		record.ProcessedAt = processedAt
		s.cache.Set(id, record, record.Cost())
	}

	// Update cold storage (synchronous for consistency)
	_, err := s.db.Exec(`
		UPDATE message_status
		SET status = ?, error = ?, processed_at = ?
		WHERE id = ?
	`, status, errorMsg, processedAt, id)

	return err
}

// Delete removes a record from both hot and cold storage
func (s *StatusStore) Delete(id string) error {
	s.cache.Del(id)
	_, err := s.db.Exec("DELETE FROM message_status WHERE id = ?", id)
	return err
}

// Cleanup removes old records from cold storage based on TTL
func (s *StatusStore) Cleanup() (int64, error) {
	if s.config.ColdStorageTTL == 0 {
		return 0, nil
	}

	cutoff := time.Now().Add(-s.config.ColdStorageTTL)
	result, err := s.db.Exec(`
		DELETE FROM message_status
		WHERE archived_at < ? AND status IN (?, ?, ?, ?)
	`, cutoff, StatusCompleted, StatusFailed, StatusExpired, StatusCancelled)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// Flush forces all pending evictions to be written to SQLite
func (s *StatusStore) Flush() error {
	s.evictionMu.Lock()
	batch := s.evictions
	s.evictions = make([]*StatusRecord, 0, s.config.EvictionBatchSize)
	s.evictionMu.Unlock()

	if len(batch) > 0 {
		s.flushEvictions(batch)
	}

	return nil
}

// Close flushes pending writes and closes the store
func (s *StatusStore) Close() error {
	s.Flush()
	s.cache.Close()
	return s.db.Close()
}

// Stats returns a snapshot of current store statistics
func (s *StatusStore) Stats() StatusStoreMetrics {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()
	return StatusStoreMetrics{
		HotHits:         s.metrics.HotHits,
		HotMisses:       s.metrics.HotMisses,
		ColdHits:        s.metrics.ColdHits,
		ColdMisses:      s.metrics.ColdMisses,
		Evictions:       s.metrics.Evictions,
		TotalStored:     s.metrics.TotalStored,
		TotalArchived:   s.metrics.TotalArchived,
		ActiveMessages:  s.metrics.ActiveMessages,
		FailedMessages:  s.metrics.FailedMessages,
		ExpiredMessages: s.metrics.ExpiredMessages,
	}
}

// =============================================================================
// Helpers
// =============================================================================

func scanStatusRecord(row *sql.Row) (*StatusRecord, error) {
	var record StatusRecord
	var correlationID, parentID, target, errStr sql.NullString
	var deadline, processedAt, archivedAt sql.NullTime
	var ttlNanos sql.NullInt64

	err := row.Scan(
		&record.ID, &correlationID, &parentID, &record.Source, &target,
		&record.Type, &record.Status, &record.Priority,
		&record.Attempt, &record.MaxAttempts, &record.Timestamp,
		&deadline, &ttlNanos, &processedAt, &errStr, &archivedAt,
	)
	if err != nil {
		return nil, err
	}

	if correlationID.Valid {
		record.CorrelationID = correlationID.String
	}
	if parentID.Valid {
		record.ParentID = parentID.String
	}
	if target.Valid {
		record.Target = target.String
	}
	if deadline.Valid {
		record.Deadline = &deadline.Time
	}
	if ttlNanos.Valid {
		record.TTL = time.Duration(ttlNanos.Int64)
	}
	if processedAt.Valid {
		record.ProcessedAt = &processedAt.Time
	}
	if errStr.Valid {
		record.Error = errStr.String
	}
	if archivedAt.Valid {
		record.ArchivedAt = &archivedAt.Time
	}

	return &record, nil
}

func scanStatusRecordRows(rows *sql.Rows) (*StatusRecord, error) {
	var record StatusRecord
	var correlationID, parentID, target, errStr sql.NullString
	var deadline, processedAt, archivedAt sql.NullTime
	var ttlNanos sql.NullInt64

	err := rows.Scan(
		&record.ID, &correlationID, &parentID, &record.Source, &target,
		&record.Type, &record.Status, &record.Priority,
		&record.Attempt, &record.MaxAttempts, &record.Timestamp,
		&deadline, &ttlNanos, &processedAt, &errStr, &archivedAt,
	)
	if err != nil {
		return nil, err
	}

	if correlationID.Valid {
		record.CorrelationID = correlationID.String
	}
	if parentID.Valid {
		record.ParentID = parentID.String
	}
	if target.Valid {
		record.Target = target.String
	}
	if deadline.Valid {
		record.Deadline = &deadline.Time
	}
	if ttlNanos.Valid {
		record.TTL = time.Duration(ttlNanos.Int64)
	}
	if processedAt.Valid {
		record.ProcessedAt = &processedAt.Time
	}
	if errStr.Valid {
		record.Error = errStr.String
	}
	if archivedAt.Valid {
		record.ArchivedAt = &archivedAt.Time
	}

	return &record, nil
}
