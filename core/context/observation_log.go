// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.4.2: ObservationLog - crash-safe WAL for EpisodeObservations.
package context

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/concurrency/safechan"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultObservationBufferSize is the buffer size for the observation channel.
const DefaultObservationBufferSize = 100

// DefaultFlushInterval is the default interval for flushing the WAL.
const DefaultFlushInterval = 100 * time.Millisecond

// =============================================================================
// Errors
// =============================================================================

var (
	ErrObservationLogClosed = errors.New("observation log is closed")
	ErrInvalidObservation   = errors.New("invalid observation")
	ErrWriteQueueFull       = errors.New("write queue is full")
)

// DefaultWriteQueueSize is the default size of the async write queue.
const DefaultWriteQueueSize = 1000

// DefaultSyncInterval is the default interval for syncing the WAL to disk.
const DefaultSyncInterval = 100 * time.Millisecond

// DefaultSyncFailureThreshold is the default number of consecutive sync failures
// before triggering the health callback.
const DefaultSyncFailureThreshold = 3

// SyncHealthCallback is called when sync failures reach a threshold.
// It receives the number of consecutive failures and the last error.
type SyncHealthCallback func(consecutiveFailures int, lastError error)

// =============================================================================
// Logged Observation
// =============================================================================

// LoggedObservation wraps an observation with WAL metadata.
type LoggedObservation struct {
	Sequence    uint64             `json:"sequence"`
	Timestamp   time.Time          `json:"timestamp"`
	Observation EpisodeObservation `json:"observation"`
	Processed   bool               `json:"processed"`
	Deleted     bool               `json:"deleted,omitempty"`
}

// TruncateMarker is a special record that marks all entries before a sequence as deleted.
type TruncateMarker struct {
	Type            string    `json:"type"`
	BeforeSequence  uint64    `json:"before_sequence"`
	Timestamp       time.Time `json:"timestamp"`
	DeletedCount    int       `json:"deleted_count"`
}

// IsTruncateMarker checks if a JSON line is a truncate marker.
func IsTruncateMarker(data []byte) bool {
	return len(data) > 10 && data[1] == '"' && data[2] == 't' && data[3] == 'y' && data[4] == 'p' && data[5] == 'e'
}

// writeRequest represents an async write request.
type writeRequest struct {
	data     []byte
	sequence uint64
	resultCh chan error
}

// =============================================================================
// Observation Log
// =============================================================================

// ObservationLog provides crash-safe logging of EpisodeObservations.
// Observations are serialized outside the critical section, then written via async queue.
// This reduces lock contention by moving expensive I/O operations to a dedicated writer goroutine.
type ObservationLog struct {
	mu sync.Mutex

	path     string
	file     *os.File
	writer   *bufio.Writer
	sequence uint64

	// Truncation tracking for soft deletes (avoids full file rewrite)
	truncateBeforeSeq uint64 // Entries with sequence < this are logically deleted
	deletedCount      int    // Count of logically deleted entries

	// Async write queue - decouples serialization from I/O
	writeQueue      chan *writeRequest
	writeDone       chan struct{}
	writeQueueClose sync.Once
	syncInterval    time.Duration
	lastSync        time.Time

	// Sync health tracking (W4N.7)
	consecutiveSyncFailures int
	syncFailureThreshold    int
	syncHealthCallback      SyncHealthCallback
	logger                  *slog.Logger

	obsChannel     chan *EpisodeObservation
	obsChannelOnce sync.Once
	closed         atomic.Bool
	closeDone      chan struct{}

	// inlineWg tracks goroutines started without a scope
	inlineWg sync.WaitGroup

	adaptive *AdaptiveState
	scope    *concurrency.GoroutineScope
}

