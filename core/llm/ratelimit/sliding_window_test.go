package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// TestNewSlidingWindowLimiter tests the constructor.
func TestNewSlidingWindowLimiter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      SlidingWindowConfig
		wantBufSize int
	}{
		{
			name: "standard config",
			config: SlidingWindowConfig{
				WindowSize:  time.Minute,
				MaxRequests: 60,
			},
			wantBufSize: 60,
		},
		{
			name: "small buffer",
			config: SlidingWindowConfig{
				WindowSize:  time.Second,
				MaxRequests: 5,
			},
			wantBufSize: 5,
		},
		{
			name: "large buffer",
			config: SlidingWindowConfig{
				WindowSize:  time.Hour,
				MaxRequests: 1000,
			},
			wantBufSize: 1000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewSlidingWindowLimiter(tc.config)

			if limiter == nil {
				t.Fatal("NewSlidingWindowLimiter returned nil")
			}
			if len(limiter.timestamps) != tc.wantBufSize {
				t.Errorf("buffer size = %d, want %d", len(limiter.timestamps), tc.wantBufSize)
			}
			if limiter.head != 0 {
				t.Errorf("head = %d, want 0", limiter.head)
			}
			if limiter.count != 0 {
				t.Errorf("count = %d, want 0", limiter.count)
			}
			if limiter.config != tc.config {
				t.Errorf("config mismatch")
			}
		})
	}
}

// TestSlidingWindowLimiter_Check_AllowsUnderLimit tests requests under limit.
func TestSlidingWindowLimiter_Check_AllowsUnderLimit(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 5,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Record 4 requests (under limit of 5)
	for i := 0; i < 4; i++ {
		limiter.Record()
	}

	decision := limiter.Check()

	if !decision.Allowed {
		t.Error("expected request to be allowed when under limit")
	}
	if decision.WaitTime != 0 {
		t.Errorf("WaitTime = %v, want 0", decision.WaitTime)
	}
	if decision.Limiter != "sliding_window" {
		t.Errorf("Limiter = %q, want %q", decision.Limiter, "sliding_window")
	}
	if decision.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", decision.Confidence)
	}
}

// TestSlidingWindowLimiter_Check_DeniesAtLimit tests requests at limit.
func TestSlidingWindowLimiter_Check_DeniesAtLimit(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 3,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Record exactly MaxRequests
	for i := 0; i < 3; i++ {
		limiter.Record()
	}

	decision := limiter.Check()

	if decision.Allowed {
		t.Error("expected request to be denied when at limit")
	}
	if decision.WaitTime <= 0 {
		t.Errorf("WaitTime = %v, expected positive", decision.WaitTime)
	}
	if decision.Reason != "sliding window limit exceeded" {
		t.Errorf("Reason = %q, want %q", decision.Reason, "sliding window limit exceeded")
	}
	if decision.Limiter != "sliding_window" {
		t.Errorf("Limiter = %q, want %q", decision.Limiter, "sliding_window")
	}
	if decision.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", decision.Confidence)
	}
}

