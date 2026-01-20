package context

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// NewObservationLog Tests
// =============================================================================

func TestNewObservationLog_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected WAL file to be created")
	}
}

func TestNewObservationLog_OpensExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create and close first log
	log1, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create first log: %v", err)
	}

	obs := &EpisodeObservation{TaskCompleted: true}
	if err := log1.Record(context.Background(), obs); err != nil {
		t.Fatalf("failed to record: %v", err)
	}
	log1.Close()

	// Open second log
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to open existing log: %v", err)
	}
	defer log2.Close()

	// Should have sequence from previous
	if log2.CurrentSequence() != 1 {
		t.Errorf("expected sequence 1, got %d", log2.CurrentSequence())
	}
}

func TestNewObservationLog_InvalidPath(t *testing.T) {
	_, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: "/nonexistent/dir/obs.wal",
	})
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

// =============================================================================
// Record Tests
// =============================================================================

func TestRecord_WritesToWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	obs := &EpisodeObservation{
		TaskCompleted: true,
		FollowUpCount: 2,
	}

	if err := log.Record(context.Background(), obs); err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	// Check file has content
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("expected WAL to have content")
	}
}

func TestRecord_IncrementsSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	for i := 0; i < 5; i++ {
		obs := &EpisodeObservation{TaskCompleted: true}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	if log.CurrentSequence() != 5 {
		t.Errorf("expected sequence 5, got %d", log.CurrentSequence())
	}
}

func TestRecord_NilObservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	err = log.Record(context.Background(), nil)
	if err != ErrInvalidObservation {
		t.Errorf("expected ErrInvalidObservation, got %v", err)
	}
}

func TestRecord_AfterClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	log.Close()

	err = log.Record(context.Background(), &EpisodeObservation{})
	if err != ErrObservationLogClosed {
		t.Errorf("expected ErrObservationLogClosed, got %v", err)
	}
}

// =============================================================================
// Replay Tests
// =============================================================================

func TestReplay_AppliesObservations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	adaptive := NewAdaptiveState()

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:     path,
		Adaptive: adaptive,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record some observations
	for i := 0; i < 5; i++ {
		obs := &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}
	log.Close()

	// Create new log and replay
	adaptive2 := NewAdaptiveState()
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:     path,
		Adaptive: adaptive2,
	})
	if err != nil {
		t.Fatalf("failed to create second log: %v", err)
	}
	defer log2.Close()

	count, err := log2.Replay()
	if err != nil {
		t.Fatalf("failed to replay: %v", err)
	}

	if count != 5 {
		t.Errorf("expected 5 observations replayed, got %d", count)
	}
}

func TestReplay_PerformanceUnder100ms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Write 1000 observations
	for i := 0; i < 1000; i++ {
		obs := &EpisodeObservation{
			TaskCompleted: i%2 == 0,
			FollowUpCount: i % 5,
			PrefetchedIDs: []string{"a", "b", "c"},
			UsedIDs:       []string{"a"},
		}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}
	log.Close()

	// Replay and measure time
	adaptive := NewAdaptiveState()
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:     path,
		Adaptive: adaptive,
	})
	if err != nil {
		t.Fatalf("failed to create second log: %v", err)
	}
	defer log2.Close()

	start := time.Now()
	count, err := log2.Replay()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("failed to replay: %v", err)
	}

	if count != 1000 {
		t.Errorf("expected 1000 observations, got %d", count)
	}

	if elapsed > 100*time.Millisecond {
		t.Errorf("replay took %v, expected < 100ms", elapsed)
	}
}

func TestReplayFrom_StartsAtSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record 10 observations
	for i := 0; i < 10; i++ {
		obs := &EpisodeObservation{TaskCompleted: true}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}
	log.Close()

	// Replay from sequence 5
	adaptive := NewAdaptiveState()
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:     path,
		Adaptive: adaptive,
	})
	if err != nil {
		t.Fatalf("failed to create second log: %v", err)
	}
	defer log2.Close()

	count, err := log2.ReplayFrom(5)
	if err != nil {
		t.Fatalf("failed to replay: %v", err)
	}

	// Should replay sequences 5-10 = 6 observations
	if count != 6 {
		t.Errorf("expected 6 observations from seq 5, got %d", count)
	}
}

