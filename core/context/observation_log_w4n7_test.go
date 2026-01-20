package context

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W4N.7 Tests - Sync Error Handling in periodicSync
// =============================================================================

// syncWriter is a thread-safe io.Writer for capturing log output in tests.
type syncWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (sw *syncWriter) Write(p []byte) (n int, err error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.buf.Write(p)
}

// TestW4N7_PeriodicSync_HappyPath verifies that when sync succeeds,
// no errors are logged and failure count stays at zero.
func TestW4N7_PeriodicSync_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create a thread-safe buffer to capture log output
	var logMu sync.Mutex
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, buf: &logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 10 * time.Millisecond,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record some data to ensure there's something to sync
	for i := 0; i < 5; i++ {
		if err := log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true}); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	// Wait for periodic sync to run
	time.Sleep(50 * time.Millisecond)

	// Capture failures before close
	failures := log.ConsecutiveSyncFailures()

	// Close to ensure async writer stops before checking log buffer
	log.Close()

	// Verify no sync errors were logged (safe to read now that writer is stopped)
	logMu.Lock()
	logOutput := logBuf.String()
	logMu.Unlock()

	if bytes.Contains([]byte(logOutput), []byte("WAL periodic sync failed")) {
		t.Errorf("unexpected sync error logged: %s", logOutput)
	}

	// Verify consecutive failures is zero
	if failures != 0 {
		t.Errorf("expected 0 consecutive failures, got %d", failures)
	}
}

// TestW4N7_PeriodicSync_SyncFailsErrorLogged verifies that when sync fails,
// the error is properly logged with appropriate details.
func TestW4N7_PeriodicSync_SyncFailsErrorLogged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create a thread-safe buffer to capture log output
	var logMu sync.Mutex
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, buf: &logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 10 * time.Millisecond,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record some data
	if err := log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true}); err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	// Close the underlying file to force sync errors
	// We need to access the file through the lock
	log.mu.Lock()
	log.file.Close()
	log.mu.Unlock()

	// Wait for periodic sync to run and fail
	time.Sleep(50 * time.Millisecond)

	// Capture failures before close
	failures := log.ConsecutiveSyncFailures()

	// Restore file for cleanup (create a new one since original is closed)
	log.mu.Lock()
	newFile, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if newFile != nil {
		log.file = newFile
	}
	log.mu.Unlock()

	log.Close()

	// Now safely read the log buffer after async writer stopped
	logMu.Lock()
	logOutput := logBuf.String()
	logMu.Unlock()

	// Verify sync error was logged
	if !bytes.Contains([]byte(logOutput), []byte("WAL periodic sync failed")) {
		t.Errorf("expected sync error to be logged, got: %s", logOutput)
	}

	// Verify the path is included in the log
	if !bytes.Contains([]byte(logOutput), []byte(path)) {
		t.Errorf("expected path in log output, got: %s", logOutput)
	}

	// Verify consecutive failures increased
	if failures == 0 {
		t.Error("expected consecutive failures > 0")
	}
}

// TestW4N7_PeriodicSync_ConsecutiveFailuresCallback verifies that the health
// callback is triggered when consecutive sync failures reach the threshold.
func TestW4N7_PeriodicSync_ConsecutiveFailuresCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	var callbackCalled atomic.Bool
	var callbackFailures atomic.Int32
	var callbackError atomic.Value

	callback := func(consecutiveFailures int, lastError error) {
		callbackCalled.Store(true)
		callbackFailures.Store(int32(consecutiveFailures))
		callbackError.Store(lastError)
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:                 path,
		SyncInterval:         5 * time.Millisecond,
		SyncFailureThreshold: 2, // Low threshold for testing
		SyncHealthCallback:   callback,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record some data
	if err := log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true}); err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	// Close the underlying file to force sync errors
	log.mu.Lock()
	log.file.Close()
	log.mu.Unlock()

	// Wait for multiple periodic syncs to fail
	time.Sleep(100 * time.Millisecond)

	// Verify callback was called
	if !callbackCalled.Load() {
		t.Error("expected health callback to be called")
	}

	// Verify failures count in callback
	if failures := callbackFailures.Load(); failures < 2 {
		t.Errorf("expected at least 2 failures in callback, got %d", failures)
	}

	// Verify error was passed to callback
	if callbackError.Load() == nil {
		t.Error("expected error to be passed to callback")
	}

	// Restore file for cleanup
	log.mu.Lock()
	newFile, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if newFile != nil {
		log.file = newFile
	}
	log.mu.Unlock()

	log.Close()
}

