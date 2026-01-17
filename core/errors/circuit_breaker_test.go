// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Happy Path Tests
// =============================================================================

func TestCircuitBreaker_StartsClosed(t *testing.T) {
	cb := NewCircuitBreaker("test-resource", DefaultCircuitBreakerConfig())

	if cb.State() != CircuitClosed {
		t.Errorf("expected new breaker to be closed, got %v", cb.State())
	}
}

func TestCircuitBreaker_AllowsWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker("test-resource", DefaultCircuitBreakerConfig())

	if !cb.Allow() {
		t.Error("expected Allow() to return true when circuit is closed")
	}
}

func TestCircuitBreaker_SuccessResetsFails(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 5
	cb := NewCircuitBreaker("test-resource", config)

	// Record some failures (not enough to trip)
	cb.RecordResult(false)
	cb.RecordResult(false)

	if cb.Failures() != 2 {
		t.Errorf("expected 2 failures, got %d", cb.Failures())
	}

	// Record a success - should reset consecutive failures
	cb.RecordResult(true)

	if cb.Failures() != 0 {
		t.Errorf("expected 0 failures after success, got %d", cb.Failures())
	}
}

func TestCircuitBreaker_RecordSuccess_StaysClosed(t *testing.T) {
	cb := NewCircuitBreaker("test-resource", DefaultCircuitBreakerConfig())

	// Record multiple successes
	for i := 0; i < 10; i++ {
		cb.RecordResult(true)
	}

	if cb.State() != CircuitClosed {
		t.Errorf("expected circuit to remain closed after successes, got %v", cb.State())
	}
}

// =============================================================================
// State Transition Tests
// =============================================================================

func TestCircuitBreaker_ConsecutiveFailures_TripsOpen(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 3
	config.FailureRateThreshold = 1.0 // Disable rate-based tripping
	cb := NewCircuitBreaker("test-resource", config)

	// Record consecutive failures
	for i := 0; i < 3; i++ {
		cb.RecordResult(false)
	}

	if cb.State() != CircuitOpen {
		t.Errorf("expected circuit to be open after %d consecutive failures, got %v",
			config.ConsecutiveFailures, cb.State())
	}
}

func TestCircuitBreaker_FailureRate_TripsOpen(t *testing.T) {
	config := CircuitBreakerConfig{
		ConsecutiveFailures:  100, // High threshold to disable consecutive failure check
		FailureRateThreshold: 0.5, // 50% failure rate
		RateWindowSize:       10,  // Small window for testing
		CooldownDuration:     time.Second,
		SuccessThreshold:     1,
		NotifyOnStateChange:  false,
	}
	cb := NewCircuitBreaker("test-resource", config)

	// Fill window with failures to exceed 50% rate
	// Window starts with all success (10 successes)
	// We need >50% failures in the window
	for i := 0; i < 6; i++ {
		cb.RecordResult(false)
	}

	// At this point we have 6 failures and 4 successes in window = 60% failure rate
	if cb.State() != CircuitOpen {
		t.Errorf("expected circuit to be open after exceeding failure rate, got %v (rate: %.2f)",
			cb.State(), cb.FailureRate())
	}
}

func TestCircuitBreaker_Open_BlocksRequests(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = time.Hour // Long cooldown
	cb := NewCircuitBreaker("test-resource", config)

	// Trip the circuit
	cb.RecordResult(false)

	if cb.State() != CircuitOpen {
		t.Fatalf("expected circuit to be open, got %v", cb.State())
	}

	if cb.Allow() {
		t.Error("expected Allow() to return false when circuit is open and in cooldown")
	}
}

func TestCircuitBreaker_Open_TransitionsToHalfOpen(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = 10 * time.Millisecond // Short cooldown for testing
	cb := NewCircuitBreaker("test-resource", config)

	// Trip the circuit
	cb.RecordResult(false)

	if cb.State() != CircuitOpen {
		t.Fatalf("expected circuit to be open, got %v", cb.State())
	}

	// Wait for cooldown to expire
	time.Sleep(20 * time.Millisecond)

	// Allow() should transition to half-open
	if !cb.Allow() {
		t.Error("expected Allow() to return true after cooldown")
	}

	if cb.State() != CircuitHalfOpen {
		t.Errorf("expected circuit to be half-open after cooldown, got %v", cb.State())
	}
}

func TestCircuitBreaker_HalfOpen_SuccessCloses(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = 10 * time.Millisecond
	config.SuccessThreshold = 2
	cb := NewCircuitBreaker("test-resource", config)

	// Trip the circuit and transition to half-open
	cb.RecordResult(false)
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // Transition to half-open

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected circuit to be half-open, got %v", cb.State())
	}

	// Record successes to meet threshold
	cb.RecordResult(true)
	if cb.State() == CircuitClosed {
		t.Error("circuit should not close after just 1 success")
	}

	cb.RecordResult(true)
	if cb.State() != CircuitClosed {
		t.Errorf("expected circuit to close after %d successes, got %v",
			config.SuccessThreshold, cb.State())
	}
}

