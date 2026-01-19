// Package context provides types and utilities for adaptive retrieval context management.
// This file implements circuit breakers for search subsystems (Bleve and VectorDB).
package context

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Circuit State
// =============================================================================

// CircuitState represents the state of a circuit breaker.
type CircuitState int32

const (
	// StateClosed allows all requests through.
	StateClosed CircuitState = 0

	// StateOpen blocks all requests.
	StateOpen CircuitState = 1

	// StateHalfOpen allows limited probe requests.
	StateHalfOpen CircuitState = 2
)

var circuitStateNames = map[CircuitState]string{
	StateClosed:   "closed",
	StateOpen:     "open",
	StateHalfOpen: "half_open",
}

// String returns the string representation of the circuit state.
func (s CircuitState) String() string {
	if name, ok := circuitStateNames[s]; ok {
		return name
	}
	return "unknown"
}

// =============================================================================
// Errors
// =============================================================================

// ErrCircuitOpen is returned when a circuit breaker is open and blocking requests.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// =============================================================================
// Configuration
// =============================================================================

// SearchCircuitConfig holds configuration for search circuit breakers.
type SearchCircuitConfig struct {
	// FailureThreshold is the number of consecutive failures before opening.
	FailureThreshold int

	// ResetTimeout is how long to wait before transitioning from open to half-open.
	ResetTimeout time.Duration

	// HalfOpenMaxRequests is the number of requests allowed in half-open state.
	HalfOpenMaxRequests int
}

// DefaultSearchCircuitConfig returns the default configuration.
func DefaultSearchCircuitConfig() SearchCircuitConfig {
	return SearchCircuitConfig{
		FailureThreshold:    5,
		ResetTimeout:        30 * time.Second,
		HalfOpenMaxRequests: 2,
	}
}

// =============================================================================
// Internal Circuit Breaker
// =============================================================================

// circuitBreaker is an internal circuit breaker implementation.
type circuitBreaker struct {
	state           int32
	failures        int32
	successes       int32
	halfOpenCount   int32
	lastStateChange int64
	config          SearchCircuitConfig
	mu              sync.Mutex
}

// newCircuitBreaker creates a new circuit breaker with the given config.
func newCircuitBreaker(config SearchCircuitConfig) *circuitBreaker {
	return &circuitBreaker{
		state:           int32(StateClosed),
		lastStateChange: time.Now().UnixNano(),
		config:          config,
	}
}

// getState returns the current circuit state.
func (cb *circuitBreaker) getState() CircuitState {
	return CircuitState(atomic.LoadInt32(&cb.state))
}

// allow checks if a request should be allowed through.
func (cb *circuitBreaker) allow() bool {
	state := cb.getState()

	switch state {
	case StateClosed:
		return true
	case StateOpen:
		return cb.checkResetTimeout()
	case StateHalfOpen:
		return cb.allowHalfOpen()
	default:
		return true
	}
}

// checkResetTimeout checks if the reset timeout has expired.
func (cb *circuitBreaker) checkResetTimeout() bool {
	lastChange := atomic.LoadInt64(&cb.lastStateChange)
	elapsed := time.Duration(time.Now().UnixNano() - lastChange)

	if elapsed < cb.config.ResetTimeout {
		return false
	}

	cb.transitionTo(StateHalfOpen)
	return true
}

// allowHalfOpen checks if requests are allowed in half-open state.
func (cb *circuitBreaker) allowHalfOpen() bool {
	count := atomic.LoadInt32(&cb.halfOpenCount)
	return int(count) < cb.config.HalfOpenMaxRequests
}

// recordSuccess records a successful operation.
func (cb *circuitBreaker) recordSuccess() {
	atomic.StoreInt32(&cb.failures, 0)

	state := cb.getState()
	if state != StateHalfOpen {
		return
	}

	newSuccesses := atomic.AddInt32(&cb.successes, 1)
	if int(newSuccesses) >= cb.config.HalfOpenMaxRequests {
		cb.transitionTo(StateClosed)
	}
}

