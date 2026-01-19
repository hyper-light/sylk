// Package search provides document search types and utilities for the Sylk search system.
// This file implements the SearchSystem facade that integrates all search components.
package search

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// DefaultStartTimeout is the maximum time allowed for system startup.
	DefaultStartTimeout = 30 * time.Second

	// DefaultStopTimeout is the maximum time allowed for graceful shutdown.
	DefaultStopTimeout = 10 * time.Second

	// DefaultIndexTimeout is the default timeout for index operations.
	DefaultIndexTimeout = 60 * time.Second
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrSystemNotRunning indicates an operation was attempted when not running.
	ErrSystemNotRunning = errors.New("search system is not running")

	// ErrSystemAlreadyRunning indicates Start was called when already running.
	ErrSystemAlreadyRunning = errors.New("search system is already running")

	// ErrSystemShuttingDown indicates an operation was attempted during shutdown.
	ErrSystemShuttingDown = errors.New("search system is shutting down")

	// ErrNilConfig indicates a nil configuration was provided.
	ErrNilConfig = errors.New("configuration cannot be nil")

	// ErrNilBleveManager indicates the Bleve index manager is nil.
	ErrNilBleveManager = errors.New("bleve index manager cannot be nil")

	// ErrStartFailed indicates system startup failed.
	ErrStartFailed = errors.New("search system start failed")
)

// =============================================================================
// SystemState
// =============================================================================

// SystemState represents the current state of the search system.
type SystemState int32

const (
	// StateStopped indicates the system is not running.
	StateStopped SystemState = iota

	// StateStarting indicates the system is initializing.
	StateStarting

	// StateRunning indicates the system is fully operational.
	StateRunning

	// StateStopping indicates the system is shutting down.
	StateStopping

	// StateError indicates the system encountered a fatal error.
	StateError
)