func TestCircuitBreaker_HalfOpen_FailureReopens(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = 10 * time.Millisecond
	config.SuccessThreshold = 3
	cb := NewCircuitBreaker("test-resource", config)

	// Trip the circuit and transition to half-open
	cb.RecordResult(false)
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // Transition to half-open

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected circuit to be half-open, got %v", cb.State())
	}

	// Record a failure - should reopen circuit
	cb.RecordResult(false)

	if cb.State() != CircuitOpen {
		t.Errorf("expected circuit to reopen after failure in half-open state, got %v", cb.State())
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestCircuitBreaker_ForceReset(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = time.Hour
	cb := NewCircuitBreaker("test-resource", config)

	// Trip the circuit
	cb.RecordResult(false)

	if cb.State() != CircuitOpen {
		t.Fatalf("expected circuit to be open, got %v", cb.State())
	}

	// Force reset
	cb.ForceReset()

	if cb.State() != CircuitClosed {
		t.Errorf("expected circuit to be closed after ForceReset, got %v", cb.State())
	}

	// Verify it allows requests again
	if !cb.Allow() {
		t.Error("expected Allow() to return true after ForceReset")
	}
}

func TestCircuitBreaker_SlidingWindow_FailureRate(t *testing.T) {
	config := CircuitBreakerConfig{
		ConsecutiveFailures:  100, // Disable consecutive check
		FailureRateThreshold: 1.0, // Disable rate-based tripping for this test
		RateWindowSize:       5,
		CooldownDuration:     time.Second,
		SuccessThreshold:     1,
		NotifyOnStateChange:  false,
	}
	cb := NewCircuitBreaker("test-resource", config)

	// Window starts with all successes (5)
	// Initial rate should be 0%
	if rate := cb.FailureRate(); rate != 0.0 {
		t.Errorf("expected initial failure rate 0.0, got %.2f", rate)
	}

	// Record 2 failures - rate should be 2/5 = 40%
	cb.RecordResult(false)
	cb.RecordResult(false)

	expectedRate := 0.4
	if rate := cb.FailureRate(); rate != expectedRate {
		t.Errorf("expected failure rate %.2f, got %.2f", expectedRate, rate)
	}

	// Record 3 more failures - all 5 slots are now failures = 100%
	cb.RecordResult(false)
	cb.RecordResult(false)
	cb.RecordResult(false)

	if rate := cb.FailureRate(); rate != 1.0 {
		t.Errorf("expected failure rate 1.0, got %.2f", rate)
	}

	// Record 2 successes - now 3 failures, 2 successes = 60%
	cb.RecordResult(true)
	cb.RecordResult(true)

	expectedRate = 0.6
	if rate := cb.FailureRate(); rate != expectedRate {
		t.Errorf("expected failure rate %.2f, got %.2f", expectedRate, rate)
	}
}

func TestCircuitBreaker_CooldownTiming(t *testing.T) {
	cooldownDuration := 50 * time.Millisecond
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = cooldownDuration
	cb := NewCircuitBreaker("test-resource", config)

	// Trip the circuit
	cb.RecordResult(false)
	tripTime := time.Now()

	if cb.State() != CircuitOpen {
		t.Fatalf("expected circuit to be open, got %v", cb.State())
	}

	// Check before cooldown expires
	time.Sleep(cooldownDuration / 2)
	if cb.Allow() {
		t.Error("expected Allow() to return false before cooldown expires")
	}

	// Wait for remaining cooldown plus buffer
	time.Sleep(cooldownDuration/2 + 10*time.Millisecond)

	// Now it should allow
	if !cb.Allow() {
		elapsed := time.Since(tripTime)
		t.Errorf("expected Allow() to return true after cooldown (elapsed: %v, cooldown: %v)",
			elapsed, cooldownDuration)
	}
}

func TestCircuitBreaker_InitializedWindowAllSuccess(t *testing.T) {
	config := CircuitBreakerConfig{
		ConsecutiveFailures:  100,
		FailureRateThreshold: 0.5,
		RateWindowSize:       10,
		CooldownDuration:     time.Second,
		SuccessThreshold:     1,
		NotifyOnStateChange:  false,
	}
	cb := NewCircuitBreaker("test-resource", config)

	// Initial failure rate should be 0 (all successes)
	if rate := cb.FailureRate(); rate != 0.0 {
		t.Errorf("expected initial failure rate 0.0, got %.2f", rate)
	}

	// Circuit should be closed
	if cb.State() != CircuitClosed {
		t.Errorf("expected circuit to start closed, got %v", cb.State())
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestCircuitBreaker_ConcurrentRecordResult(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 100 // High threshold
	config.FailureRateThreshold = 1.0
	cb := NewCircuitBreaker("test-resource", config)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Spawn goroutines that record results concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(success bool) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				cb.RecordResult(success)
			}
		}(i%2 == 0)
	}

	wg.Wait()

	// Just verify no race/panic occurred and state is valid
	state := cb.State()
	if state != CircuitClosed && state != CircuitOpen && state != CircuitHalfOpen {
		t.Errorf("invalid circuit state: %v", state)
	}
}