// ObservationLogConfig holds configuration for the observation log.
type ObservationLogConfig struct {
	Path                 string
	BufferSize           int
	WriteQueueSize       int                // Size of async write queue (default: 1000)
	SyncInterval         time.Duration      // Interval between fsync calls (default: 100ms)
	SyncFailureThreshold int                // Consecutive failures before health callback (default: 3)
	SyncHealthCallback   SyncHealthCallback // Optional callback for sync health issues
	Logger               *slog.Logger       // Optional logger for sync errors
	Adaptive             *AdaptiveState
	Scope                *concurrency.GoroutineScope
}

// NewObservationLog creates a new observation log.
func NewObservationLog(ctx context.Context, config ObservationLogConfig) (*ObservationLog, error) {
	if config.BufferSize <= 0 {
		config.BufferSize = DefaultObservationBufferSize
	}
	if config.WriteQueueSize <= 0 {
		config.WriteQueueSize = DefaultWriteQueueSize
	}
	if config.SyncInterval <= 0 {
		config.SyncInterval = DefaultSyncInterval
	}
	if config.SyncFailureThreshold <= 0 {
		config.SyncFailureThreshold = DefaultSyncFailureThreshold
	}

	file, err := openWALFile(config.Path)
	if err != nil {
		return nil, err
	}

	log := &ObservationLog{
		path:                 config.Path,
		file:                 file,
		writer:               bufio.NewWriter(file),
		writeQueue:           make(chan *writeRequest, config.WriteQueueSize),
		writeDone:            make(chan struct{}),
		syncInterval:         config.SyncInterval,
		lastSync:             time.Now(),
		syncFailureThreshold: config.SyncFailureThreshold,
		syncHealthCallback:   config.SyncHealthCallback,
		logger:               normalizeObsLogger(config.Logger),
		obsChannel:           make(chan *EpisodeObservation, config.BufferSize),
		closeDone:            make(chan struct{}),
		adaptive:             config.Adaptive,
		scope:                config.Scope,
	}

	if err := log.loadSequence(); err != nil {
		file.Close()
		return nil, err
	}

	// Start async writer goroutine
	if err := log.startAsyncWriter(ctx); err != nil {
		file.Close()
		return nil, err
	}

	if err := log.startProcessor(ctx); err != nil {
		file.Close()
		return nil, err
	}

	return log, nil
}

func openWALFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open WAL file %q: %w", path, err)
	}
	return file, nil
}

func (l *ObservationLog) loadSequence() error {
	info, err := l.file.Stat()
	if err != nil {
		return err
	}

	if info.Size() == 0 {
		l.sequence = 0
		return nil
	}

	// Read file to find last sequence
	return l.scanForLastSequence()
}

func (l *ObservationLog) scanForLastSequence() error {
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}

	scanner := bufio.NewScanner(l.file)
	var lastSeq uint64
	var truncateSeq uint64
	var deletedCount int

	for scanner.Scan() {
		line := scanner.Bytes()
		if IsTruncateMarker(line) {
			var marker TruncateMarker
			if err := json.Unmarshal(line, &marker); err == nil {
				truncateSeq = marker.BeforeSequence
				deletedCount = marker.DeletedCount
			}
			continue
		}
		var logged LoggedObservation
		if err := json.Unmarshal(line, &logged); err != nil {
			continue
		}
		if logged.Sequence > lastSeq {
			lastSeq = logged.Sequence
		}
	}

	l.sequence = lastSeq
	l.truncateBeforeSeq = truncateSeq
	l.deletedCount = deletedCount

	// Seek to end for appending
	_, err := l.file.Seek(0, 2)
	return err
}

func (l *ObservationLog) startProcessor(ctx context.Context) error {
	if l.scope == nil {
		// No scope provided, track inline goroutine via WaitGroup
		l.inlineWg.Add(1)
		go func() {
			defer l.inlineWg.Done()
			l.processLoop(ctx)
		}()
		return nil
	}

	return l.scope.Go("observation-log-processor", 0, func(innerCtx context.Context) error {
		l.processLoop(innerCtx)
		return nil
	})
}