// String returns the string representation of the system state.
func (s SystemState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// =============================================================================
// SearchSystemConfig
// =============================================================================

// SearchSystemConfig configures the SearchSystem facade.
type SearchSystemConfig struct {
	// BlevePath is the path to the Bleve index directory.
	BlevePath string

	// VectorDBPath is the path to the VectorDB data directory.
	VectorDBPath string

	// CMTPath is the path to the CMT manifest file.
	CMTPath string

	// StartTimeout is the maximum time for system startup.
	StartTimeout time.Duration

	// StopTimeout is the maximum time for graceful shutdown.
	StopTimeout time.Duration

	// IndexTimeout is the default timeout for index operations.
	IndexTimeout time.Duration

	// MaxConcurrentSearches is the maximum concurrent search operations.
	MaxConcurrentSearches int

	// MaxConcurrentIndexes is the maximum concurrent index operations.
	MaxConcurrentIndexes int

	// EnableWatcher enables file system watching.
	EnableWatcher bool

	// WatchPaths are the paths to watch for changes.
	WatchPaths []string
}

// DefaultSearchSystemConfig returns sensible defaults.
func DefaultSearchSystemConfig() SearchSystemConfig {
	return SearchSystemConfig{
		StartTimeout:          DefaultStartTimeout,
		StopTimeout:           DefaultStopTimeout,
		IndexTimeout:          DefaultIndexTimeout,
		MaxConcurrentSearches: 10,
		MaxConcurrentIndexes:  4,
		EnableWatcher:         true,
	}
}

// Validate checks that the configuration is valid.
func (c *SearchSystemConfig) Validate() error {
	if c.BlevePath == "" {
		return errors.New("bleve path is required")
	}
	return nil
}

// applyDefaults sets default values for zero-valued fields.
func (c *SearchSystemConfig) applyDefaults() {
	if c.StartTimeout <= 0 {
		c.StartTimeout = DefaultStartTimeout
	}
	if c.StopTimeout <= 0 {
		c.StopTimeout = DefaultStopTimeout
	}
	if c.IndexTimeout <= 0 {
		c.IndexTimeout = DefaultIndexTimeout
	}
	if c.MaxConcurrentSearches <= 0 {
		c.MaxConcurrentSearches = 10
	}
	if c.MaxConcurrentIndexes <= 0 {
		c.MaxConcurrentIndexes = 4
	}
}

// =============================================================================
// Component Interfaces
// =============================================================================

// BleveManager defines the interface for Bleve index operations.
type BleveManager interface {
	Open() error
	Close() error
	IsOpen() bool
	Search(ctx context.Context, req *SearchRequest) (*SearchResult, error)
	Index(ctx context.Context, doc *Document) error
	IndexBatch(ctx context.Context, docs []*Document) error
	Delete(ctx context.Context, id string) error
	DocumentCount() (uint64, error)
}

// VectorDBManager defines the interface for VectorDB operations.
type VectorDBManager interface {
	Open() error
	Close() error
	Search(ctx context.Context, query []float32, limit int) ([]ScoredDocument, error)
}

// ChangeWatcher defines the interface for file change detection.
type ChangeWatcher interface {
	Start(ctx context.Context) error
	Stop() error
	Events() <-chan ChangeEvent
}

// ChangeEvent represents a detected file change.
type ChangeEvent struct {
	Path      string
	Operation string
	Time      time.Time
}

// ManifestTracker defines the interface for file manifest tracking.
type ManifestTracker interface {
	Get(path string) (string, bool)
	Set(path string, checksum string) error
	Delete(path string) error
	Size() int64
	Close() error
}

// =============================================================================
// SearchSystemStatus
// =============================================================================

// SearchSystemStatus contains the current status of the search system.
type SearchSystemStatus struct {
	// State is the current system state.
	State SystemState `json:"state"`

	// StartedAt is when the system was started.
	StartedAt time.Time `json:"started_at,omitempty"`

	// Uptime is how long the system has been running.
	Uptime time.Duration `json:"uptime,omitempty"`

	// DocumentCount is the number of indexed documents.
	DocumentCount uint64 `json:"document_count"`

	// IndexingInProgress indicates if indexing is currently running.
	IndexingInProgress bool `json:"indexing_in_progress"`

	// LastIndexTime is when the last indexing operation completed.
	LastIndexTime time.Time `json:"last_index_time,omitempty"`

	// LastSearchTime is when the last search was performed.
	LastSearchTime time.Time `json:"last_search_time,omitempty"`

	// SearchCount is the total number of searches performed.
	SearchCount int64 `json:"search_count"`

	// ErrorCount is the number of errors encountered.
	ErrorCount int64 `json:"error_count"`

	// LastError is the most recent error message.
	LastError string `json:"last_error,omitempty"`

	// WatcherActive indicates if file watching is active.
	WatcherActive bool `json:"watcher_active"`

	// WatchedPaths are the paths being watched.
	WatchedPaths []string `json:"watched_paths,omitempty"`
}

// =============================================================================
// SearchSystem
// =============================================================================

// SearchSystem is the main facade integrating all search components.
// It provides unified access to Bleve full-text search, VectorDB semantic
// search, file change detection, and manifest tracking.
type SearchSystem struct {
	config SearchSystemConfig

	// Components
	bleve    BleveManager
	vectorDB VectorDBManager
	watcher  ChangeWatcher
	manifest ManifestTracker

	// State
	state       atomic.Int32
	startedAt   time.Time
	searchCount atomic.Int64
	errorCount  atomic.Int64
	lastError   atomic.Value // string

	// Timestamps
	lastSearchTime atomic.Value // time.Time
	lastIndexTime  atomic.Value // time.Time

	// Indexing state
	indexingMu sync.RWMutex
	indexing   bool

	// Concurrency control
	searchSem chan struct{}
	indexSem  chan struct{}

	// Lifecycle
	cancelFunc context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.RWMutex
}

// NewSearchSystem creates a new SearchSystem with the given configuration.
func NewSearchSystem(config SearchSystemConfig) (*SearchSystem, error) {
	config.applyDefaults()

	if err := config.Validate(); err != nil {
		return nil, err
	}

	ss := &SearchSystem{
		config:    config,
		searchSem: make(chan struct{}, config.MaxConcurrentSearches),
		indexSem:  make(chan struct{}, config.MaxConcurrentIndexes),
	}

	ss.state.Store(int32(StateStopped))

	return ss, nil
}

// NewSearchSystemWithComponents creates a SearchSystem with injected components.
func NewSearchSystemWithComponents(
	config SearchSystemConfig,
	bleve BleveManager,
	vectorDB VectorDBManager,
	watcher ChangeWatcher,
	manifest ManifestTracker,
) (*SearchSystem, error) {
	ss, err := NewSearchSystem(config)
	if err != nil {
		return nil, err
	}

	ss.bleve = bleve
	ss.vectorDB = vectorDB
	ss.watcher = watcher
	ss.manifest = manifest

	return ss, nil
}

// =============================================================================
// Lifecycle Methods
// =============================================================================

// Start initializes all components and starts the search system.
func (ss *SearchSystem) Start(ctx context.Context) error {
	if !ss.transitionState(StateStopped, StateStarting) {
		return ErrSystemAlreadyRunning
	}

	startCtx, cancel := context.WithTimeout(ctx, ss.config.StartTimeout)
	defer cancel()

	if err := ss.initializeComponents(startCtx); err != nil {
		ss.recordError(err)
		ss.state.Store(int32(StateError))
		return errors.Join(ErrStartFailed, err)
	}

	ss.startBackgroundTasks(ctx)
	ss.startedAt = time.Now()
	ss.state.Store(int32(StateRunning))

	return nil
}

// initializeComponents opens all system components.
func (ss *SearchSystem) initializeComponents(ctx context.Context) error {
	if err := ss.openBleve(ctx); err != nil {
		return err
	}

	if err := ss.openVectorDB(ctx); err != nil {
		return err
	}

	return nil
}

// openBleve opens the Bleve index manager.
func (ss *SearchSystem) openBleve(ctx context.Context) error {
	if ss.bleve == nil {
		return nil // No Bleve configured
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return ss.bleve.Open()
}

// openVectorDB opens the VectorDB.
func (ss *SearchSystem) openVectorDB(ctx context.Context) error {
	if ss.vectorDB == nil {
		return nil // No VectorDB configured
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return ss.vectorDB.Open()
}

// startBackgroundTasks starts watcher and other background processing.
func (ss *SearchSystem) startBackgroundTasks(ctx context.Context) {
	ctx, ss.cancelFunc = context.WithCancel(ctx)

	if ss.watcher != nil && ss.config.EnableWatcher {
		ss.wg.Add(1)
		go ss.runWatcher(ctx)
	}
}

// runWatcher runs the file change watcher.
func (ss *SearchSystem) runWatcher(ctx context.Context) {
	defer ss.wg.Done()

	if err := ss.watcher.Start(ctx); err != nil {
		ss.recordError(err)
		return
	}

	ss.processWatcherEvents(ctx)
}

// processWatcherEvents handles events from the watcher.
func (ss *SearchSystem) processWatcherEvents(ctx context.Context) {
	events := ss.watcher.Events()
	if events == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			ss.handleWatcherEvent(ctx, event)
		}
	}
}

// handleWatcherEvent processes a single watcher event.
func (ss *SearchSystem) handleWatcherEvent(_ context.Context, _ ChangeEvent) {
	// Placeholder for incremental indexing integration
	// This would trigger re-indexing of the changed file
}

// Stop gracefully shuts down all components.
func (ss *SearchSystem) Stop() error {
	if !ss.transitionState(StateRunning, StateStopping) {
		return ErrSystemNotRunning
	}

	// Cancel background tasks
	if ss.cancelFunc != nil {
		ss.cancelFunc()
	}

	// Wait for background tasks with timeout
	done := make(chan struct{})
	go func() {
		ss.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(ss.config.StopTimeout):
		// Timeout waiting for tasks
	}

	// Close components
	errs := ss.closeComponents()
	ss.state.Store(int32(StateStopped))

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// closeComponents closes all system components.
func (ss *SearchSystem) closeComponents() []error {
	var errs []error

	if ss.watcher != nil {
		if err := ss.watcher.Stop(); err != nil {
			errs = append(errs, err)
		}
	}

	if ss.bleve != nil {
		if err := ss.bleve.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if ss.vectorDB != nil {
		if err := ss.vectorDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if ss.manifest != nil {
		if err := ss.manifest.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// transitionState atomically transitions from one state to another.
func (ss *SearchSystem) transitionState(from, to SystemState) bool {
	return ss.state.CompareAndSwap(int32(from), int32(to))
}

// =============================================================================
// Search Methods
// =============================================================================

// Search performs a search across configured backends.
func (ss *SearchSystem) Search(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
	if err := ss.checkRunning(); err != nil {
		return nil, err
	}

	if err := ss.acquireSearchSlot(ctx); err != nil {
		return nil, err
	}
	defer ss.releaseSearchSlot()

	result, err := ss.executeSearch(ctx, req)
	ss.recordSearch()

	if err != nil {
		ss.recordError(err)
	}

	return result, err
}

// executeSearch runs the actual search operation.
func (ss *SearchSystem) executeSearch(ctx context.Context, req *SearchRequest) (*SearchResult, error) {
	if ss.bleve == nil {
		return nil, ErrNilBleveManager
	}

	return ss.bleve.Search(ctx, req)
}

// acquireSearchSlot acquires a slot from the search semaphore.
func (ss *SearchSystem) acquireSearchSlot(ctx context.Context) error {
	select {
	case ss.searchSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseSearchSlot releases a slot back to the search semaphore.
func (ss *SearchSystem) releaseSearchSlot() {
	<-ss.searchSem
}

// recordSearch updates search statistics.
func (ss *SearchSystem) recordSearch() {
	ss.searchCount.Add(1)
	ss.lastSearchTime.Store(time.Now())
}

// =============================================================================
// Index Methods
// =============================================================================

// Index indexes specific file paths.
func (ss *SearchSystem) Index(ctx context.Context, paths []string) (*IndexingResult, error) {
	if err := ss.checkRunning(); err != nil {
		return nil, err
	}

	if err := ss.acquireIndexSlot(ctx); err != nil {
		return nil, err
	}
	defer ss.releaseIndexSlot()

	ss.setIndexing(true)
	defer ss.setIndexing(false)

	result := ss.indexPaths(ctx, paths)
	ss.recordIndex()

	return result, nil
}

// indexPaths indexes a list of file paths.
func (ss *SearchSystem) indexPaths(ctx context.Context, paths []string) *IndexingResult {
	result := NewIndexingResult()
	startTime := time.Now()

	for _, path := range paths {
		if isCancelled(ctx) {
			break
		}

		if err := ss.indexSinglePath(ctx, path); err != nil {
			result.AddFailure("", path, err.Error(), true)
		} else {
			result.AddSuccess()
		}
	}

	result.Duration = time.Since(startTime)
	return result
}

// indexSinglePath indexes a single file path.
func (ss *SearchSystem) indexSinglePath(ctx context.Context, path string) error {
	if ss.bleve == nil {
		return ErrNilBleveManager
	}

	// Build document from path (placeholder - actual implementation would read file)
	doc := &Document{
		ID:        GenerateDocumentID([]byte(path)),
		Path:      path,
		Type:      DocTypeSourceCode,
		IndexedAt: time.Now(),
	}

	return ss.bleve.Index(ctx, doc)
}

// Reindex performs a full reindex of all configured paths.
func (ss *SearchSystem) Reindex(ctx context.Context) (*IndexingResult, error) {
	if err := ss.checkRunning(); err != nil {
		return nil, err
	}

	if err := ss.acquireIndexSlot(ctx); err != nil {
		return nil, err
	}
	defer ss.releaseIndexSlot()

	ss.setIndexing(true)
	defer ss.setIndexing(false)

	// Full reindex would scan all watch paths
	result := ss.reindexAll(ctx)
	ss.recordIndex()

	return result, nil
}

// reindexAll performs a full reindex operation.
func (ss *SearchSystem) reindexAll(_ context.Context) *IndexingResult {
	result := NewIndexingResult()
	result.Duration = 0
	return result
}

// acquireIndexSlot acquires a slot from the index semaphore.
func (ss *SearchSystem) acquireIndexSlot(ctx context.Context) error {
	select {
	case ss.indexSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseIndexSlot releases a slot back to the index semaphore.
func (ss *SearchSystem) releaseIndexSlot() {
	<-ss.indexSem
}

// setIndexing updates the indexing flag.
func (ss *SearchSystem) setIndexing(indexing bool) {
	ss.indexingMu.Lock()
	ss.indexing = indexing
	ss.indexingMu.Unlock()
}

// isIndexing returns whether indexing is in progress.
func (ss *SearchSystem) isIndexing() bool {
	ss.indexingMu.RLock()
	defer ss.indexingMu.RUnlock()
	return ss.indexing
}

// recordIndex updates indexing statistics.
func (ss *SearchSystem) recordIndex() {
	ss.lastIndexTime.Store(time.Now())
}

// =============================================================================
// Status Methods
// =============================================================================

// GetStatus returns the current status of the search system.
func (ss *SearchSystem) GetStatus() *SearchSystemStatus {
	state := SystemState(ss.state.Load())

	status := &SearchSystemStatus{
		State:              state,
		IndexingInProgress: ss.isIndexing(),
		SearchCount:        ss.searchCount.Load(),
		ErrorCount:         ss.errorCount.Load(),
		WatcherActive:      ss.isWatcherActive(),
		WatchedPaths:       ss.config.WatchPaths,
	}

	if state == StateRunning {
		status.StartedAt = ss.startedAt
		status.Uptime = time.Since(ss.startedAt)
	}

	if ss.bleve != nil && ss.bleve.IsOpen() {
		if count, err := ss.bleve.DocumentCount(); err == nil {
			status.DocumentCount = count
		}
	}

	if lastSearch := ss.lastSearchTime.Load(); lastSearch != nil {
		status.LastSearchTime = lastSearch.(time.Time)
	}

	if lastIndex := ss.lastIndexTime.Load(); lastIndex != nil {
		status.LastIndexTime = lastIndex.(time.Time)
	}

	if lastErr := ss.lastError.Load(); lastErr != nil {
		status.LastError = lastErr.(string)
	}

	return status
}

// IsRunning returns true if the system is in the running state.
func (ss *SearchSystem) IsRunning() bool {
	return SystemState(ss.state.Load()) == StateRunning
}

// State returns the current system state.
func (ss *SearchSystem) State() SystemState {
	return SystemState(ss.state.Load())
}

// isWatcherActive returns true if the watcher is active.
func (ss *SearchSystem) isWatcherActive() bool {
	return ss.watcher != nil && ss.config.EnableWatcher && ss.IsRunning()
}

// =============================================================================
// Internal Helpers
// =============================================================================

// checkRunning verifies the system is in a running state.
func (ss *SearchSystem) checkRunning() error {
	state := SystemState(ss.state.Load())

	switch state {
	case StateRunning:
		return nil
	case StateStopping:
		return ErrSystemShuttingDown
	default:
		return ErrSystemNotRunning
	}
}

// recordError records an error for status reporting.
func (ss *SearchSystem) recordError(err error) {
	ss.errorCount.Add(1)
	if err != nil {
		ss.lastError.Store(err.Error())
	}
}

// isCancelled checks if the context has been cancelled.
func isCancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