func TestCircuitBreaker_ConcurrentAllow(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = 10 * time.Millisecond
	cb := NewCircuitBreaker("test-resource", config)

	// Trip the circuit
	cb.RecordResult(false)

	// Wait for cooldown to almost expire
	time.Sleep(5 * time.Millisecond)

	var wg sync.WaitGroup
	numGoroutines := 50

	// Spawn goroutines that call Allow() concurrently
	// Some will see open, some will see half-open
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				cb.Allow()
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Verify final state is valid
	state := cb.State()
	if state != CircuitClosed && state != CircuitOpen && state != CircuitHalfOpen {
		t.Errorf("invalid circuit state after concurrent Allow: %v", state)
	}
}

// =============================================================================
// Additional Tests for Code Coverage
// =============================================================================

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state    CircuitState
		expected string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half_open"},
		{CircuitState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("CircuitState(%d).String() = %q, want %q", tt.state, got, tt.expected)
			}
		})
	}
}

func TestCircuitBreaker_GetResourceID(t *testing.T) {
	resourceID := "test-resource-123"
	cb := NewCircuitBreaker(resourceID, DefaultCircuitBreakerConfig())

	if cb.GetResourceID() != resourceID {
		t.Errorf("expected resource ID %q, got %q", resourceID, cb.GetResourceID())
	}
}

func TestCircuitBreaker_Config(t *testing.T) {
	config := CircuitBreakerConfig{
		ConsecutiveFailures:  7,
		FailureRateThreshold: 0.75,
		RateWindowSize:       25,
		CooldownDuration:     45 * time.Second,
		SuccessThreshold:     5,
		NotifyOnStateChange:  false,
	}
	cb := NewCircuitBreaker("test-resource", config)

	got := cb.Config()
	if got.ConsecutiveFailures != config.ConsecutiveFailures {
		t.Errorf("ConsecutiveFailures mismatch: got %d, want %d",
			got.ConsecutiveFailures, config.ConsecutiveFailures)
	}
	if got.FailureRateThreshold != config.FailureRateThreshold {
		t.Errorf("FailureRateThreshold mismatch: got %f, want %f",
			got.FailureRateThreshold, config.FailureRateThreshold)
	}
}

func TestCircuitBreaker_LastStateChange(t *testing.T) {
	cb := NewCircuitBreaker("test-resource", DefaultCircuitBreakerConfig())

	initialTime := cb.LastStateChange()
	if initialTime.IsZero() {
		t.Error("expected LastStateChange to be set on creation")
	}

	// Trip the circuit
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	cb2 := NewCircuitBreaker("test-resource-2", config)

	beforeTrip := time.Now()
	time.Sleep(time.Millisecond)
	cb2.RecordResult(false)
	afterTrip := time.Now()

	stateChangeTime := cb2.LastStateChange()
	if stateChangeTime.Before(beforeTrip) || stateChangeTime.After(afterTrip) {
		t.Error("LastStateChange not updated correctly after state transition")
	}
}

func TestCircuitBreaker_HalfOpen_AllowsRequests(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.ConsecutiveFailures = 1
	config.CooldownDuration = 10 * time.Millisecond
	cb := NewCircuitBreaker("test-resource", config)

	// Trip and transition to half-open
	cb.RecordResult(false)
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // Triggers transition to half-open

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected half-open state, got %v", cb.State())
	}

	// Half-open should allow requests
	if !cb.Allow() {
		t.Error("expected Allow() to return true in half-open state")
	}
}

func TestDefaultCircuitBreakerConfig(t *testing.T) {
	config := DefaultCircuitBreakerConfig()

	if config.ConsecutiveFailures != 3 {
		t.Errorf("expected ConsecutiveFailures=3, got %d", config.ConsecutiveFailures)
	}
	if config.FailureRateThreshold != 0.5 {
		t.Errorf("expected FailureRateThreshold=0.5, got %f", config.FailureRateThreshold)
	}
	if config.RateWindowSize != 20 {
		t.Errorf("expected RateWindowSize=20, got %d", config.RateWindowSize)
	}
	if config.CooldownDuration != 30*time.Second {
		t.Errorf("expected CooldownDuration=30s, got %v", config.CooldownDuration)
	}
	if config.SuccessThreshold != 3 {
		t.Errorf("expected SuccessThreshold=3, got %d", config.SuccessThreshold)
	}
	if !config.NotifyOnStateChange {
		t.Error("expected NotifyOnStateChange=true")
	}
}