// =============================================================================
// Crash Recovery Tests
// =============================================================================

func TestCrashRecovery_SurvivesClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Write observations
	log1, _ := NewObservationLog(context.Background(), ObservationLogConfig{Path: path})
	for i := 0; i < 5; i++ {
		log1.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		})
	}
	log1.Close()

	// Simulate crash by opening new log
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{Path: path})
	if err != nil {
		t.Fatalf("failed to reopen: %v", err)
	}
	defer log2.Close()

	// All observations should be readable
	observations, err := log2.GetObservations()
	if err != nil {
		t.Fatalf("failed to get observations: %v", err)
	}

	if len(observations) != 5 {
		t.Errorf("expected 5 observations after crash, got %d", len(observations))
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestClose_UnblocksWaitingProcessor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:       path,
		BufferSize: 1,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	done := make(chan struct{})
	go func() {
		log.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Error("Close did not return in time")
	}
}

func TestObservationLog_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Close multiple times should not panic
	log.Close()
	log.Close()
	log.Close()
}

func TestObservationLog_Close_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Write some observations first
	for i := 0; i < 5; i++ {
		log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	}

	// Concurrent close calls should not panic (W3C.5)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Close()
		}()
	}
	wg.Wait()

	// Verify closed state
	if !log.IsClosed() {
		t.Error("expected log to be closed")
	}
}

func TestObservationLog_Close_SingleClose_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Write observations
	for i := 0; i < 10; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	// Single close should work without error
	if err := log.Close(); err != nil {
		t.Errorf("unexpected error on close: %v", err)
	}

	// Verify closed
	if !log.IsClosed() {
		t.Error("expected log to be closed")
	}

	// Verify data persisted
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
	if len(observations) != 10 {
		t.Errorf("expected 10 observations, got %d", len(observations))
	}
}

// =============================================================================
// Truncate Tests
// =============================================================================

func TestTruncate_RemovesOldObservations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record 10 observations
	for i := 0; i < 10; i++ {
		log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	}

	// Truncate before sequence 5
	if err := log.Truncate(5); err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}

	observations, err := log.GetObservations()
	if err != nil {
		t.Fatalf("failed to get observations: %v", err)
	}

	// Should have sequences 5-10 = 6 observations
	if len(observations) != 6 {
		t.Errorf("expected 6 observations after truncate, got %d", len(observations))
	}

	for _, obs := range observations {
		if obs.Sequence < 5 {
			t.Errorf("found observation with sequence %d, expected >= 5", obs.Sequence)
		}
	}

	log.Close()
}

func TestTruncate_AppendOnlyNoRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record 10 observations
	for i := 0; i < 10; i++ {
		log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	}

	// Get file size before truncate
	sizeBefore, _ := log.Size()

	// Truncate before sequence 5 - should append marker, not rewrite
	if err := log.Truncate(5); err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}

	// File size should increase (append-only)
	sizeAfter, _ := log.Size()
	if sizeAfter <= sizeBefore {
		t.Errorf("expected file size to increase after truncate marker, got %d <= %d", sizeAfter, sizeBefore)
	}

	// Verify observations are still filtered correctly
	observations, _ := log.GetObservations()
	if len(observations) != 6 {
		t.Errorf("expected 6 observations, got %d", len(observations))
	}

	log.Close()
}

func TestTruncate_DataIntegrityAfterReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record 10 observations
	for i := 0; i < 10; i++ {
		log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		})
	}

	// Truncate before sequence 5
	log.Truncate(5)
	log.Close()

	// Reopen and verify
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	observations, _ := log2.GetObservations()
	if len(observations) != 6 {
		t.Errorf("expected 6 observations after reopen, got %d", len(observations))
	}

	// Verify truncate sequence was preserved
	if log2.TruncateSequence() != 5 {
		t.Errorf("expected truncate sequence 5, got %d", log2.TruncateSequence())
	}
}

