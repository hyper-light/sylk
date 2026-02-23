package network

import (
	"testing"
	"time"
)

func TestCircuitBreaker_StartsOpen(t *testing.T) {
	cb := NewCircuitBreaker("agent-1", DefaultCircuitBreakerConfig())
	if cb.State() != CircuitClosed {
		t.Fatalf("expected Closed, got %v", cb.State())
	}
}

func TestCircuitBreaker_AllowInClosed(t *testing.T) {
	cb := NewCircuitBreaker("agent-1", DefaultCircuitBreakerConfig())
	if !cb.Allow() {
		t.Fatal("should allow in closed state")
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:  3,
		SuccessThreshold:  1,
		ResetTimeout:      time.Hour,
		HalfOpenMaxProbes: 1,
	}
	cb := NewCircuitBreaker("agent-1", cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open after 3 failures, got %v", cb.State())
	}
}

func TestCircuitBreaker_DeniesInOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:  1,
		SuccessThreshold:  1,
		ResetTimeout:      time.Hour, // Long timeout so it stays open
		HalfOpenMaxProbes: 1,
	}
	cb := NewCircuitBreaker("agent-1", cfg)

	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("should deny in open state with long reset timeout")
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:  1,
		SuccessThreshold:  1,
		ResetTimeout:      50 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	}
	cb := NewCircuitBreaker("agent-1", cfg)

	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open, got %v", cb.State())
	}

	time.Sleep(60 * time.Millisecond)

	if !cb.Allow() {
		t.Fatal("should allow probe in half-open")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected HalfOpen, got %v", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClosedOnSuccess(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:  1,
		SuccessThreshold:  1,
		ResetTimeout:      10 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	}
	cb := NewCircuitBreaker("agent-1", cfg)

	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // transitions to half-open

	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected Closed after success in half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToOpenOnFailure(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:  1,
		SuccessThreshold:  1,
		ResetTimeout:      10 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	}
	cb := NewCircuitBreaker("agent-1", cfg)

	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // transitions to half-open

	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open after failure in half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:  1,
		SuccessThreshold:  1,
		ResetTimeout:      time.Hour,
		HalfOpenMaxProbes: 1,
	}
	cb := NewCircuitBreaker("agent-1", cfg)

	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open, got %v", cb.State())
	}

	cb.Reset()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected Closed after reset, got %v", cb.State())
	}
}

func TestCircuitBreaker_SuccessResetsFailed(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:  3,
		SuccessThreshold:  1,
		ResetTimeout:      time.Hour,
		HalfOpenMaxProbes: 1,
	}
	cb := NewCircuitBreaker("agent-1", cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // resets failure count

	if cb.FailureCount() != 0 {
		t.Fatalf("expected 0 failures after success, got %d", cb.FailureCount())
	}
	if cb.State() != CircuitClosed {
		t.Fatalf("expected Closed, got %v", cb.State())
	}
}
