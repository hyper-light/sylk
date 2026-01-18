package timeout

import (
	"context"
	"testing"
	"time"
)

func TestNewStreamingTimeoutMonitor(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 50 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)
	defer stm.Done()

	if stm.config != config {
		t.Error("config not set correctly")
	}
	if stm.started.IsZero() {
		t.Error("started time should be set")
	}
	if stm.totalCtx == nil {
		t.Error("totalCtx should not be nil")
	}
	if stm.tokenCh == nil {
		t.Error("tokenCh should not be nil")
	}
	if stm.errorCh == nil {
		t.Error("errorCh should not be nil")
	}
}

func TestStreamingTimeoutMonitor_FirstTokenTimeout(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 50 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)
	defer stm.Done()

	select {
	case err := <-stm.Errors():
		if err != ErrFirstTokenTimeout {
			t.Errorf("expected ErrFirstTokenTimeout, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("expected first token timeout error")
	}
}

func TestStreamingTimeoutMonitor_InterTokenTimeout(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 50 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)
	defer stm.Done()

	// Send first token to pass first token timeout check
	stm.RecordToken()

	// Wait for inter-token timeout
	select {
	case err := <-stm.Errors():
		if err != ErrInterTokenTimeout {
			t.Errorf("expected ErrInterTokenTimeout, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("expected inter-token timeout error")
	}
}

func TestStreamingTimeoutMonitor_SuccessfulCompletion(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)

	// Simulate successful streaming with multiple tokens
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(30 * time.Millisecond)
			stm.RecordToken()
		}
		stm.Done()
	}()

	// Should not receive any timeout errors
	select {
	case err := <-stm.Errors():
		t.Errorf("unexpected error: %v", err)
	case <-stm.Context().Done():
		// Stream completed successfully
	}
}

func TestStreamingTimeoutMonitor_RecordToken(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)
	defer stm.Done()

	// Record first token
	stm.RecordToken()

	stats := stm.Stats()
	if stats.FirstTokenAt.IsZero() {
		t.Error("firstToken should be set after RecordToken")
	}
	if stats.LastTokenAt.IsZero() {
		t.Error("lastToken should be set after RecordToken")
	}

	firstTokenTime := stats.FirstTokenAt

	// Record second token
	time.Sleep(10 * time.Millisecond)
	stm.RecordToken()

	stats = stm.Stats()
	if stats.FirstTokenAt != firstTokenTime {
		t.Error("firstToken should not change after first token")
	}
	if !stats.LastTokenAt.After(firstTokenTime) {
		t.Error("lastToken should be updated")
	}
}

func TestStreamingTimeoutMonitor_Stats(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)
	defer stm.Done()

	// Initial stats
	stats := stm.Stats()
	if stats.Started.IsZero() {
		t.Error("Started should be set")
	}
	if !stats.FirstTokenAt.IsZero() {
		t.Error("FirstTokenAt should be zero initially")
	}
	if stats.TimeToFirstToken != 0 {
		t.Error("TimeToFirstToken should be zero initially")
	}

	// After recording token
	time.Sleep(20 * time.Millisecond)
	stm.RecordToken()

	stats = stm.Stats()
	if stats.TimeToFirstToken < 20*time.Millisecond {
		t.Error("TimeToFirstToken should be at least 20ms")
	}
	if stats.TotalDuration < 20*time.Millisecond {
		t.Error("TotalDuration should be at least 20ms")
	}
}

func TestStreamingTimeoutMonitor_Context(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      50 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)

	ctx := stm.Context()
	if ctx == nil {
		t.Error("Context should not be nil")
	}

	// Wait for total timeout
	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(200 * time.Millisecond):
		t.Error("context should be done after total timeout")
	}
}

func TestStreamingTimeoutMonitor_Done(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)

	stm.RecordToken()
	stm.Done()

	select {
	case <-stm.Context().Done():
		// Expected - context should be cancelled
	case <-time.After(50 * time.Millisecond):
		t.Error("context should be cancelled after Done()")
	}
}

func TestStreamingTimeoutMonitor_ParentContextCancellation(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 200 * time.Millisecond,
		InterTokenTimeout: 200 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	parentCtx, parentCancel := context.WithCancel(context.Background())
	stm := NewStreamingTimeoutMonitor(parentCtx, config)

	// Cancel parent context
	go func() {
		time.Sleep(50 * time.Millisecond)
		parentCancel()
	}()

	select {
	case <-stm.Context().Done():
		// Expected - should be cancelled when parent is cancelled
	case <-time.After(200 * time.Millisecond):
		t.Error("context should be cancelled when parent is cancelled")
	}
}

func TestStreamingTimeoutMonitor_MultipleTokensPreventTimeout(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 50 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)

	// Send tokens faster than inter-token timeout
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			stm.RecordToken()
			time.Sleep(20 * time.Millisecond) // Less than 50ms inter-token timeout
		}
		close(done)
	}()

	// Should not receive timeout errors while tokens are flowing
	select {
	case err := <-stm.Errors():
		t.Errorf("unexpected error while tokens flowing: %v", err)
	case <-done:
		// All tokens sent without timeout
	}

	stm.Done()
}

func TestStreamingTimeoutMonitor_NonBlockingRecordToken(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)
	defer stm.Done()

	// Record many tokens rapidly - should not block
	for i := 0; i < 100; i++ {
		stm.RecordToken()
	}

	stats := stm.Stats()
	if stats.FirstTokenAt.IsZero() {
		t.Error("firstToken should be set")
	}
}

func TestStreamingTimeoutMonitor_ConcurrentAccess(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 200 * time.Millisecond,
		InterTokenTimeout: 200 * time.Millisecond,
		TotalTimeout:      500 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)

	// Concurrent access from multiple goroutines
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			stm.RecordToken()
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()

	go func() {
		for i := 0; i < 50; i++ {
			_ = stm.Stats()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	<-done
	stm.Done()
}

func TestStreamingTimeoutMonitor_TotalTimeoutExceeded(t *testing.T) {
	config := StreamingTimeoutConfig{
		FirstTokenTimeout: 100 * time.Millisecond,
		InterTokenTimeout: 100 * time.Millisecond,
		TotalTimeout:      100 * time.Millisecond,
	}

	stm := NewStreamingTimeoutMonitor(context.Background(), config)

	// Send token to pass first token check
	stm.RecordToken()

	// Wait for total timeout
	select {
	case <-stm.Context().Done():
		// Expected - total timeout exceeded
	case <-time.After(200 * time.Millisecond):
		t.Error("context should be done after total timeout")
	}
}