// recordFailure records a failed operation.
func (cb *circuitBreaker) recordFailure() {
	atomic.StoreInt32(&cb.successes, 0)
	newFailures := atomic.AddInt32(&cb.failures, 1)

	state := cb.getState()
	if state == StateHalfOpen {
		cb.transitionTo(StateOpen)
		return
	}

	if state == StateClosed && int(newFailures) >= cb.config.FailureThreshold {
		cb.transitionTo(StateOpen)
	}
}

// transitionTo transitions the circuit to a new state.
func (cb *circuitBreaker) transitionTo(state CircuitState) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	atomic.StoreInt32(&cb.state, int32(state))
	atomic.StoreInt64(&cb.lastStateChange, time.Now().UnixNano())

	cb.resetCounters(state)
}

// resetCounters resets counters based on the target state.
func (cb *circuitBreaker) resetCounters(state CircuitState) {
	switch state {
	case StateClosed:
		atomic.StoreInt32(&cb.failures, 0)
		atomic.StoreInt32(&cb.successes, 0)
		atomic.StoreInt32(&cb.halfOpenCount, 0)
	case StateHalfOpen:
		atomic.StoreInt32(&cb.successes, 0)
		atomic.StoreInt32(&cb.halfOpenCount, 0)
	case StateOpen:
		atomic.StoreInt32(&cb.halfOpenCount, 0)
	}
}

// reset resets the circuit breaker to closed state.
func (cb *circuitBreaker) reset() {
	cb.transitionTo(StateClosed)
}

// =============================================================================
// Search Circuit Breakers
// =============================================================================

// SearchCircuitBreakers manages circuit breakers for search subsystems.
type SearchCircuitBreakers struct {
	bleveBreaker  *circuitBreaker
	vectorBreaker *circuitBreaker
	config        SearchCircuitConfig
	mu            sync.RWMutex
}

// NewSearchCircuitBreakers creates circuit breakers for Bleve and VectorDB.
func NewSearchCircuitBreakers(config SearchCircuitConfig) *SearchCircuitBreakers {
	return &SearchCircuitBreakers{
		bleveBreaker:  newCircuitBreaker(config),
		vectorBreaker: newCircuitBreaker(config),
		config:        config,
	}
}

// ExecuteBleve executes a function through the Bleve circuit breaker.
func (s *SearchCircuitBreakers) ExecuteBleve(ctx context.Context, fn func() error) error {
	s.mu.RLock()
	breaker := s.bleveBreaker
	s.mu.RUnlock()

	return s.execute(ctx, breaker, fn)
}

// ExecuteVector executes a function through the Vector circuit breaker.
func (s *SearchCircuitBreakers) ExecuteVector(ctx context.Context, fn func() error) error {
	s.mu.RLock()
	breaker := s.vectorBreaker
	s.mu.RUnlock()

	return s.execute(ctx, breaker, fn)
}

// execute runs a function through the given circuit breaker.
func (s *SearchCircuitBreakers) execute(ctx context.Context, breaker *circuitBreaker, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if !breaker.allow() {
		return ErrCircuitOpen
	}

	if breaker.getState() == StateHalfOpen {
		atomic.AddInt32(&breaker.halfOpenCount, 1)
	}

	err := fn()
	s.recordResult(breaker, err)
	return err
}

// recordResult records the result of an operation.
func (s *SearchCircuitBreakers) recordResult(breaker *circuitBreaker, err error) {
	if err == nil {
		breaker.recordSuccess()
	} else {
		breaker.recordFailure()
	}
}

// BleveState returns the current state of the Bleve circuit breaker.
func (s *SearchCircuitBreakers) BleveState() CircuitState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bleveBreaker.getState()
}

// VectorState returns the current state of the Vector circuit breaker.
func (s *SearchCircuitBreakers) VectorState() CircuitState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vectorBreaker.getState()
}

// Reset resets both circuit breakers to closed state.
func (s *SearchCircuitBreakers) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.bleveBreaker.reset()
	s.vectorBreaker.reset()
}

// ResetBleve resets only the Bleve circuit breaker.
func (s *SearchCircuitBreakers) ResetBleve() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bleveBreaker.reset()
}

// ResetVector resets only the Vector circuit breaker.
func (s *SearchCircuitBreakers) ResetVector() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vectorBreaker.reset()
}

// Config returns the current configuration.
func (s *SearchCircuitBreakers) Config() SearchCircuitConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}