// startAsyncWriter starts the async writer goroutine.
func (l *ObservationLog) startAsyncWriter(ctx context.Context) error {
	if l.scope == nil {
		// No scope provided, track inline goroutine via WaitGroup
		l.inlineWg.Add(1)
		go func() {
			defer l.inlineWg.Done()
			l.asyncWriteLoop(ctx)
		}()
		return nil
	}

	return l.scope.Go("observation-log-writer", 0, func(innerCtx context.Context) error {
		l.asyncWriteLoop(innerCtx)
		return nil
	})
}

// asyncWriteLoop is the main loop for the async writer goroutine.
// It processes write requests from the queue and performs actual I/O.
func (l *ObservationLog) asyncWriteLoop(ctx context.Context) {
	defer close(l.writeDone)

	// Ticker for periodic sync
	ticker := time.NewTicker(l.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.flushPendingWrites()
			return
		case req, ok := <-l.writeQueue:
			if !ok {
				// Channel closed, flush and exit
				l.flushPendingWrites()
				return
			}
			err := l.performWrite(req.data)
			if req.resultCh != nil {
				req.resultCh <- err
			}
		case <-ticker.C:
			// Periodic sync to ensure durability
			l.periodicSync()
		}
	}
}

// performWrite executes the actual write to the file.
// This runs in the single writer goroutine, so no lock is needed for I/O.
func (l *ObservationLog) performWrite(data []byte) error {
	if _, err := l.writer.Write(data); err != nil {
		return err
	}
	// Flush buffer to OS (but don't sync yet for performance)
	return l.writer.Flush()
}

// periodicSync syncs to disk if enough time has passed.
// Logs errors and tracks consecutive failures for health monitoring.
func (l *ObservationLog) periodicSync() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if time.Since(l.lastSync) < l.syncInterval {
		return
	}

	l.performSyncLocked()
}

// performSyncLocked performs the actual sync and handles errors.
// Must be called with l.mu held.
func (l *ObservationLog) performSyncLocked() {
	if l.file == nil {
		return
	}

	err := l.file.Sync()
	l.lastSync = time.Now()

	if err == nil {
		l.consecutiveSyncFailures = 0
		return
	}

	l.handleSyncError(err)
}

// handleSyncError logs the error and triggers callback if threshold reached.
// Must be called with l.mu held.
func (l *ObservationLog) handleSyncError(err error) {
	l.consecutiveSyncFailures++

	l.logger.Error("WAL periodic sync failed",
		slog.String("path", l.path),
		slog.Int("consecutive_failures", l.consecutiveSyncFailures),
		slog.String("error", err.Error()),
	)

	if l.syncHealthCallback == nil {
		return
	}

	if l.consecutiveSyncFailures >= l.syncFailureThreshold {
		l.syncHealthCallback(l.consecutiveSyncFailures, err)
	}
}

// flushPendingWrites drains the write queue and flushes to disk.
func (l *ObservationLog) flushPendingWrites() {
	// Drain any remaining writes from the queue
	for {
		select {
		case req, ok := <-l.writeQueue:
			if !ok {
				// Channel closed
				l.finalSync()
				return
			}
			err := l.performWrite(req.data)
			if req.resultCh != nil {
				req.resultCh <- err
			}
		default:
			// Queue empty
			l.finalSync()
			return
		}
	}
}

// finalSync performs final flush and sync.
func (l *ObservationLog) finalSync() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writer.Flush()
	l.file.Sync()
}

func (l *ObservationLog) processLoop(ctx context.Context) {
	defer close(l.closeDone)

	for {
		obs, err := safechan.Recv(ctx, l.obsChannel)
		if err != nil {
			return
		}
		l.processObservation(obs)
	}
}

func (l *ObservationLog) processObservation(obs *EpisodeObservation) {
	if l.adaptive == nil {
		return
	}
	l.adaptive.UpdateFromOutcome(*obs)
}

