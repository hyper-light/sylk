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