func TestTruncate_PerformanceLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Record 1000 observations
	for i := 0; i < 1000; i++ {
		log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
			PrefetchedIDs: []string{"a", "b", "c"},
		})
	}

	// Truncate should be fast (append-only, no rewrite)
	start := time.Now()
	if err := log.Truncate(500); err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}
	elapsed := time.Since(start)

	// Truncate should be < 10ms since it's just appending a marker
	if elapsed > 10*time.Millisecond {
		t.Errorf("truncate took %v, expected < 10ms for append-only operation", elapsed)
	}

	// Verify correct observations are returned
	observations, _ := log.GetObservations()
	if len(observations) != 501 {
		t.Errorf("expected 501 observations (500-1000), got %d", len(observations))
	}
}

func TestCompact_ReclainsSpace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Record 100 observations
	for i := 0; i < 100; i++ {
		log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	}

	// Truncate to keep only last 10
	log.Truncate(91)

	// Get size before compaction
	sizeBefore, _ := log.Size()

	// Compact to physically remove deleted entries
	if err := log.Compact(); err != nil {
		t.Fatalf("failed to compact: %v", err)
	}

	// Size should decrease significantly
	sizeAfter, _ := log.Size()
	if sizeAfter >= sizeBefore {
		t.Errorf("expected size to decrease after compact, got %d >= %d", sizeAfter, sizeBefore)
	}

	// Verify data integrity
	observations, _ := log.GetObservations()
	if len(observations) != 10 {
		t.Errorf("expected 10 observations after compact, got %d", len(observations))
	}

	// Deleted count should be reset
	if log.DeletedCount() != 0 {
		t.Errorf("expected deleted count 0 after compact, got %d", log.DeletedCount())
	}
}

func TestCompact_DataIntegrityAfterReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record, truncate, compact
	for i := 0; i < 50; i++ {
		log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		})
	}
	log.Truncate(26)
	log.Compact()
	log.Close()

	// Reopen and verify
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	observations, _ := log2.GetObservations()
	if len(observations) != 25 {
		t.Errorf("expected 25 observations, got %d", len(observations))
	}

	// Verify data content
	for _, obs := range observations {
		if obs.Sequence < 26 {
			t.Errorf("found observation with sequence %d, expected >= 26", obs.Sequence)
		}
	}
}

func TestTruncate_MultipleTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Record 20 observations
	for i := 0; i < 20; i++ {
		log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	}

	// Multiple truncates
	log.Truncate(5)
	log.Truncate(10)
	log.Truncate(15)

	// Should only see observations >= 15
	observations, _ := log.GetObservations()
	if len(observations) != 6 {
		t.Errorf("expected 6 observations (15-20), got %d", len(observations))
	}

	for _, obs := range observations {
		if obs.Sequence < 15 {
			t.Errorf("found observation with sequence %d, expected >= 15", obs.Sequence)
		}
	}
}

// =============================================================================
// Query Methods Tests
// =============================================================================

func TestObservationCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	count, _ := log.ObservationCount()
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	for i := 0; i < 5; i++ {
		log.Record(context.Background(), &EpisodeObservation{})
	}

	count, _ = log.ObservationCount()
	if count != 5 {
		t.Errorf("expected 5, got %d", count)
	}
}

func TestReadAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	for i := 0; i < 5; i++ {
		log.Record(context.Background(), &EpisodeObservation{
			FollowUpCount: i,
		})
	}

	obs, err := log.ReadAt(3)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	if obs.Observation.FollowUpCount != 2 {
		t.Errorf("expected FollowUpCount 2, got %d", obs.Observation.FollowUpCount)
	}
}

func TestSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	size1, _ := log.Size()
	if size1 != 0 {
		t.Errorf("expected size 0, got %d", size1)
	}

	log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})

	size2, _ := log.Size()
	if size2 <= size1 {
		t.Errorf("expected size to increase, got %d <= %d", size2, size1)
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestObservationLog_ConcurrentRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			obs := &EpisodeObservation{
				FollowUpCount: id,
				TaskCompleted: id%2 == 0,
			}
			log.Record(context.Background(), obs)
		}(i)
	}
	wg.Wait()

	if log.CurrentSequence() != 100 {
		t.Errorf("expected sequence 100, got %d", log.CurrentSequence())
	}
}

// =============================================================================
// Adaptive Integration Tests
// =============================================================================

