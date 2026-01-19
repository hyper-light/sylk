// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.4.2: ObservationLog - crash-safe WAL for EpisodeObservations.
package context

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
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
)

// =============================================================================
// Logged Observation
// =============================================================================

// LoggedObservation wraps an observation with WAL metadata.
type LoggedObservation struct {
	Sequence    uint64            `json:"sequence"`
	Timestamp   time.Time         `json:"timestamp"`
	Observation EpisodeObservation `json:"observation"`
	Processed   bool              `json:"processed"`
}

// =============================================================================
// Observation Log
// =============================================================================

// ObservationLog provides crash-safe logging of EpisodeObservations.
// Observations are written synchronously to disk, then queued for async processing.
type ObservationLog struct {
	mu sync.Mutex

	path     string
	file     *os.File
	writer   *bufio.Writer
	sequence uint64

	obsChannel chan *EpisodeObservation
	closed     atomic.Bool
	closeDone  chan struct{}

	adaptive *AdaptiveState
	scope    *concurrency.GoroutineScope
}

// ObservationLogConfig holds configuration for the observation log.
type ObservationLogConfig struct {
	Path         string
	BufferSize   int
	Adaptive     *AdaptiveState
	Scope        *concurrency.GoroutineScope
}

// NewObservationLog creates a new observation log.
func NewObservationLog(ctx context.Context, config ObservationLogConfig) (*ObservationLog, error) {
	if config.BufferSize <= 0 {
		config.BufferSize = DefaultObservationBufferSize
	}

	file, err := openWALFile(config.Path)
	if err != nil {
		return nil, err
	}

	log := &ObservationLog{
		path:       config.Path,
		file:       file,
		writer:     bufio.NewWriter(file),
		obsChannel: make(chan *EpisodeObservation, config.BufferSize),
		closeDone:  make(chan struct{}),
		adaptive:   config.Adaptive,
		scope:      config.Scope,
	}

	if err := log.loadSequence(); err != nil {
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
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
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

	for scanner.Scan() {
		var logged LoggedObservation
		if err := json.Unmarshal(scanner.Bytes(), &logged); err != nil {
			continue
		}
		if logged.Sequence > lastSeq {
			lastSeq = logged.Sequence
		}
	}

	l.sequence = lastSeq

	// Seek to end for appending
	_, err := l.file.Seek(0, 2)
	return err
}

func (l *ObservationLog) startProcessor(ctx context.Context) error {
	if l.scope == nil {
		// No scope provided, start inline
		go l.processLoop(ctx)
		return nil
	}

	return l.scope.Go("observation-log-processor", 0, func(innerCtx context.Context) error {
		l.processLoop(innerCtx)
		return nil
	})
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
	defer l.mu.Unlock()

	// Read from start
	file, err := os.Open(l.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var observations []LoggedObservation
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		var logged LoggedObservation
		if err := json.Unmarshal(scanner.Bytes(), &logged); err != nil {
			continue
		}
		observations = append(observations, logged)
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

// Truncate removes all observations before the given sequence.
func (l *ObservationLog) Truncate(beforeSequence uint64) error {
	if l.closed.Load() {
		return ErrObservationLogClosed
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.truncateLocked(beforeSequence)
}

func (l *ObservationLog) truncateLocked(beforeSequence uint64) error {
	// Read all observations
	file, err := os.Open(l.path)
	if err != nil {
		return err
	}

	var keep []LoggedObservation
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		var logged LoggedObservation
		if err := json.Unmarshal(scanner.Bytes(), &logged); err != nil {
			continue
		}
		if logged.Sequence >= beforeSequence {
			keep = append(keep, logged)
		}
	}
	file.Close()

	// Rewrite file with kept observations
	return l.rewriteFile(keep)
}

func (l *ObservationLog) rewriteFile(observations []LoggedObservation) error {
	// Close current file
	l.writer.Flush()
	l.file.Close()

	// Create new file
	file, err := os.Create(l.path)
	if err != nil {
		return err
	}

	writer := bufio.NewWriter(file)

	for _, obs := range observations {
		data, err := json.Marshal(obs)
		if err != nil {
			file.Close()
			return err
		}
		data = append(data, '\n')
		if _, err := writer.Write(data); err != nil {
			file.Close()
			return err
		}
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

	return nil
}

// =============================================================================
// Close Methods
// =============================================================================

// Close shuts down the observation log.
func (l *ObservationLog) Close() error {
	if l.closed.Swap(true) {
		return nil
	}

	// Close channel to stop processor
	close(l.obsChannel)

	// Wait for processor to finish
	<-l.closeDone

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.writer.Flush(); err != nil {
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
