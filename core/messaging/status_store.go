package messaging

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/ristretto"
	_ "modernc.org/sqlite"
)

const (
	DefaultStatusStorePath = ".sylk/message_status.db"

	defaultNumCounters = 1e6 // 1M counters for admission policy
	defaultMaxCost     = 1e8 // 100MB max cache size
	defaultBufferItems = 64  // Buffer for async operations
)

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

func (r *StatusRecord) Cost() int64 {
	cost := int64(200)
	cost += int64(len(r.ID))
	cost += int64(len(r.CorrelationID))
	cost += int64(len(r.ParentID))
	cost += int64(len(r.Source))
	cost += int64(len(r.Target))
	cost += int64(len(r.Error))
	return cost
}

type StatusStore struct {
	cache *ristretto.Cache

	db   *sql.DB
	path string

	evictionMu sync.Mutex
	evictions  []*StatusRecord

	config StatusStoreConfig

	metrics internalMetrics
}

type StatusStoreConfig struct {
	DBPath string

	NumCounters int64
	MaxCost     int64
	BufferItems int64

	EvictionBatchSize int

	PersistCompleted bool

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
		ColdStorageTTL:    7 * 24 * time.Hour,
	}
}

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

type internalMetrics struct {
	HotHits         atomic.Int64
	HotMisses       atomic.Int64
	ColdHits        atomic.Int64
	ColdMisses      atomic.Int64
	Evictions       atomic.Int64
	TotalStored     atomic.Int64
	TotalArchived   atomic.Int64
	ActiveMessages  atomic.Int64
	FailedMessages  atomic.Int64
	ExpiredMessages atomic.Int64
}

func NewStatusStore(cfg StatusStoreConfig) (*StatusStore, error) {
	cfg = normalizeStatusStoreConfig(cfg)

	store := &StatusStore{
		config:    cfg,
		evictions: make([]*StatusRecord, 0, cfg.EvictionBatchSize),
	}

	if err := store.initSQLite(cfg.DBPath); err != nil {
		return nil, fmt.Errorf("failed to initialize SQLite: %w", err)
	}

	cache, err := store.initCache()
	if err != nil {
		store.db.Close()
		return nil, err
	}
	store.cache = cache

	return store, nil
}

func normalizeStatusStoreConfig(cfg StatusStoreConfig) StatusStoreConfig {
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
	return cfg
}

func (s *StatusStore) initCache() (*ristretto.Cache, error) {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: s.config.NumCounters,
		MaxCost:     s.config.MaxCost,
		BufferItems: s.config.BufferItems,
		OnEvict:     s.onEvict,
		OnReject:    s.onReject,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Ristretto cache: %w", err)
	}
	return cache, nil
}

// initSQLite initializes the SQLite database
func (s *StatusStore) initSQLite(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return fmt.Errorf("failed to enable WAL: %w", err)
	}

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

func (s *StatusStore) onEvict(item *ristretto.Item) {
	record, ok := item.Value.(*StatusRecord)
	if !ok {
		return
	}

	s.evictionMu.Lock()
	s.evictions = append(s.evictions, record)

	if len(s.evictions) >= s.config.EvictionBatchSize {
		batch := s.evictions
		s.evictions = make([]*StatusRecord, 0, s.config.EvictionBatchSize)
		s.evictionMu.Unlock()

		s.flushEvictions(batch)
	} else {
		s.evictionMu.Unlock()
	}

	s.metrics.Evictions.Add(1)
}

func (s *StatusStore) onReject(item *ristretto.Item) {
	record, ok := item.Value.(*StatusRecord)
	if !ok {
		return
	}

	s.archiveRecord(record)
}

func (s *StatusStore) flushEvictions(records []*StatusRecord) {
	if len(records) == 0 {
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		return
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
			continue
		}
	}

	tx.Commit()

	s.metrics.TotalArchived.Add(int64(len(records)))
}

func (s *StatusStore) archiveRecord(record *StatusRecord) {
	if record.ArchivedAt == nil {
		now := time.Now()
		record.ArchivedAt = &now
	}

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

	s.metrics.TotalArchived.Add(1)
}

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

	s.cache.Set(msg.ID, record, record.Cost())
	s.cache.Wait()

	s.persistRecord(record)

	s.metrics.TotalStored.Add(1)
	if !msg.Status.IsTerminal() {
		s.metrics.ActiveMessages.Add(1)
	} else if msg.Status == StatusFailed {
		s.metrics.FailedMessages.Add(1)
	} else if msg.Status == StatusExpired {
		s.metrics.ExpiredMessages.Add(1)
	}
}

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