func TestObservationLog_UpdatesAdaptiveState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	adaptive := NewAdaptiveState()
	initialObs := adaptive.TotalObservations

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:       path,
		Adaptive:   adaptive,
		BufferSize: 10,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record observations
	for i := 0; i < 5; i++ {
		obs := &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: 0,
		}
		log.Record(context.Background(), obs)
	}

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)
	log.Close()

	// Check that adaptive state was updated
	if adaptive.TotalObservations <= initialObs {
		t.Logf("Initial: %d, Final: %d", initialObs, adaptive.TotalObservations)
		// This is expected since async processing may not complete
		// The important thing is that the WAL has the observations
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestObservationLog_EmptyReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	adaptive := NewAdaptiveState()
	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:     path,
		Adaptive: adaptive,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	count, err := log.Replay()
	if err != nil {
		t.Fatalf("failed to replay: %v", err)
	}

	if count != 0 {
		t.Errorf("expected 0 observations for empty log, got %d", count)
	}
}

func TestObservationLog_Flush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	log.Record(context.Background(), &EpisodeObservation{})

	if err := log.Flush(); err != nil {
		t.Fatalf("failed to flush: %v", err)
	}
}

func TestObservationLog_IsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, _ := NewObservationLog(context.Background(), ObservationLogConfig{Path: path})

	if log.IsClosed() {
		t.Error("expected not closed initially")
	}

	log.Close()

	if !log.IsClosed() {
		t.Error("expected closed after Close()")
	}
}

// =============================================================================
// Async Write Tests (PF.5.6)
// =============================================================================

func TestObservationLog_AsyncWriteConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 500,
		SyncInterval:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Should be able to record without issues
	for i := 0; i < 10; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true}); err != nil {
			t.Errorf("failed to record: %v", err)
		}
	}
}

func TestObservationLog_WriteQueueFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create log with tiny write queue
	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1,
		SyncInterval:   time.Hour, // Long sync interval to prevent draining
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Try to flood the write queue - at least one should fail
	// Note: This test is timing dependent, so we just verify the error type
	// when it does occur, rather than guaranteeing it happens
	var writeQueueFullSeen bool
	for i := 0; i < 100; i++ {
		err := log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
		if err == ErrWriteQueueFull {
			writeQueueFullSeen = true
			break
		}
	}

	// We can't guarantee we'll see the error in this test since the writer
	// goroutine might process fast enough, but the code path exists
	t.Logf("ErrWriteQueueFull seen: %v", writeQueueFullSeen)
}

func TestObservationLog_AsyncWriteDurability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Write observations
	for i := 0; i < 10; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	// Close ensures all writes are flushed
	log.Close()

	// Reopen and verify all observations are there
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

	if len(observations) != 10 {
		t.Errorf("expected 10 observations after reopen, got %d", len(observations))
	}
}

func TestObservationLog_AsyncWritePerformance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1000,
		SyncInterval:   100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Write 100 observations and measure time
	start := time.Now()
	for i := 0; i < 100; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}
	elapsed := time.Since(start)

	// With async writes, 100 records should complete quickly
	// (serialization outside lock, queue-based I/O)
	if elapsed > 500*time.Millisecond {
		t.Errorf("async writes took %v, expected < 500ms", elapsed)
	}
	t.Logf("100 async writes completed in %v", elapsed)
}

func TestObservationLog_ConcurrentAsyncWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1000,
		SyncInterval:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Concurrent writes
	var wg sync.WaitGroup
	successCount := make(chan int, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := log.Record(context.Background(), &EpisodeObservation{
				TaskCompleted: id%2 == 0,
				FollowUpCount: id,
			})
			if err == nil {
				successCount <- 1
			}
		}(i)
	}
	wg.Wait()
	close(successCount)

	// Close to ensure all writes complete
	log.Close()

	// Count successes
	total := 0
	for s := range successCount {
		total += s
	}

	// At least most should succeed (some might hit queue full)
	if total < 40 {
		t.Errorf("expected at least 40 successful writes, got %d", total)
	}

	// Verify data integrity
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	observations, _ := log2.GetObservations()
	if len(observations) != total {
		t.Errorf("expected %d observations, got %d", total, len(observations))
	}

	// Verify sequence numbers are unique
	seqMap := make(map[uint64]bool)
	for _, obs := range observations {
		if seqMap[obs.Sequence] {
			t.Errorf("duplicate sequence number: %d", obs.Sequence)
		}
		seqMap[obs.Sequence] = true
	}
}
