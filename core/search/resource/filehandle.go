package resource

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrFileHandleExhausted is returned when no file handles are available.
	ErrFileHandleExhausted = errors.New("file handle budget exhausted")
	// ErrFileHandleClosed is returned when the budget has been closed.
	ErrFileHandleClosed = errors.New("file handle budget closed")
)

// =============================================================================
// FileHandleBudget Interface
// =============================================================================

// FileHandleBudget defines the interface for the underlying file handle budget system.
// This matches the core/resources/FileHandleBudget API.
type FileHandleBudget interface {
	// TryAllocate attempts to allocate the specified number of handles.
	TryAllocate(amount int64) bool
	// Deallocate returns handles to the budget.
	Deallocate(amount int64)
	// IncrementUsed increments the used counter.
	IncrementUsed()
	// DecrementUsed decrements the used counter.
	DecrementUsed()
	// Available returns the number of available handles.
	Available() int64
	// GlobalLimit returns the total limit.
	GlobalLimit() int64
}

// =============================================================================
// Handle Types
// =============================================================================

// HandleType identifies the type of file handle being tracked.
type HandleType int

const (
	// HandleTypeRead is for reading files (e.g., search queries).
	HandleTypeRead HandleType = iota
	// HandleTypeIndex is for indexing files.
	HandleTypeIndex
	// HandleTypeBleve is for Bleve index files (persistent).
	HandleTypeBleve
	// HandleTypeSQLite is for SQLite database files (persistent).
	HandleTypeSQLite
)

// String returns the string representation of the handle type.
func (h HandleType) String() string {
	switch h {
	case HandleTypeRead:
		return "read"
	case HandleTypeIndex:
		return "index"
	case HandleTypeBleve:
		return "bleve"
	case HandleTypeSQLite:
		return "sqlite"
	default:
		return "unknown"
	}
}

// =============================================================================
// SearchFileHandleBudget Configuration
// =============================================================================

// SearchFileHandleBudgetConfig configures the search file handle budget.
type SearchFileHandleBudgetConfig struct {
	// Budget is the underlying file handle budget (required).
	Budget FileHandleBudget
	// BleveHandles is the number of persistent handles for Bleve.
	BleveHandles int
	// SQLiteHandles is the number of persistent handles for SQLite.
	SQLiteHandles int
	// MaxReadHandles limits concurrent read operations.
	MaxReadHandles int
	// MaxIndexHandles limits concurrent index operations.
	MaxIndexHandles int
	// Logger for budget operations.
	Logger *slog.Logger
	// OnDegraded is called when operating in degraded mode.
	OnDegraded func(handleType HandleType, reason string)
}

// DefaultSearchFileHandleBudgetConfig returns sensible defaults.
func DefaultSearchFileHandleBudgetConfig() SearchFileHandleBudgetConfig {
	return SearchFileHandleBudgetConfig{
		BleveHandles:    4,
		SQLiteHandles:   2,
		MaxReadHandles:  32,
		MaxIndexHandles: 16,
		Logger:          slog.Default(),
	}
}

// =============================================================================
// SearchFileHandleBudget
// =============================================================================

// SearchFileHandleBudget wraps FileHandleBudget with search-specific semantics.
// It tracks Bleve and SQLite as persistent handles and provides separate
// tracking for read and index operations.
type SearchFileHandleBudget struct {
	budget FileHandleBudget
	logger *slog.Logger

	bleveHandles  int32
	sqliteHandles int32
	maxRead       int32
	maxIndex      int32

	activePersistent atomic.Int32
	activeRead       atomic.Int32
	activeIndex      atomic.Int32

	trackedPaths sync.Map // path -> HandleType
	onDegraded   func(handleType HandleType, reason string)

	mu     sync.RWMutex
	closed bool
}