// TestSlidingWindowLimiter_WaitTimeCalculation tests wait time accuracy.
func TestSlidingWindowLimiter_WaitTimeCalculation(t *testing.T) {
	t.Parallel()

	windowSize := 100 * time.Millisecond
	config := SlidingWindowConfig{
		WindowSize:  windowSize,
		MaxRequests: 2,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Record 2 requests to hit the limit
	limiter.Record()
	limiter.Record()

	decision := limiter.Check()

	if decision.Allowed {
		t.Fatal("expected request to be denied")
	}

	// Wait time should be close to windowSize (within tolerance)
	tolerance := 20 * time.Millisecond
	if decision.WaitTime > windowSize+tolerance {
		t.Errorf("WaitTime = %v, expected <= %v", decision.WaitTime, windowSize+tolerance)
	}
	if decision.WaitTime < windowSize-tolerance {
		t.Errorf("WaitTime = %v, expected >= %v", decision.WaitTime, windowSize-tolerance)
	}
}

// TestSlidingWindowLimiter_OldEntryCleanup tests cleanup of expired entries.
func TestSlidingWindowLimiter_OldEntryCleanup(t *testing.T) {
	t.Parallel()

	windowSize := 50 * time.Millisecond
	config := SlidingWindowConfig{
		WindowSize:  windowSize,
		MaxRequests: 3,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Fill the buffer
	for i := 0; i < 3; i++ {
		limiter.Record()
	}

	// Should be denied now
	decision := limiter.Check()
	if decision.Allowed {
		t.Error("expected denial before window expires")
	}

	// Wait for window to expire
	time.Sleep(windowSize + 10*time.Millisecond)

	// Should be allowed after cleanup
	decision = limiter.Check()
	if !decision.Allowed {
		t.Error("expected allowance after window expired")
	}
}

// TestSlidingWindowLimiter_PartialCleanup tests partial entry expiration.
func TestSlidingWindowLimiter_PartialCleanup(t *testing.T) {
	t.Parallel()

	windowSize := 100 * time.Millisecond
	config := SlidingWindowConfig{
		WindowSize:  windowSize,
		MaxRequests: 3,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Record first request
	limiter.Record()

	// Wait half the window
	time.Sleep(60 * time.Millisecond)

	// Record two more requests
	limiter.Record()
	limiter.Record()

	// Should be denied (3 requests in window)
	decision := limiter.Check()
	if decision.Allowed {
		t.Error("expected denial with 3 requests in window")
	}

	// Wait for first request to expire
	time.Sleep(50 * time.Millisecond)

	// Should be allowed (first request expired, only 2 left)
	decision = limiter.Check()
	if !decision.Allowed {
		t.Error("expected allowance after first request expired")
	}
}

// TestSlidingWindowLimiter_Record tests the Record function.
func TestSlidingWindowLimiter_Record(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 5,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Record some requests and check count
	for i := 1; i <= 5; i++ {
		limiter.Record()

		limiter.mu.Lock()
		count := limiter.count
		limiter.mu.Unlock()

		if count != i {
			t.Errorf("after %d Record(), count = %d, want %d", i, count, i)
		}
	}
}

// TestSlidingWindowLimiter_CircularBufferWrap tests buffer wraparound.
func TestSlidingWindowLimiter_CircularBufferWrap(t *testing.T) {
	t.Parallel()

	windowSize := 30 * time.Millisecond
	config := SlidingWindowConfig{
		WindowSize:  windowSize,
		MaxRequests: 3,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Fill buffer, let expire, fill again multiple times
	for round := 0; round < 3; round++ {
		// Fill the buffer
		for i := 0; i < 3; i++ {
			limiter.Record()
		}

		decision := limiter.Check()
		if decision.Allowed {
			t.Errorf("round %d: expected denial when buffer full", round)
		}

		// Wait for expiration
		time.Sleep(windowSize + 10*time.Millisecond)

		decision = limiter.Check()
		if !decision.Allowed {
			t.Errorf("round %d: expected allowance after expiration", round)
		}
	}
}

// TestSlidingWindowLimiter_EmptyBuffer tests empty buffer behavior.
func TestSlidingWindowLimiter_EmptyBuffer(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 10,
	}
	limiter := NewSlidingWindowLimiter(config)

	decision := limiter.Check()

	if !decision.Allowed {
		t.Error("expected allowance for empty buffer")
	}
	if decision.WaitTime != 0 {
		t.Errorf("WaitTime = %v, want 0", decision.WaitTime)
	}
}

// TestSlidingWindowLimiter_SingleRequest tests single request behavior.
func TestSlidingWindowLimiter_SingleRequest(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Second,
		MaxRequests: 1,
	}
	limiter := NewSlidingWindowLimiter(config)

	// First check should allow
	decision := limiter.Check()
	if !decision.Allowed {
		t.Error("expected first check to be allowed")
	}

	// Record the request
	limiter.Record()

	// Second check should deny
	decision = limiter.Check()
	if decision.Allowed {
		t.Error("expected second check to be denied")
	}
}

// TestSlidingWindowLimiter_Concurrent tests thread safety.
func TestSlidingWindowLimiter_Concurrent(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 100,
	}
	limiter := NewSlidingWindowLimiter(config)

	var wg sync.WaitGroup
	numGoroutines := 20
	requestsPerGoroutine := 5 // Total: 100 = MaxRequests

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				limiter.Check()
				limiter.Record()
			}
		}()
	}

	wg.Wait()

	// Verify count is capped at MaxRequests
	limiter.mu.Lock()
	count := limiter.count
	limiter.mu.Unlock()

	expected := numGoroutines * requestsPerGoroutine
	if count != expected {
		t.Errorf("count = %d, want %d", count, expected)
	}
}