// =============================================================================
// Record Methods
// =============================================================================

// Record writes an observation to the WAL and queues it for async processing.
func (l *ObservationLog) Record(ctx context.Context, obs *EpisodeObservation) error {
	if l.closed.Load() {
		return ErrObservationLogClosed
	}

	if obs == nil {
		return ErrInvalidObservation
	}

	// Write to WAL synchronously
	if err := l.writeToWAL(obs); err != nil {
		return err
	}

	// Queue for async processing (non-blocking)
	l.queueForProcessing(ctx, obs)

	return nil
}

func (l *ObservationLog) writeToWAL(obs *EpisodeObservation) error {
	// Step 1: Assign sequence under lock (fast)
	l.mu.Lock()
	seq := l.sequence + 1
	l.sequence = seq
	l.mu.Unlock()

	// Step 2: Serialize outside lock (can be slow, doesn't block other operations)
	logged := LoggedObservation{
		Sequence:    seq,
		Timestamp:   time.Now(),
		Observation: *obs,
		Processed:   false,
	}

	data, err := json.Marshal(logged)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Step 3: Queue for async write
	req := &writeRequest{
		data:     data,
		sequence: seq,
		resultCh: make(chan error, 1),
	}

	select {
	case l.writeQueue <- req:
		// Wait for write to complete to ensure durability
		return <-req.resultCh
	default:
		// Queue is full - apply backpressure
		return ErrWriteQueueFull
	}
}

// writeToWALSync writes synchronously to WAL, bypassing the async queue.
// Used for operations that require immediate durability (e.g., shutdown).
func (l *ObservationLog) writeToWALSync(obs *EpisodeObservation) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sequence++
	logged := LoggedObservation{
		Sequence:    l.sequence,
		Timestamp:   time.Now(),
		Observation: *obs,
		Processed:   false,
	}

	data, err := json.Marshal(logged)
	if err != nil {
		return err
	}

	data = append(data, '\n')

	if _, err := l.writer.Write(data); err != nil {
		return err
	}

	if err := l.writer.Flush(); err != nil {
		return err
	}

	return l.file.Sync()
}

func (l *ObservationLog) queueForProcessing(ctx context.Context, obs *EpisodeObservation) {
	select {
	case l.obsChannel <- obs:
	case <-ctx.Done():
	default:
		// Channel full, observation was written to WAL and will be replayed
	}
}

// =============================================================================
// Replay Methods
// =============================================================================

// Replay reads all unprocessed observations and applies them to the adaptive state.
// Returns the number of observations replayed.
func (l *ObservationLog) Replay() (int, error) {
	if l.closed.Load() {
		return 0, ErrObservationLogClosed
	}

	observations, err := l.readAllObservations()
	if err != nil {
		return 0, err
	}

	count := l.applyObservations(observations)
	return count, nil
}

func (l *ObservationLog) readAllObservations() ([]LoggedObservation, error) {
	l.mu.Lock()
	truncateSeq := l.truncateBeforeSeq
	l.mu.Unlock()

	return l.readObservationsFiltered(truncateSeq)
}

func (l *ObservationLog) readObservationsFiltered(truncateSeq uint64) ([]LoggedObservation, error) {
	file, err := os.Open(l.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var observations []LoggedObservation
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Bytes()
		if IsTruncateMarker(line) {
			continue // Skip truncate markers
		}
		var logged LoggedObservation
		if err := json.Unmarshal(line, &logged); err != nil {
			continue
		}
		// Filter out logically deleted entries
		if logged.Sequence >= truncateSeq && !logged.Deleted {
			observations = append(observations, logged)
		}
	}

	return observations, scanner.Err()
}

func (l *ObservationLog) applyObservations(observations []LoggedObservation) int {
	if l.adaptive == nil {
		return 0
	}

	count := 0
	for _, logged := range observations {
		l.adaptive.UpdateFromOutcome(logged.Observation)
		count++
	}

	return count
}