func (s *StatusStore) Get(id string) (*StatusRecord, bool) {
	if record, ok := s.getFromHot(id); ok {
		return record, true
	}

	record, ok := s.getFromColdStore(id)
	if ok {
		return record, true
	}

	s.metrics.ColdMisses.Add(1)
	return nil, false
}

func (s *StatusStore) getFromHot(id string) (*StatusRecord, bool) {
	val, ok := s.cache.Get(id)
	if !ok {
		s.metrics.HotMisses.Add(1)
		return nil, false
	}

	s.metrics.HotHits.Add(1)
	return val.(*StatusRecord), true
}

func (s *StatusStore) getFromColdStore(id string) (*StatusRecord, bool) {
	record, err := s.getFromCold(id)
	if err != nil || record == nil {
		return nil, false
	}

	s.metrics.ColdHits.Add(1)
	s.cache.Set(id, record, record.Cost())
	return record, true
}

func (s *StatusStore) getFromCold(id string) (*StatusRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, correlation_id, parent_id, source, target, type, status, priority,
		       attempt, max_attempts, timestamp, deadline, ttl, processed_at, error, archived_at
		FROM message_status WHERE id = ?
	`, id)

	return scanStatusRecord(row)
}

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

func (s *StatusStore) UpdateStatus(id string, status MessageStatus, errorMsg string) error {
	var processedAt *time.Time
	if status.IsTerminal() {
		now := time.Now()
		processedAt = &now
	}

	if val, ok := s.cache.Get(id); ok {
		record := val.(*StatusRecord)
		record.Status = status
		record.Error = errorMsg
		record.ProcessedAt = processedAt
		s.cache.Set(id, record, record.Cost())
	}

	_, err := s.db.Exec(`
		UPDATE message_status
		SET status = ?, error = ?, processed_at = ?
		WHERE id = ?
	`, status, errorMsg, processedAt, id)

	return err
}

func (s *StatusStore) Delete(id string) error {
	s.cache.Del(id)
	_, err := s.db.Exec("DELETE FROM message_status WHERE id = ?", id)
	return err
}

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

func (s *StatusStore) Close() error {
	s.Flush()
	s.cache.Close()
	return s.db.Close()
}

func (s *StatusStore) Stats() StatusStoreMetrics {
	return StatusStoreMetrics{
		HotHits:         s.metrics.HotHits.Load(),
		HotMisses:       s.metrics.HotMisses.Load(),
		ColdHits:        s.metrics.ColdHits.Load(),
		ColdMisses:      s.metrics.ColdMisses.Load(),
		Evictions:       s.metrics.Evictions.Load(),
		TotalStored:     s.metrics.TotalStored.Load(),
		TotalArchived:   s.metrics.TotalArchived.Load(),
		ActiveMessages:  s.metrics.ActiveMessages.Load(),
		FailedMessages:  s.metrics.FailedMessages.Load(),
		ExpiredMessages: s.metrics.ExpiredMessages.Load(),
	}
}

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

	applyStatusRecordOptionals(&record, correlationID, parentID, target, deadline, ttlNanos, processedAt, errStr, archivedAt)
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

	applyStatusRecordOptionals(&record, correlationID, parentID, target, deadline, ttlNanos, processedAt, errStr, archivedAt)
	return &record, nil
}

func applyStatusRecordOptionals(record *StatusRecord, correlationID, parentID, target sql.NullString, deadline sql.NullTime, ttlNanos sql.NullInt64, processedAt sql.NullTime, errStr sql.NullString, archivedAt sql.NullTime) {
	setOptionalString(&record.CorrelationID, correlationID)
	setOptionalString(&record.ParentID, parentID)
	setOptionalString(&record.Target, target)
	setOptionalTime(&record.Deadline, deadline)
	setOptionalDuration(&record.TTL, ttlNanos)
	setOptionalTime(&record.ProcessedAt, processedAt)
	setOptionalString(&record.Error, errStr)
	setOptionalTime(&record.ArchivedAt, archivedAt)
}

func setOptionalString(target *string, value sql.NullString) {
	if value.Valid {
		*target = value.String
	}
}

func setOptionalTime(target **time.Time, value sql.NullTime) {
	if value.Valid {
		*target = &value.Time
	}
}

func setOptionalDuration(target *time.Duration, value sql.NullInt64) {
	if value.Valid {
		*target = time.Duration(value.Int64)
	}
}