// TestSlidingWindowLimiter_RateLimiterInterface tests interface compliance.
func TestSlidingWindowLimiter_RateLimiterInterface(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 10,
	}

	var limiter RateLimiter = NewSlidingWindowLimiter(config)

	decision := limiter.Check()
	if !decision.Allowed {
		t.Error("expected interface implementation to work")
	}
}

// TestSlidingWindowLimiter_WaitTimeNonNegative tests WaitTime never negative.
func TestSlidingWindowLimiter_WaitTimeNonNegative(t *testing.T) {
	t.Parallel()

	windowSize := 10 * time.Millisecond
	config := SlidingWindowConfig{
		WindowSize:  windowSize,
		MaxRequests: 2,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Fill buffer
	limiter.Record()
	limiter.Record()

	// Wait longer than window
	time.Sleep(windowSize + 20*time.Millisecond)

	// Check should allow (entries expired)
	decision := limiter.Check()
	if !decision.Allowed {
		// If somehow denied, WaitTime must be non-negative
		if decision.WaitTime < 0 {
			t.Errorf("WaitTime = %v, must be non-negative", decision.WaitTime)
		}
	}
}

// TestSlidingWindowLimiter_DecisionFields tests all decision fields.
func TestSlidingWindowLimiter_DecisionFields(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 1,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Test allowed decision fields
	allowed := limiter.Check()
	if allowed.Limiter != "sliding_window" {
		t.Errorf("allowed.Limiter = %q, want %q", allowed.Limiter, "sliding_window")
	}
	if allowed.Confidence != 1.0 {
		t.Errorf("allowed.Confidence = %v, want 1.0", allowed.Confidence)
	}
	if allowed.Reason != "" {
		t.Errorf("allowed.Reason = %q, want empty", allowed.Reason)
	}

	// Fill buffer
	limiter.Record()

	// Test denied decision fields
	denied := limiter.Check()
	if denied.Limiter != "sliding_window" {
		t.Errorf("denied.Limiter = %q, want %q", denied.Limiter, "sliding_window")
	}
	if denied.Confidence != 1.0 {
		t.Errorf("denied.Confidence = %v, want 1.0", denied.Confidence)
	}
	if denied.Reason != "sliding window limit exceeded" {
		t.Errorf("denied.Reason = %q, want %q", denied.Reason, "sliding window limit exceeded")
	}
}

// TestSlidingWindowLimiter_RecordOverflow tests recording beyond MaxRequests.
func TestSlidingWindowLimiter_RecordOverflow(t *testing.T) {
	t.Parallel()

	config := SlidingWindowConfig{
		WindowSize:  time.Minute,
		MaxRequests: 3,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Record more than MaxRequests
	for i := 0; i < 10; i++ {
		limiter.Record()
	}

	limiter.mu.Lock()
	count := limiter.count
	limiter.mu.Unlock()

	// Count should cap at MaxRequests
	if count != 3 {
		t.Errorf("count = %d, want %d (MaxRequests)", count, 3)
	}
}

// TestSlidingWindowLimiter_CleanupDuringRecord tests cleanup happens on Record.
func TestSlidingWindowLimiter_CleanupDuringRecord(t *testing.T) {
	t.Parallel()

	windowSize := 30 * time.Millisecond
	config := SlidingWindowConfig{
		WindowSize:  windowSize,
		MaxRequests: 5,
	}
	limiter := NewSlidingWindowLimiter(config)

	// Record some requests
	limiter.Record()
	limiter.Record()

	// Wait for expiration
	time.Sleep(windowSize + 10*time.Millisecond)

	// Record new request (should trigger cleanup first)
	limiter.Record()

	limiter.mu.Lock()
	count := limiter.count
	limiter.mu.Unlock()

	// Should only have the new request
	if count != 1 {
		t.Errorf("count = %d, want 1 after cleanup during Record", count)
	}
}