// ReplayFrom reads observations from a specific sequence and applies them.
func (l *ObservationLog) ReplayFrom(fromSequence uint64) (int, error) {
	if l.closed.Load() {
		return 0, ErrObservationLogClosed
	}

	observations, err := l.readAllObservations()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, logged := range observations {
		if logged.Sequence >= fromSequence {
			if l.adaptive != nil {
				l.adaptive.UpdateFromOutcome(logged.Observation)
				count++
			}
		}
	}

	return count, nil
}

// =============================================================================
// Query Methods
// =============================================================================

// CurrentSequence returns the current sequence number.
func (l *ObservationLog) CurrentSequence() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sequence
}

// ObservationCount returns the total number of logged observations.
func (l *ObservationLog) ObservationCount() (int, error) {
	observations, err := l.readAllObservations()
	if err != nil {
		return 0, err
	}
	return len(observations), nil
}

// GetObservations returns all logged observations.
func (l *ObservationLog) GetObservations() ([]LoggedObservation, error) {
	return l.readAllObservations()
}

// =============================================================================
// Truncate Methods
// =============================================================================

// Truncate marks all observations before the given sequence as logically deleted.
// This is an append-only operation that writes a truncate marker, avoiding full file rewrite.
// Use Compact() to reclaim space by physically removing deleted entries.
func (l *ObservationLog) Truncate(beforeSequence uint64) error {
	if l.closed.Load() {
		return ErrObservationLogClosed
	}

	return l.writeTruncateMarker(beforeSequence)
}

func (l *ObservationLog) writeTruncateMarker(beforeSequence uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Count entries being deleted
	deletedCount := l.countEntriesBefore(beforeSequence)

	// Create truncate marker
	marker := TruncateMarker{
		Type:           "truncate",
		BeforeSequence: beforeSequence,
		Timestamp:      time.Now(),
		DeletedCount:   deletedCount,
	}

	// Write marker to WAL
	return l.writeMarkerLocked(marker)
}

func (l *ObservationLog) countEntriesBefore(beforeSequence uint64) int {
	count := 0
	for seq := l.truncateBeforeSeq; seq < beforeSequence && seq <= l.sequence; seq++ {
		count++
	}
	return count
}

func (l *ObservationLog) writeMarkerLocked(marker TruncateMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if _, err := l.writer.Write(data); err != nil {
		return err
	}

	if err := l.writer.Flush(); err != nil {
		return err
	}

	if err := l.file.Sync(); err != nil {
		return err
	}

	// Update in-memory state
	l.truncateBeforeSeq = marker.BeforeSequence
	l.deletedCount += marker.DeletedCount

	return nil
}

// Compact physically removes deleted entries by rewriting the file.
// This should be called periodically when deletedCount exceeds a threshold.
func (l *ObservationLog) Compact() error {
	if l.closed.Load() {
		return ErrObservationLogClosed
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.compactLocked()
}

func (l *ObservationLog) compactLocked() error {
	observations, err := l.readObservationsFiltered(l.truncateBeforeSeq)
	if err != nil {
		return err
	}

	return l.rewriteFileAndResetSequence(observations)
}

// rewriteFileAndResetSequence rewrites the WAL file with remaining observations
// and resets the sequence counter to prevent unbounded growth (W3M.13 fix).
func (l *ObservationLog) rewriteFileAndResetSequence(observations []LoggedObservation) error {
	// Flush and close current file
	l.writer.Flush()
	l.file.Close()

	// Renumber observations starting from 1 to reset sequence counter
	renumbered := l.renumberObservations(observations)

	// Create new file
	file, err := os.Create(l.path)
	if err != nil {
		return err
	}

	writer := bufio.NewWriter(file)

	// Write observations with new sequence numbers
	if err := l.writeObservationsToWriter(writer, renumbered); err != nil {
		file.Close()
		return err
	}

	if err := writer.Flush(); err != nil {
		file.Close()
		return err
	}

	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}

	l.file = file
	l.writer = bufio.NewWriter(file)
	l.deletedCount = 0         // Reset after compaction
	l.truncateBeforeSeq = 0    // Reset truncation point after renumbering
	l.sequence = uint64(len(renumbered)) // Reset sequence to count of remaining observations

	return nil
}

