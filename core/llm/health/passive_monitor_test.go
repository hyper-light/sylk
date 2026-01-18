package health

import (
	"errors"
	"testing"
	"time"
)

func TestNewPassiveMonitor(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	if pm.windowSize != 5*time.Minute {
		t.Errorf("expected windowSize 5m, got %v", pm.windowSize)
	}
	if pm.minSamples != 10 {
		t.Errorf("expected minSamples 10, got %d", pm.minSamples)
	}
	if len(pm.outcomes) != 100 {
		t.Errorf("expected buffer size 100, got %d", len(pm.outcomes))
	}
	if pm.head != 0 {
		t.Errorf("expected head 0, got %d", pm.head)
	}
	if pm.count != 0 {
		t.Errorf("expected count 0, got %d", pm.count)
	}
}

func TestRecordSuccess(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	pm.RecordSuccess(100 * time.Millisecond)

	if pm.count != 1 {
		t.Errorf("expected count 1, got %d", pm.count)
	}

	// Check the recorded outcome
	idx := (pm.head - 1 + len(pm.outcomes)) % len(pm.outcomes)
	o := pm.outcomes[idx]

	if !o.success {
		t.Error("expected success to be true")
	}
	if o.latency != 100*time.Millisecond {
		t.Errorf("expected latency 100ms, got %v", o.latency)
	}
	if o.errorType != ErrNone {
		t.Errorf("expected errorType ErrNone, got %v", o.errorType)
	}
}

func TestRecordFailure(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	pm.RecordFailure(errors.New("rate limit exceeded 429"))

	if pm.count != 1 {
		t.Errorf("expected count 1, got %d", pm.count)
	}

	idx := (pm.head - 1 + len(pm.outcomes)) % len(pm.outcomes)
	o := pm.outcomes[idx]

	if o.success {
		t.Error("expected success to be false")
	}
	if o.errorType != ErrRateLimit {
		t.Errorf("expected errorType ErrRateLimit, got %v", o.errorType)
	}
}

func TestRecordFailureCategories(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorCategory
	}{
		{"rate limit", errors.New("429 too many requests"), ErrRateLimit},
		{"auth error", errors.New("401 unauthorized"), ErrAuth},
		{"timeout", errors.New("connection timeout"), ErrTransient},
		{"permanent", errors.New("invalid request format"), ErrPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPassiveMonitor(5*time.Minute, 10)
			pm.RecordFailure(tt.err)

			idx := (pm.head - 1 + len(pm.outcomes)) % len(pm.outcomes)
			if pm.outcomes[idx].errorType != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, pm.outcomes[idx].errorType)
			}
		})
	}
}

func TestScoreInsufficientSamples(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	// Add fewer than minSamples
	for i := 0; i < 5; i++ {
		pm.RecordSuccess(100 * time.Millisecond)
	}

	score := pm.Score()
	if score != 0.8 {
		t.Errorf("expected default score 0.8, got %f", score)
	}
}

func TestScoreAllSuccesses(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	// Add exactly minSamples successes
	for i := 0; i < 10; i++ {
		pm.RecordSuccess(100 * time.Millisecond)
	}

	score := pm.Score()
	if score != 1.0 {
		t.Errorf("expected score 1.0, got %f", score)
	}
}

func TestScoreAllFailures(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	for i := 0; i < 10; i++ {
		pm.RecordFailure(errors.New("some error"))
	}

	score := pm.Score()
	if score != 0.0 {
		t.Errorf("expected score 0.0, got %f", score)
	}
}

func TestScoreMixedResults(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	// 8 successes, 2 failures = 80% success rate
	for i := 0; i < 8; i++ {
		pm.RecordSuccess(100 * time.Millisecond)
	}
	for i := 0; i < 2; i++ {
		pm.RecordFailure(errors.New("error"))
	}

	score := pm.Score()
	expected := 0.8
	if score != expected {
		t.Errorf("expected score %f, got %f", expected, score)
	}
}