// NewSearchFileHandleBudget creates a new search file handle budget wrapper.
func NewSearchFileHandleBudget(cfg SearchFileHandleBudgetConfig) (*SearchFileHandleBudget, error) {
	cfg = normalizeFileHandleConfig(cfg)

	sfb := &SearchFileHandleBudget{
		budget:        cfg.Budget,
		logger:        cfg.Logger,
		bleveHandles:  int32(cfg.BleveHandles),
		sqliteHandles: int32(cfg.SQLiteHandles),
		maxRead:       int32(cfg.MaxReadHandles),
		maxIndex:      int32(cfg.MaxIndexHandles),
		onDegraded:    cfg.OnDegraded,
	}

	if err := sfb.reservePersistentHandles(); err != nil {
		return nil, err
	}

	return sfb, nil
}

func normalizeFileHandleConfig(cfg SearchFileHandleBudgetConfig) SearchFileHandleBudgetConfig {
	if cfg.BleveHandles <= 0 {
		cfg.BleveHandles = 4
	}
	if cfg.SQLiteHandles <= 0 {
		cfg.SQLiteHandles = 2
	}
	if cfg.MaxReadHandles <= 0 {
		cfg.MaxReadHandles = 32
	}
	if cfg.MaxIndexHandles <= 0 {
		cfg.MaxIndexHandles = 16
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

func (s *SearchFileHandleBudget) reservePersistentHandles() error {
	if s.budget == nil {
		return nil
	}

	total := int64(s.bleveHandles + s.sqliteHandles)
	if !s.budget.TryAllocate(total) {
		return ErrFileHandleExhausted
	}

	s.activePersistent.Store(int32(total))
	return nil
}

// =============================================================================
// Read Operations
// =============================================================================

// AcquireForRead acquires a file handle for reading a file.
// Returns a release function and any error encountered.
func (s *SearchFileHandleBudget) AcquireForRead(ctx context.Context, path string) (func(), error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	if err := s.acquireReadHandle(ctx); err != nil {
		return nil, err
	}

	s.trackPath(path, HandleTypeRead)
	return s.makeReadReleaser(path), nil
}

func (s *SearchFileHandleBudget) acquireReadHandle(ctx context.Context) error {
	if !s.canAcquireRead() {
		s.notifyDegraded(HandleTypeRead, "read limit reached")
		return ErrFileHandleExhausted
	}

	if err := s.checkContext(ctx); err != nil {
		return err
	}

	if s.budget != nil {
		if !s.budget.TryAllocate(1) {
			s.notifyDegraded(HandleTypeRead, "global budget exhausted")
			return ErrFileHandleExhausted
		}
		s.budget.IncrementUsed()
	}

	s.activeRead.Add(1)
	return nil
}

func (s *SearchFileHandleBudget) canAcquireRead() bool {
	return s.activeRead.Load() < s.maxRead
}

func (s *SearchFileHandleBudget) makeReadReleaser(path string) func() {
	released := &atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			s.releaseReadHandle(path)
		}
	}
}

func (s *SearchFileHandleBudget) releaseReadHandle(path string) {
	s.activeRead.Add(-1)
	s.untrackPath(path)

	if s.budget != nil {
		s.budget.DecrementUsed()
		s.budget.Deallocate(1)
	}
}

// TryAcquireForRead attempts non-blocking acquisition for reading.
// Returns a release function and true if successful, nil and false otherwise.
func (s *SearchFileHandleBudget) TryAcquireForRead(path string) (func(), bool) {
	if s.checkClosed() != nil {
		return nil, false
	}

	if !s.canAcquireRead() {
		return nil, false
	}

	if s.budget != nil && !s.budget.TryAllocate(1) {
		return nil, false
	}

	s.activeRead.Add(1)
	if s.budget != nil {
		s.budget.IncrementUsed()
	}

	s.trackPath(path, HandleTypeRead)
	return s.makeReadReleaser(path), true
}

// =============================================================================
// Index Operations
// =============================================================================

// AcquireForIndex acquires a file handle for indexing a file.
// Returns a release function and any error encountered.
func (s *SearchFileHandleBudget) AcquireForIndex(ctx context.Context, path string) (func(), error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	if err := s.acquireIndexHandle(ctx); err != nil {
		return nil, err
	}

	s.trackPath(path, HandleTypeIndex)
	return s.makeIndexReleaser(path), nil
}