// renumberObservations assigns new sequential sequence numbers starting from 1.
func (l *ObservationLog) renumberObservations(observations []LoggedObservation) []LoggedObservation {
	result := make([]LoggedObservation, len(observations))
	for i, obs := range observations {
		result[i] = LoggedObservation{
			Sequence:    uint64(i + 1),
			Timestamp:   obs.Timestamp,
			Observation: obs.Observation,
			Processed:   obs.Processed,
			Deleted:     false,
		}
	}
	return result
}

func (l *ObservationLog) writeMarkerToWriter(w *bufio.Writer, marker TruncateMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func (l *ObservationLog) writeObservationsToWriter(w *bufio.Writer, observations []LoggedObservation) error {
	for _, obs := range observations {
		data, err := json.Marshal(obs)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// DeletedCount returns the number of logically deleted entries.
// When this exceeds a threshold, Compact() should be called.
func (l *ObservationLog) DeletedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.deletedCount
}

// TruncateSequence returns the sequence number before which entries are deleted.
func (l *ObservationLog) TruncateSequence() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncateBeforeSeq
}

// =============================================================================
// Close Methods
// =============================================================================

// Close shuts down the observation log.
func (l *ObservationLog) Close() error {
	if l.closed.Swap(true) {
		return nil
	}

	// Close write queue using sync.Once for panic-safe close
	l.writeQueueClose.Do(func() {
		close(l.writeQueue)
	})

	// Wait for async writer to finish (it will flush pending writes)
	<-l.writeDone

	// Close observation channel using sync.Once for panic-safe close
	l.obsChannelOnce.Do(func() {
		close(l.obsChannel)
	})

	// Wait for processor to finish
	<-l.closeDone

	// Wait for any inline goroutines that were started without a scope
	l.inlineWg.Wait()

	return l.closeFile()
}

// closeFile performs final flush and file close.
func (l *ObservationLog) closeFile() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Final flush and sync (should be no-op if async writer did its job)
	if err := l.writer.Flush(); err != nil {
		l.file.Close()
		return err
	}

	if err := l.file.Sync(); err != nil {
		l.file.Close()
		return err
	}

	return l.file.Close()
}

// IsClosed returns whether the log is closed.
func (l *ObservationLog) IsClosed() bool {
	return l.closed.Load()
}

// =============================================================================
// File Operations
// =============================================================================

// Size returns the size of the WAL file in bytes.
func (l *ObservationLog) Size() (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	info, err := l.file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// Flush forces a flush of the WAL buffer to disk.
func (l *ObservationLog) Flush() error {
	if l.closed.Load() {
		return ErrObservationLogClosed
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.writer.Flush(); err != nil {
		return err
	}
	return l.file.Sync()
}

// =============================================================================
// Read Observation at Position
// =============================================================================

// ReadAt reads the observation at the given sequence number.
func (l *ObservationLog) ReadAt(sequence uint64) (*LoggedObservation, error) {
	observations, err := l.readAllObservations()
	if err != nil {
		return nil, err
	}

	for _, obs := range observations {
		if obs.Sequence == sequence {
			return &obs, nil
		}
	}

	return nil, io.EOF
}

// =============================================================================
// Sync Health Monitoring (W4N.7)
// =============================================================================

// ConsecutiveSyncFailures returns the current count of consecutive sync failures.
func (l *ObservationLog) ConsecutiveSyncFailures() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.consecutiveSyncFailures
}

// =============================================================================
// Helper Functions
// =============================================================================

// normalizeObsLogger returns a default logger if nil is provided.
func normalizeObsLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}
