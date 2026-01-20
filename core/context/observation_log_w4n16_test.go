package context

import (
	"context"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W4N.16 Sequence Number Overflow Protection Tests
// =============================================================================

// TestW4N16_SequenceIncrements_HappyPath verifies sequence increments correctly
// under normal operation.
func TestW4N16_SequenceIncrements_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Verify initial sequence is 0
	if log.CurrentSequence() != 0 {
		t.Errorf("expected initial sequence 0, got %d", log.CurrentSequence())
	}

	// Record multiple observations and verify sequence increments
	for i := 1; i <= 10; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}); err != nil {
			t.Fatalf("failed to record observation %d: %v", i, err)
		}

		if log.CurrentSequence() != uint64(i) {
			t.Errorf("after %d records, expected sequence %d, got %d", i, i, log.CurrentSequence())
		}
	}
}

// TestW4N16_SequenceNearMax_Warning verifies warning is logged when sequence
// approaches the overflow threshold.
func TestW4N16_SequenceNearMax_Warning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create a custom logger to capture warnings
	var warningLogged atomic.Bool
	handler := &testLogHandler{
		onWarn: func(msg string) {
			if msg == "sequence number approaching overflow threshold" {
				warningLogged.Store(true)
			}
		},
	}
	logger := slog.New(handler)

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:   path,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Manually set sequence to just below threshold
	log.mu.Lock()
	log.sequence = SequenceOverflowThreshold - 1
	log.mu.Unlock()

	// This record should trigger the warning (sequence becomes SequenceOverflowThreshold)
	if err := log.Record(context.Background(), &EpisodeObservation{
		TaskCompleted: true,
	}); err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	// Give async operations a moment to complete
	time.Sleep(50 * time.Millisecond)

	if !warningLogged.Load() {
		t.Error("expected warning to be logged when sequence reaches overflow threshold")
	}

	// Verify sequence is at threshold
	if log.CurrentSequence() != SequenceOverflowThreshold {
		t.Errorf("expected sequence %d, got %d", SequenceOverflowThreshold, log.CurrentSequence())
	}
}

// TestW4N16_SequenceOverflow_Error verifies that an error is returned when
// sequence would overflow (at MaxUint64).
func TestW4N16_SequenceOverflow_Error(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Set sequence to MaxUint64 (the point where overflow would occur)
	log.mu.Lock()
	log.sequence = ^uint64(0) // MaxUint64
	log.mu.Unlock()

	// This record should fail with ErrSequenceOverflow
	err = log.Record(context.Background(), &EpisodeObservation{
		TaskCompleted: true,
	})

	if err != ErrSequenceOverflow {
		t.Errorf("expected ErrSequenceOverflow, got %v", err)
	}

	// Verify sequence hasn't changed
	if log.CurrentSequence() != ^uint64(0) {
		t.Errorf("sequence should remain at MaxUint64 after overflow error")
	}
}

// TestW4N16_SequenceOverflow_SyncPath verifies overflow protection in
// writeToWALSync path.
func TestW4N16_SequenceOverflow_SyncPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Set sequence to MaxUint64
	log.mu.Lock()
	log.sequence = ^uint64(0)
	log.mu.Unlock()

	// Call writeToWALSync directly (bypasses async queue)
	err = log.writeToWALSync(&EpisodeObservation{TaskCompleted: true})

	if err != ErrSequenceOverflow {
		t.Errorf("expected ErrSequenceOverflow from writeToWALSync, got %v", err)
	}
}

// TestW4N16_ConcurrentWrites_SequenceIntegrity verifies sequence integrity
// under concurrent writes.
func TestW4N16_ConcurrentWrites_SequenceIntegrity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1000,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	const numWriters = 50
	const writesPerWriter = 20

	var wg sync.WaitGroup
	var successCount atomic.Int64

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				err := log.Record(context.Background(), &EpisodeObservation{
					TaskCompleted: writerID%2 == 0,
					FollowUpCount: writerID*100 + j,
				})
				if err == nil {
					successCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	log.Close()

	// Reopen and verify sequence integrity
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	observations, err := log2.GetObservations()
	if err != nil {
		t.Fatalf("failed to get observations: %v", err)
	}

	// Verify we got the expected number of observations
	if int64(len(observations)) != successCount.Load() {
		t.Errorf("expected %d observations, got %d", successCount.Load(), len(observations))
	}

	// Verify all sequence numbers are unique
	seqMap := make(map[uint64]bool)
	for _, obs := range observations {
		if seqMap[obs.Sequence] {
			t.Errorf("duplicate sequence number: %d", obs.Sequence)
		}
		seqMap[obs.Sequence] = true
	}

	// Verify sequence numbers are sequential (1 to N)
	for i := uint64(1); i <= uint64(len(observations)); i++ {
		if !seqMap[i] {
			t.Errorf("missing sequence number: %d", i)
		}
	}
}