func TestScoreHighLatencyPenalty(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	// 9 fast successes + 1 slow success (>10s)
	for i := 0; i < 9; i++ {
		pm.RecordSuccess(100 * time.Millisecond)
	}
	pm.RecordSuccess(11 * time.Second)

	score := pm.Score()
	expected := 1.0 * 0.8 // 100% success rate * 0.8 penalty
	if score != expected {
		t.Errorf("expected score %f, got %f", expected, score)
	}
}

func TestScoreMediumLatencyPenalty(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	// 9 fast successes + 1 medium-slow success (>5s, <=10s)
	for i := 0; i < 9; i++ {
		pm.RecordSuccess(100 * time.Millisecond)
	}
	pm.RecordSuccess(6 * time.Second)

	score := pm.Score()
	expected := 1.0 * 0.9 // 100% success rate * 0.9 penalty
	if score != expected {
		t.Errorf("expected score %f, got %f", expected, score)
	}
}

func TestScoreHighLatencyTakesPrecedence(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	// Mix of high and medium latency - high should take precedence
	for i := 0; i < 8; i++ {
		pm.RecordSuccess(100 * time.Millisecond)
	}
	pm.RecordSuccess(6 * time.Second)  // medium
	pm.RecordSuccess(11 * time.Second) // high

	score := pm.Score()
	expected := 1.0 * 0.8 // high latency penalty wins
	if score != expected {
		t.Errorf("expected score %f, got %f", expected, score)
	}
}

func TestRingBufferWraparound(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 5) // buffer size = 50

	// Fill buffer and overflow
	for i := 0; i < 60; i++ {
		pm.RecordSuccess(100 * time.Millisecond)
	}

	if pm.count != 50 {
		t.Errorf("expected count to cap at 50, got %d", pm.count)
	}

	score := pm.Score()
	if score != 1.0 {
		t.Errorf("expected score 1.0 after wraparound, got %f", score)
	}
}

func TestWindowFiltering(t *testing.T) {
	pm := NewPassiveMonitor(100*time.Millisecond, 5)

	// Record 5 successes
	for i := 0; i < 5; i++ {
		pm.RecordSuccess(10 * time.Millisecond)
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Now add 5 failures
	for i := 0; i < 5; i++ {
		pm.RecordFailure(errors.New("error"))
	}

	// Only recent failures should count
	score := pm.Score()
	if score != 0.0 {
		t.Errorf("expected score 0.0 (only failures in window), got %f", score)
	}
}

func TestConcurrentAccess(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)
	done := make(chan bool)

	// Concurrent writers
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				pm.RecordSuccess(100 * time.Millisecond)
			}
			done <- true
		}()
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = pm.Score()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not panic and count should be valid
	if pm.count < 1 || pm.count > len(pm.outcomes) {
		t.Errorf("unexpected count %d", pm.count)
	}
}

func TestEmptyMonitorScore(t *testing.T) {
	pm := NewPassiveMonitor(5*time.Minute, 10)

	score := pm.Score()
	if score != 0.8 {
		t.Errorf("expected default score 0.8 for empty monitor, got %f", score)
	}
}

func TestLatencyBoundaries(t *testing.T) {
	tests := []struct {
		name            string
		latency         time.Duration
		expectedPenalty float64
	}{
		{"exactly 5s - no penalty", 5 * time.Second, 1.0},
		{"just over 5s - medium penalty", 5*time.Second + time.Millisecond, 0.9},
		{"exactly 10s - medium penalty", 10 * time.Second, 0.9},
		{"just over 10s - high penalty", 10*time.Second + time.Millisecond, 0.8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPassiveMonitor(5*time.Minute, 1)
			pm.RecordSuccess(tt.latency)

			score := pm.Score()
			if score != tt.expectedPenalty {
				t.Errorf("expected score %f, got %f", tt.expectedPenalty, score)
			}
		})
	}
}