// TestW4N7_PeriodicSync_FailuresResetOnSuccess verifies that consecutive
// failure count resets to zero after a successful sync.
func TestW4N7_PeriodicSync_FailuresResetOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Manually set some failures
	log.mu.Lock()
	log.consecutiveSyncFailures = 5
	log.mu.Unlock()

	// Force a successful sync by calling Flush (which syncs)
	if err := log.Flush(); err != nil {
		t.Fatalf("failed to flush: %v", err)
	}

	// Wait for periodic sync
	time.Sleep(50 * time.Millisecond)

	// Verify failures were reset
	if failures := log.ConsecutiveSyncFailures(); failures != 0 {
		t.Errorf("expected failures to reset to 0, got %d", failures)
	}
}

// TestW4N7_PeriodicSync_ConcurrentAccess verifies that periodic sync handles
// concurrent access correctly without race conditions.
func TestW4N7_PeriodicSync_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 1 * time.Millisecond, // Very short interval
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Run concurrent operations
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					log.Record(context.Background(), &EpisodeObservation{
						TaskCompleted: id%2 == 0,
						FollowUpCount: id,
					})
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	// Concurrent readers of sync status
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_ = log.ConsecutiveSyncFailures()
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	wg.Wait()
	log.Close()

	// If we get here without deadlock or panic, the test passes
}

// TestW4N7_PeriodicSync_NilFile verifies that periodicSync handles nil file gracefully.
func TestW4N7_PeriodicSync_NilFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Simulate nil file scenario (would be unusual but should be handled)
	log.mu.Lock()
	originalFile := log.file
	log.file = nil
	log.mu.Unlock()

	// Call periodicSync directly - should not panic
	log.periodicSync()

	// Restore file
	log.mu.Lock()
	log.file = originalFile
	log.mu.Unlock()

	log.Close()
}

// TestW4N7_PeriodicSync_DuringClose verifies that sync operations during close
// are handled gracefully without race conditions.
func TestW4N7_PeriodicSync_DuringClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record some data
	for i := 0; i < 10; i++ {
		log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	}

	// Close while periodic syncs might be happening
	done := make(chan struct{})
	go func() {
		log.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success - close completed
	case <-time.After(5 * time.Second):
		t.Error("Close did not complete in time - possible deadlock")
	}
}

// TestW4N7_PeriodicSync_CallbackNotCalledBelowThreshold verifies that the
// health callback is NOT called when failures are below threshold.
func TestW4N7_PeriodicSync_CallbackNotCalledBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	var callbackCalled atomic.Bool
	callback := func(consecutiveFailures int, lastError error) {
		callbackCalled.Store(true)
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:                 path,
		SyncInterval:         10 * time.Millisecond,
		SyncFailureThreshold: 100, // High threshold
		SyncHealthCallback:   callback,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record some data
	log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})

	// Close file to cause failure
	log.mu.Lock()
	log.file.Close()
	log.mu.Unlock()

	// Wait for a few syncs to fail (but not reach threshold)
	time.Sleep(50 * time.Millisecond)

	// Verify callback was NOT called (threshold is 100)
	if callbackCalled.Load() {
		t.Error("callback should not be called when below threshold")
	}

	// Restore file for cleanup
	log.mu.Lock()
	newFile, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if newFile != nil {
		log.file = newFile
	}
	log.mu.Unlock()

	log.Close()
}

// TestW4N7_PeriodicSync_NoCallbackWhenNil verifies that nil callback doesn't
// cause panic when failures reach threshold.
func TestW4N7_PeriodicSync_NoCallbackWhenNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:                 path,
		SyncInterval:         5 * time.Millisecond,
		SyncFailureThreshold: 1, // Very low threshold
		SyncHealthCallback:   nil, // No callback
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record some data
	log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})

	// Close file to cause failure
	log.mu.Lock()
	log.file.Close()
	log.mu.Unlock()

	// Wait for syncs to fail - should not panic
	time.Sleep(50 * time.Millisecond)

	// Restore file for cleanup
	log.mu.Lock()
	newFile, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if newFile != nil {
		log.file = newFile
	}
	log.mu.Unlock()

	log.Close()
}