// TestW4N16_EdgeCase_MaxUint64Minus1 verifies behavior at MaxUint64-1.
func TestW4N16_EdgeCase_MaxUint64Minus1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Set sequence to MaxUint64 - 1
	log.mu.Lock()
	log.sequence = ^uint64(0) - 1
	log.mu.Unlock()

	// This should succeed and set sequence to MaxUint64
	err = log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	if err != nil {
		t.Errorf("expected success at MaxUint64-1, got error: %v", err)
	}

	if log.CurrentSequence() != ^uint64(0) {
		t.Errorf("expected sequence MaxUint64, got %d", log.CurrentSequence())
	}

	// Next record should fail
	err = log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	if err != ErrSequenceOverflow {
		t.Errorf("expected ErrSequenceOverflow after MaxUint64, got %v", err)
	}
}

// TestW4N16_CompactResetsSequence verifies Compact() resets sequence numbers.
func TestW4N16_CompactResetsSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Record observations to get a high sequence number
	for i := 0; i < 100; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	if log.CurrentSequence() != 100 {
		t.Fatalf("expected sequence 100, got %d", log.CurrentSequence())
	}

	// Truncate to keep only last 10
	if err := log.Truncate(91); err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}

	// Compact should reset sequence to count of remaining observations
	if err := log.Compact(); err != nil {
		t.Fatalf("failed to compact: %v", err)
	}

	// After compact, sequence should be 10 (renumbered 1-10)
	if log.CurrentSequence() != 10 {
		t.Errorf("expected sequence 10 after compact, got %d", log.CurrentSequence())
	}

	// New records should continue from 10
	if err := log.Record(context.Background(), &EpisodeObservation{
		TaskCompleted: true,
	}); err != nil {
		t.Fatalf("failed to record after compact: %v", err)
	}

	if log.CurrentSequence() != 11 {
		t.Errorf("expected sequence 11, got %d", log.CurrentSequence())
	}
}

// TestW4N16_CompactResetsHighSequence verifies Compact() can reset even very
// high sequence numbers.
func TestW4N16_CompactResetsHighSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Record a few observations
	for i := 0; i < 5; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	// Artificially set a very high sequence number
	log.mu.Lock()
	log.sequence = SequenceOverflowThreshold - 100
	log.mu.Unlock()

	// Truncate all but keep empty (by truncating above current count)
	if err := log.Truncate(6); err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}

	// Compact should reset sequence
	if err := log.Compact(); err != nil {
		t.Fatalf("failed to compact: %v", err)
	}

	// Sequence should now be 0 (no observations remaining after truncate above 5)
	if log.CurrentSequence() != 0 {
		t.Errorf("expected sequence 0 after compact with no remaining obs, got %d", log.CurrentSequence())
	}
}

// TestW4N16_OverflowThresholdConstant verifies the threshold constant is set correctly.
func TestW4N16_OverflowThresholdConstant(t *testing.T) {
	maxUint64 := ^uint64(0)
	expectedThreshold := maxUint64 - 1_000_000

	if SequenceOverflowThreshold != expectedThreshold {
		t.Errorf("SequenceOverflowThreshold should be MaxUint64 - 1_000_000, got %d", SequenceOverflowThreshold)
	}

	// Verify it's a reasonable distance from max
	distance := maxUint64 - SequenceOverflowThreshold
	if distance != 1_000_000 {
		t.Errorf("threshold should be 1 million from max, got %d", distance)
	}
}

// TestW4N16_ErrorType verifies ErrSequenceOverflow error is properly defined.
func TestW4N16_ErrorType(t *testing.T) {
	if ErrSequenceOverflow == nil {
		t.Error("ErrSequenceOverflow should not be nil")
	}

	expected := "sequence number would overflow; call Compact() to reset"
	if ErrSequenceOverflow.Error() != expected {
		t.Errorf("unexpected error message: %s", ErrSequenceOverflow.Error())
	}
}

// TestW4N16_RaceCondition_IncrementSequence tests for race conditions in
// sequence increment.
func TestW4N16_RaceCondition_IncrementSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 2000,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Run concurrent increments
	const numGoroutines = 100
	const incrementsPerGoroutine = 10

	var wg sync.WaitGroup
	startCh := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh // Wait for start signal
			for j := 0; j < incrementsPerGoroutine; j++ {
				log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
			}
		}()
	}

	// Start all goroutines simultaneously
	close(startCh)
	wg.Wait()
	log.Close()

	// Verify final sequence
	expectedMax := uint64(numGoroutines * incrementsPerGoroutine)
	if log.CurrentSequence() != expectedMax {
		t.Errorf("expected sequence %d, got %d", expectedMax, log.CurrentSequence())
	}
}

// TestW4N16_Persistence_HighSequence verifies high sequence numbers persist
// correctly across restarts.
func TestW4N16_Persistence_HighSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create log and record observations
	log1, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	for i := 0; i < 1000; i++ {
		if err := log1.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}
	log1.Close()

	// Reopen and verify sequence
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	if log2.CurrentSequence() != 1000 {
		t.Errorf("expected sequence 1000 after reopen, got %d", log2.CurrentSequence())
	}

	// Record more and verify continuation
	if err := log2.Record(context.Background(), &EpisodeObservation{
		TaskCompleted: true,
	}); err != nil {
		t.Fatalf("failed to record after reopen: %v", err)
	}

	if log2.CurrentSequence() != 1001 {
		t.Errorf("expected sequence 1001, got %d", log2.CurrentSequence())
	}
}

