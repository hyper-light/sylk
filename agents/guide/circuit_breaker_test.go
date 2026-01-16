package guide

import (
	"testing"
	"time"
)

func TestCircuitBreaker_StartsInClosedState(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())
	if cb.State() != CircuitClosed {
		t.Errorf("expected initial state to be Closed, got %v", cb.State())
	}
}

func TestCircuitBreaker_AllowsRequestsWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())
	if !cb.Allow() {
		t.Error("expected Allow() to return true when circuit is closed")
	}
}

func TestCircuitBreaker_OpensAfterFailureThreshold(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		ResetTimeout:     1 * time.Second,
	}
	cb := NewCircuitBreaker(cfg)

	// Record failures up to threshold
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if cb.State() != CircuitOpen {
		t.Errorf("expected state to be Open after %d failures, got %v", cfg.FailureThreshold, cb.State())
	}
}

func TestCircuitBreaker_RejectsRequestsWhenOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     1 * time.Hour, // Long timeout so it doesn't transition
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.Allow() {
		t.Error("expected Allow() to return false when circuit is open")
	}
}

func TestCircuitBreaker_TransitionsToHalfOpenAfterTimeout(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     10 * time.Millisecond,
		HalfOpenMaxCalls: 3,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset timeout
	time.Sleep(20 * time.Millisecond)

	// Should now allow and transition to half-open
	if !cb.Allow() {
		t.Error("expected Allow() to return true after reset timeout")
	}
	if cb.State() != CircuitHalfOpen {
		t.Errorf("expected state to be HalfOpen, got %v", cb.State())
	}
}

func TestCircuitBreaker_ClosesFromHalfOpenOnSuccess(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		ResetTimeout:     10 * time.Millisecond,
		HalfOpenMaxCalls: 3,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset timeout
	time.Sleep(20 * time.Millisecond)

	// Transition to half-open
	cb.Allow()

	// Record successes to close
	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.State() != CircuitClosed {
		t.Errorf("expected state to be Closed after successes, got %v", cb.State())
	}
}

func TestCircuitBreaker_ReopensFromHalfOpenOnFailure(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		ResetTimeout:     10 * time.Millisecond,
		HalfOpenMaxCalls: 3,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset timeout
	time.Sleep(20 * time.Millisecond)

	// Transition to half-open
	cb.Allow()

	// Record failure - should reopen
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Errorf("expected state to be Open after failure in half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 3,
	}
	cb := NewCircuitBreaker(cfg)

	// Record some failures
	cb.RecordFailure()
	cb.RecordFailure()

	// Record success - should reset failures
	cb.RecordSuccess()

	// More failures shouldn't open yet (reset to 0 + 2 = 2 < 3)
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitClosed {
		t.Error("expected circuit to remain closed after success reset")
	}

	// One more should open it
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Error("expected circuit to open after reaching threshold again")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     1 * time.Hour,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Force reset
	cb.Reset()

	if cb.State() != CircuitClosed {
		t.Errorf("expected state to be Closed after Reset, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Error("expected Allow() to return true after Reset")
	}
}

func TestCircuitBreaker_ForceOpen(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())

	cb.ForceOpen()

	if cb.State() != CircuitOpen {
		t.Errorf("expected state to be Open after ForceOpen, got %v", cb.State())
	}
}

func TestCircuitBreaker_Stats(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // This resets failure count to 0
	cb.RecordFailure()

	stats := cb.Stats()

	if stats.State != CircuitClosed {
		t.Errorf("expected stats.State to be Closed, got %v", stats.State)
	}
	// After success, failures reset to 0, then we add 1 more failure
	if stats.Failures != 1 {
		t.Errorf("expected stats.Failures to be 1 (reset on success), got %d", stats.Failures)
	}
	if stats.ConsecutiveFails != 1 {
		t.Errorf("expected stats.ConsecutiveFails to be 1, got %d", stats.ConsecutiveFails)
	}
}

func TestCircuitBreaker_HalfOpenMaxCalls(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		ResetTimeout:     10 * time.Millisecond,
		HalfOpenMaxCalls: 2,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure()

	// Wait for reset timeout
	time.Sleep(20 * time.Millisecond)

	// First two calls should be allowed
	if !cb.Allow() {
		t.Error("expected first call to be allowed in half-open")
	}
	if !cb.Allow() {
		t.Error("expected second call to be allowed in half-open")
	}

	// Third call should be rejected (max calls reached)
	if cb.Allow() {
		t.Error("expected third call to be rejected in half-open")
	}
}

func TestCircuitBreakerRegistry_GetOrCreate(t *testing.T) {
	registry := NewCircuitBreakerRegistry(DefaultCircuitBreakerConfig())

	cb1 := registry.Get("agent1")
	cb2 := registry.Get("agent1")

	if cb1 != cb2 {
		t.Error("expected same circuit breaker instance for same agent")
	}

	cb3 := registry.Get("agent2")
	if cb1 == cb3 {
		t.Error("expected different circuit breaker for different agent")
	}
}

func TestCircuitBreakerRegistry_AllowAndRecord(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     1 * time.Hour,
	}
	registry := NewCircuitBreakerRegistry(cfg)

	// Initial state allows
	if !registry.Allow("agent1") {
		t.Error("expected initial Allow to be true")
	}

	// Record failures to open
	registry.RecordFailure("agent1")
	registry.RecordFailure("agent1")

	// Now should be blocked
	if registry.Allow("agent1") {
		t.Error("expected Allow to be false after failures")
	}

	// Other agents unaffected
	if !registry.Allow("agent2") {
		t.Error("expected other agent to be unaffected")
	}
}

func TestCircuitBreakerRegistry_Remove(t *testing.T) {
	registry := NewCircuitBreakerRegistry(DefaultCircuitBreakerConfig())

	registry.Get("agent1")
	registry.Remove("agent1")

	// Getting again should create new instance
	cb := registry.Get("agent1")
	if cb.State() != CircuitClosed {
		t.Error("expected new circuit breaker after removal")
	}
}

func TestCircuitBreakerRegistry_OpenCircuits(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		ResetTimeout:     1 * time.Hour,
	}
	registry := NewCircuitBreakerRegistry(cfg)

	// Open circuit for agent1
	registry.RecordFailure("agent1")

	// agent2 remains closed
	registry.Get("agent2")

	open := registry.OpenCircuits()
	if len(open) != 1 || open[0] != "agent1" {
		t.Errorf("expected [agent1], got %v", open)
	}
}