func (s *SearchFileHandleBudget) acquireIndexHandle(ctx context.Context) error {
	if !s.canAcquireIndex() {
		s.notifyDegraded(HandleTypeIndex, "index limit reached")
		return ErrFileHandleExhausted
	}

	if err := s.checkContext(ctx); err != nil {
		return err
	}

	if s.budget != nil {
		if !s.budget.TryAllocate(1) {
			s.notifyDegraded(HandleTypeIndex, "global budget exhausted")
			return ErrFileHandleExhausted
		}
		s.budget.IncrementUsed()
	}

	s.activeIndex.Add(1)
	return nil
}

func (s *SearchFileHandleBudget) canAcquireIndex() bool {
	return s.activeIndex.Load() < s.maxIndex
}

func (s *SearchFileHandleBudget) makeIndexReleaser(path string) func() {
	released := &atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			s.releaseIndexHandle(path)
		}
	}
}

func (s *SearchFileHandleBudget) releaseIndexHandle(path string) {
	s.activeIndex.Add(-1)
	s.untrackPath(path)

	if s.budget != nil {
		s.budget.DecrementUsed()
		s.budget.Deallocate(1)
	}
}

// TryAcquireForIndex attempts non-blocking acquisition for indexing.
// Returns a release function and true if successful, nil and false otherwise.
func (s *SearchFileHandleBudget) TryAcquireForIndex(path string) (func(), bool) {
	if s.checkClosed() != nil {
		return nil, false
	}

	if !s.canAcquireIndex() {
		return nil, false
	}

	if s.budget != nil && !s.budget.TryAllocate(1) {
		return nil, false
	}

	s.activeIndex.Add(1)
	if s.budget != nil {
		s.budget.IncrementUsed()
	}

	s.trackPath(path, HandleTypeIndex)
	return s.makeIndexReleaser(path), true
}

// =============================================================================
// Persistent Handle Management
// =============================================================================

// RegisterBleveIndex registers a Bleve index path as a persistent handle.
// These handles are pre-allocated during initialization.
func (s *SearchFileHandleBudget) RegisterBleveIndex(path string) {
	s.trackPath(path, HandleTypeBleve)
}

// RegisterSQLiteDB registers a SQLite database path as a persistent handle.
// These handles are pre-allocated during initialization.
func (s *SearchFileHandleBudget) RegisterSQLiteDB(path string) {
	s.trackPath(path, HandleTypeSQLite)
}

// UnregisterBleveIndex removes a Bleve index path from tracking.
func (s *SearchFileHandleBudget) UnregisterBleveIndex(path string) {
	s.untrackPath(path)
}

// UnregisterSQLiteDB removes a SQLite database path from tracking.
func (s *SearchFileHandleBudget) UnregisterSQLiteDB(path string) {
	s.untrackPath(path)
}

// =============================================================================
// Path Tracking
// =============================================================================

func (s *SearchFileHandleBudget) trackPath(path string, handleType HandleType) {
	s.trackedPaths.Store(path, handleType)
}

func (s *SearchFileHandleBudget) untrackPath(path string) {
	s.trackedPaths.Delete(path)
}

// IsPathTracked returns true if the path is currently tracked.
func (s *SearchFileHandleBudget) IsPathTracked(path string) bool {
	_, ok := s.trackedPaths.Load(path)
	return ok
}

// GetPathType returns the handle type for a tracked path.
func (s *SearchFileHandleBudget) GetPathType(path string) (HandleType, bool) {
	val, ok := s.trackedPaths.Load(path)
	if !ok {
		return 0, false
	}
	return val.(HandleType), true
}

// =============================================================================
// Statistics
// =============================================================================

// FileHandleStats contains current statistics for the file handle budget.
type FileHandleStats struct {
	ActiveRead       int32
	ActiveIndex      int32
	ActivePersistent int32
	MaxRead          int32
	MaxIndex         int32
	BleveHandles     int32
	SQLiteHandles    int32
	TrackedPaths     int
}