// TestW4N7_PeriodicSync_ErrorDetailsInLog verifies that error details are
// properly included in log output.
func TestW4N7_PeriodicSync_ErrorDetailsInLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	var logMu sync.Mutex
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, buf: &logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 10 * time.Millisecond,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record and close file to cause sync failure
	log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})

	log.mu.Lock()
	log.file.Close()
	log.mu.Unlock()

	// Wait for sync to fail
	time.Sleep(50 * time.Millisecond)

	// Restore file for cleanup
	log.mu.Lock()
	newFile, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if newFile != nil {
		log.file = newFile
	}
	log.mu.Unlock()

	log.Close()

	// Now safely read the log buffer after async writer stopped
	logMu.Lock()
	logOutput := logBuf.String()
	logMu.Unlock()

	// Check for required fields in log
	if !bytes.Contains([]byte(logOutput), []byte("path")) {
		t.Errorf("expected 'path' in log output, got: %s", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("consecutive_failures")) {
		t.Errorf("expected 'consecutive_failures' in log output, got: %s", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("error")) {
		t.Errorf("expected 'error' in log output, got: %s", logOutput)
	}
}

// TestW4N7_PeriodicSync_DefaultLogger verifies that default logger is used
// when no logger is provided.
func TestW4N7_PeriodicSync_DefaultLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create without explicit logger
	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:         path,
		SyncInterval: 10 * time.Millisecond,
		Logger:       nil, // Should use default
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Verify logger is not nil
	if log.logger == nil {
		t.Error("expected logger to be set to default, got nil")
	}
}

// TestW4N7_PeriodicSync_DefaultThreshold verifies that default failure threshold
// is used when none is provided.
func TestW4N7_PeriodicSync_DefaultThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:                 path,
		SyncFailureThreshold: 0, // Should use default
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Verify default threshold is used
	if log.syncFailureThreshold != DefaultSyncFailureThreshold {
		t.Errorf("expected threshold %d, got %d", DefaultSyncFailureThreshold, log.syncFailureThreshold)
	}
}

// TestW4N7_PeriodicSync_HandleSyncError_IncrementCount verifies that each
// call to handleSyncError increments the consecutive failure count.
func TestW4N7_PeriodicSync_HandleSyncError_IncrementCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	var logMu sync.Mutex
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, buf: &logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:   path,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Call handleSyncError multiple times
	testErr := errors.New("test sync error")
	for i := 1; i <= 5; i++ {
		log.mu.Lock()
		log.handleSyncError(testErr)
		failures := log.consecutiveSyncFailures
		log.mu.Unlock()

		if failures != i {
			t.Errorf("expected %d consecutive failures, got %d", i, failures)
		}
	}
}

// TestW4N7_PeriodicSync_CallbackCalledEachTimeAboveThreshold verifies that
// the callback is called each time sync fails once above threshold.
func TestW4N7_PeriodicSync_CallbackCalledEachTimeAboveThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	var callbackCount atomic.Int32
	callback := func(consecutiveFailures int, lastError error) {
		callbackCount.Add(1)
	}

	var logMu sync.Mutex
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&syncWriter{mu: &logMu, buf: &logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:                 path,
		SyncFailureThreshold: 2,
		SyncHealthCallback:   callback,
		Logger:               logger,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	testErr := errors.New("test sync error")

	// First failure - below threshold
	log.mu.Lock()
	log.handleSyncError(testErr)
	log.mu.Unlock()
	if callbackCount.Load() != 0 {
		t.Errorf("callback should not be called on first failure")
	}

	// Second failure - at threshold
	log.mu.Lock()
	log.handleSyncError(testErr)
	log.mu.Unlock()
	if callbackCount.Load() != 1 {
		t.Errorf("callback should be called at threshold, got count %d", callbackCount.Load())
	}

	// Third failure - above threshold
	log.mu.Lock()
	log.handleSyncError(testErr)
	log.mu.Unlock()
	if callbackCount.Load() != 2 {
		t.Errorf("callback should be called again above threshold, got count %d", callbackCount.Load())
	}
}