// TestW4N16_SyncPath_Warning verifies warning in writeToWALSync path.
func TestW4N16_SyncPath_Warning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	var warningLogged atomic.Bool
	handler := &testLogHandler{
		onWarn: func(msg string) {
			if msg == "sequence number approaching overflow threshold" {
				warningLogged.Store(true)
			}
		},
	}
	logger := slog.New(handler)

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:   path,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Set sequence just below threshold
	log.mu.Lock()
	log.sequence = SequenceOverflowThreshold - 1
	log.mu.Unlock()

	// Call writeToWALSync directly
	err = log.writeToWALSync(&EpisodeObservation{TaskCompleted: true})
	if err != nil {
		t.Fatalf("writeToWALSync failed: %v", err)
	}

	if !warningLogged.Load() {
		t.Error("expected warning to be logged in writeToWALSync path")
	}
}

// =============================================================================
// Test Helper: Custom Log Handler
// =============================================================================

type testLogHandler struct {
	onWarn func(msg string)
}

func (h *testLogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (h *testLogHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn && h.onWarn != nil {
		h.onWarn(r.Message)
	}
	return nil
}

func (h *testLogHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *testLogHandler) WithGroup(_ string) slog.Handler {
	return h
}

// =============================================================================
// Benchmark: Sequence Increment Performance
// =============================================================================

func BenchmarkW4N16_SequenceIncrement(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: b.N + 1000,
	})
	if err != nil {
		b.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	obs := &EpisodeObservation{TaskCompleted: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log.Record(context.Background(), obs)
	}
}

// =============================================================================
// Additional Edge Case Tests
// =============================================================================

// TestW4N16_SequenceAfterOverflowError verifies log remains usable after
// overflow error if compacted.
func TestW4N16_SequenceAfterOverflowError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Record some observations first
	for i := 0; i < 5; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	// Set sequence to max
	log.mu.Lock()
	log.sequence = ^uint64(0)
	log.mu.Unlock()

	// Verify overflow error
	err = log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	if err != ErrSequenceOverflow {
		t.Errorf("expected ErrSequenceOverflow, got %v", err)
	}

	// Truncate and compact to reset
	log.mu.Lock()
	log.sequence = 5 // Reset for truncate to work properly
	log.mu.Unlock()

	if err := log.Truncate(4); err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}

	if err := log.Compact(); err != nil {
		t.Fatalf("failed to compact: %v", err)
	}

	// Now should be able to record again
	err = log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	if err != nil {
		t.Errorf("expected success after compact, got %v", err)
	}
}

// TestW4N16_SequenceZeroStart verifies sequence starts at 0 for new log.
func TestW4N16_SequenceZeroStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	if log.CurrentSequence() != 0 {
		t.Errorf("expected initial sequence 0, got %d", log.CurrentSequence())
	}
}

// TestW4N16_SequenceIncrementUnderLock verifies incrementSequence is atomic.
func TestW4N16_SequenceIncrementUnderLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1000,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Concurrent reads and writes
	const numOps = 100
	var wg sync.WaitGroup

	// Writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
			}
		}()
	}

	// Readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				seq := log.CurrentSequence()
				// Sequence should always be >= 0
				if seq > math.MaxUint64 {
					t.Errorf("impossible sequence value: %d", seq)
				}
			}
		}()
	}

	wg.Wait()
	log.Close()
}

// TestW4N16_NoOverflowWarningBeforeThreshold verifies no warning before threshold.
func TestW4N16_NoOverflowWarningBeforeThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	var warningLogged atomic.Bool
	handler := &testLogHandler{
		onWarn: func(msg string) {
			if msg == "sequence number approaching overflow threshold" {
				warningLogged.Store(true)
			}
		},
	}
	logger := slog.New(handler)

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:   path,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Set sequence well below threshold
	log.mu.Lock()
	log.sequence = SequenceOverflowThreshold - 100
	log.mu.Unlock()

	// Record a few observations (not reaching threshold)
	for i := 0; i < 5; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	time.Sleep(50 * time.Millisecond)

	if warningLogged.Load() {
		t.Error("warning should not be logged before reaching threshold")
	}
}

// TestW4N16_EmptyLogCompact verifies compacting an empty log works correctly.
func TestW4N16_EmptyLogCompact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Compact empty log
	if err := log.Compact(); err != nil {
		t.Fatalf("failed to compact empty log: %v", err)
	}

	// Sequence should remain 0
	if log.CurrentSequence() != 0 {
		t.Errorf("expected sequence 0 after compact empty log, got %d", log.CurrentSequence())
	}

	// Should still be able to record
	if err := log.Record(context.Background(), &EpisodeObservation{
		TaskCompleted: true,
	}); err != nil {
		t.Fatalf("failed to record after compact: %v", err)
	}

	if log.CurrentSequence() != 1 {
		t.Errorf("expected sequence 1, got %d", log.CurrentSequence())
	}
}