// GetStats returns current statistics.
func (s *SearchFileHandleBudget) GetStats() FileHandleStats {
	pathCount := 0
	s.trackedPaths.Range(func(_, _ any) bool {
		pathCount++
		return true
	})

	return FileHandleStats{
		ActiveRead:       s.activeRead.Load(),
		ActiveIndex:      s.activeIndex.Load(),
		ActivePersistent: s.activePersistent.Load(),
		MaxRead:          s.maxRead,
		MaxIndex:         s.maxIndex,
		BleveHandles:     s.bleveHandles,
		SQLiteHandles:    s.sqliteHandles,
		TrackedPaths:     pathCount,
	}
}

// ActiveRead returns the count of active read handles.
func (s *SearchFileHandleBudget) ActiveRead() int32 {
	return s.activeRead.Load()
}

// ActiveIndex returns the count of active index handles.
func (s *SearchFileHandleBudget) ActiveIndex() int32 {
	return s.activeIndex.Load()
}

// =============================================================================
// Lifecycle
// =============================================================================

func (s *SearchFileHandleBudget) checkClosed() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrFileHandleClosed
	}
	return nil
}

func (s *SearchFileHandleBudget) checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// Close releases all resources including persistent handles.
func (s *SearchFileHandleBudget) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	s.releasePersistentHandles()
	s.clearTrackedPaths()

	return nil
}

func (s *SearchFileHandleBudget) releasePersistentHandles() {
	if s.budget == nil {
		return
	}

	persistent := s.activePersistent.Load()
	if persistent > 0 {
		s.budget.Deallocate(int64(persistent))
		s.activePersistent.Store(0)
	}
}

func (s *SearchFileHandleBudget) clearTrackedPaths() {
	s.trackedPaths.Range(func(key, _ any) bool {
		s.trackedPaths.Delete(key)
		return true
	})
}

// =============================================================================
// Degradation
// =============================================================================

func (s *SearchFileHandleBudget) notifyDegraded(handleType HandleType, reason string) {
	s.logger.Warn("search file handle operating in degraded mode",
		"handle_type", handleType.String(),
		"reason", reason,
		"active_read", s.activeRead.Load(),
		"active_index", s.activeIndex.Load(),
	)

	if s.onDegraded != nil {
		s.onDegraded(handleType, reason)
	}
}

// =============================================================================
// Graceful Degradation Helpers
// =============================================================================

// AcquireForIndexDegraded acquires handles with graceful degradation.
// If a handle cannot be acquired, it returns false but no error.
func (s *SearchFileHandleBudget) AcquireForIndexDegraded(ctx context.Context, path string) (func(), bool, error) {
	if err := s.checkClosed(); err != nil {
		return nil, false, err
	}

	release, ok := s.TryAcquireForIndex(path)
	if !ok {
		s.notifyDegraded(HandleTypeIndex, "degraded mode - skipping file")
		return nil, false, nil
	}

	return release, true, nil
}

// AcquireForReadDegraded acquires handles with graceful degradation.
// If a handle cannot be acquired, it returns false but no error.
func (s *SearchFileHandleBudget) AcquireForReadDegraded(ctx context.Context, path string) (func(), bool, error) {
	if err := s.checkClosed(); err != nil {
		return nil, false, err
	}

	release, ok := s.TryAcquireForRead(path)
	if !ok {
		s.notifyDegraded(HandleTypeRead, "degraded mode - skipping file")
		return nil, false, nil
	}

	return release, true, nil
}

// AvailableReadHandles returns the number of available read handles.
func (s *SearchFileHandleBudget) AvailableReadHandles() int {
	current := s.activeRead.Load()
	available := s.maxRead - current
	if available < 0 {
		return 0
	}
	return int(available)
}

// AvailableIndexHandles returns the number of available index handles.
func (s *SearchFileHandleBudget) AvailableIndexHandles() int {
	current := s.activeIndex.Load()
	available := s.maxIndex - current
	if available < 0 {
		return 0
	}
	return int(available)
}
