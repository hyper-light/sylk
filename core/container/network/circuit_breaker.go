package network

import (
	"sync/atomic"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int32

const (
	CircuitClosed   CircuitState = iota // Healthy — traffic flows
	CircuitOpen                         // Tripped — fast-fail all traffic
	CircuitHalfOpen                     // Trial — allow probe traffic
)

// circuitStateNames maps circuit states to their string representation.
var circuitStateNames = map[CircuitState]string{
	CircuitClosed:   "closed",
	CircuitOpen:     "open",
	CircuitHalfOpen: "half-open",
}

// String returns the string representation of a circuit state.
func (s CircuitState) String() string {
	if name, ok := circuitStateNames[s]; ok {
		return name
	}
	return "unknown"
}

// CircuitBreakerConfig configures a circuit breaker.
type CircuitBreakerConfig struct {
	FailureThreshold  int64         // Consecutive failures before opening
	SuccessThreshold  int64         // Consecutive successes in half-open to close
	ResetTimeout      time.Duration // Time in open before transitioning to half-open
	HalfOpenMaxProbes int64         // Max concurrent requests in half-open
}

// DefaultCircuitBreakerConfig returns sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold:  defaultCBFailureThreshold,
		SuccessThreshold:  defaultCBSuccessThreshold,
		ResetTimeout:      defaultCBResetTimeout,
		HalfOpenMaxProbes: defaultCBHalfOpenProbes,
	}
}

const (
	defaultCBFailureThreshold = 5
	defaultCBSuccessThreshold = 2
	defaultCBResetTimeout     = 30 * time.Second
	defaultCBHalfOpenProbes   = 1
)

// CircuitBreaker implements the circuit breaker pattern per agent.
// Closed → Open (after threshold failures), Open → HalfOpen (after timeout),
// HalfOpen → Closed (after threshold successes) or HalfOpen → Open (on failure).
type CircuitBreaker struct {
	agentID         string
	state           atomic.Int32
	failureCount    atomic.Int64
	successCount    atomic.Int64
	halfOpenProbes  atomic.Int64
	lastFailureTime atomic.Int64 // UnixNano
	config          CircuitBreakerConfig
}

// NewCircuitBreaker creates a circuit breaker for the given agent.
func NewCircuitBreaker(agentID string, cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaultCBFailureThreshold
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = defaultCBSuccessThreshold
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = defaultCBResetTimeout
	}
	if cfg.HalfOpenMaxProbes <= 0 {
		cfg.HalfOpenMaxProbes = defaultCBHalfOpenProbes
	}
	cb := &CircuitBreaker{
		agentID: agentID,
		config:  cfg,
	}
	cb.state.Store(int32(CircuitClosed))
	return cb
}

// Allow reports whether a request should be allowed through.
// In Open state, checks if the reset timeout has elapsed to transition
// to HalfOpen. In HalfOpen, limits concurrent probes.
func (cb *CircuitBreaker) Allow() bool {
	state := CircuitState(cb.state.Load())
	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		return cb.tryTransitionToHalfOpen()
	case CircuitHalfOpen:
		return cb.halfOpenProbes.Add(1) <= cb.config.HalfOpenMaxProbes
	default:
		return false
	}
}

func (cb *CircuitBreaker) tryTransitionToHalfOpen() bool {
	lastFail := time.Unix(0, cb.lastFailureTime.Load())
	if time.Since(lastFail) < cb.config.ResetTimeout {
		return false
	}
	if cb.state.CompareAndSwap(int32(CircuitOpen), int32(CircuitHalfOpen)) {
		cb.successCount.Store(0)
		cb.halfOpenProbes.Store(1)
		return true
	}
	return CircuitState(cb.state.Load()) == CircuitHalfOpen
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess() {
	state := CircuitState(cb.state.Load())
	switch state {
	case CircuitClosed:
		cb.failureCount.Store(0)
	case CircuitHalfOpen:
		cb.recordHalfOpenSuccess()
	}
}

func (cb *CircuitBreaker) recordHalfOpenSuccess() {
	newCount := cb.successCount.Add(1)
	if newCount >= cb.config.SuccessThreshold {
		cb.transitionToClosed()
	}
}

func (cb *CircuitBreaker) transitionToClosed() {
	cb.state.Store(int32(CircuitClosed))
	cb.failureCount.Store(0)
	cb.successCount.Store(0)
	cb.halfOpenProbes.Store(0)
}

// RecordFailure records a failed request.
func (cb *CircuitBreaker) RecordFailure() {
	cb.lastFailureTime.Store(time.Now().UnixNano())
	state := CircuitState(cb.state.Load())
	switch state {
	case CircuitClosed:
		cb.recordClosedFailure()
	case CircuitHalfOpen:
		cb.transitionToOpen()
	}
}

func (cb *CircuitBreaker) recordClosedFailure() {
	newCount := cb.failureCount.Add(1)
	if newCount >= cb.config.FailureThreshold {
		cb.transitionToOpen()
	}
}

func (cb *CircuitBreaker) transitionToOpen() {
	cb.state.Store(int32(CircuitOpen))
	cb.halfOpenProbes.Store(0)
	cb.successCount.Store(0)
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(cb.state.Load())
}

// AgentID returns the agent this breaker is for.
func (cb *CircuitBreaker) AgentID() string {
	return cb.agentID
}

// FailureCount returns the current consecutive failure count.
func (cb *CircuitBreaker) FailureCount() int64 {
	return cb.failureCount.Load()
}

// Reset forces the circuit to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.transitionToClosed()
}
