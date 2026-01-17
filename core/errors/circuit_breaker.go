// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	// CircuitClosed allows requests to proceed normally.
	CircuitClosed CircuitState = iota

	// CircuitOpen blocks all requests during cooldown.
	CircuitOpen

	// CircuitHalfOpen allows probe requests to test recovery.
	CircuitHalfOpen
)

var circuitStateNames = map[CircuitState]string{
	CircuitClosed:   "closed",
	CircuitOpen:     "open",
	CircuitHalfOpen: "half_open",
}

// String returns the string representation of the circuit state.
func (s CircuitState) String() string {
	if name, ok := circuitStateNames[s]; ok {
		return name
	}
	return "unknown"
}

// CircuitBreakerConfig configures circuit breaker behavior.
type CircuitBreakerConfig struct {
	// ConsecutiveFailures is the count-based trip threshold.
	ConsecutiveFailures int `yaml:"consecutive_failures"`

	// FailureRateThreshold is the rate-based trip threshold (0.0-1.0).
	FailureRateThreshold float64 `yaml:"failure_rate_threshold"`

	// RateWindowSize is the sliding window size for rate calculation.
	RateWindowSize int `yaml:"rate_window_size"`

	// CooldownDuration is the time before transitioning to half-open.
	CooldownDuration time.Duration `yaml:"cooldown_duration"`

	// SuccessThreshold is the number of successes needed to close.
	SuccessThreshold int `yaml:"success_threshold"`

	// NotifyOnStateChange indicates whether to notify on state transitions.
	NotifyOnStateChange bool `yaml:"notify_on_state_change"`
}

// DefaultCircuitBreakerConfig returns the default configuration.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		ConsecutiveFailures:  3,
		FailureRateThreshold: 0.5,
		RateWindowSize:       20,
		CooldownDuration:     30 * time.Second,
		SuccessThreshold:     3,
		NotifyOnStateChange:  true,
	}
}

// CircuitBreaker implements the circuit breaker pattern for fault tolerance.
type CircuitBreaker struct {
	mu              sync.RWMutex
	state           CircuitState
	failures        int
	successes       int
	lastFailure     time.Time
	lastStateChange time.Time
	config          CircuitBreakerConfig
	resourceID      string
	recentResults   []bool
	windowIndex     int
}

// NewCircuitBreaker creates a new circuit breaker for a resource.
func NewCircuitBreaker(resourceID string, config CircuitBreakerConfig) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:           CircuitClosed,
		config:          config,
		resourceID:      resourceID,
		lastStateChange: time.Now(),
		recentResults:   make([]bool, config.RateWindowSize),
		windowIndex:     0,
	}
	cb.initializeWindow()
	return cb
}

// initializeWindow sets all window slots to success (true).
func (cb *CircuitBreaker) initializeWindow() {
	for i := range cb.recentResults {
		cb.recentResults[i] = true
	}
}

// RecordResult tracks the outcome of an operation.
func (cb *CircuitBreaker) RecordResult(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.recordToWindow(success)

	if success {
		cb.recordSuccess()
	} else {
		cb.recordFailure()
	}
}

// recordToWindow adds a result to the sliding window.
func (cb *CircuitBreaker) recordToWindow(success bool) {
	cb.recentResults[cb.windowIndex] = success
	cb.windowIndex = (cb.windowIndex + 1) % len(cb.recentResults)
}

// recordSuccess handles a successful operation.
func (cb *CircuitBreaker) recordSuccess() {
	cb.failures = 0

	if cb.state == CircuitHalfOpen {
		cb.handleHalfOpenSuccess()
	}
}

// handleHalfOpenSuccess processes success in half-open state.
func (cb *CircuitBreaker) handleHalfOpenSuccess() {
	cb.successes++
	if cb.successes >= cb.config.SuccessThreshold {
		cb.transitionTo(CircuitClosed)
	}
}

// recordFailure handles a failed operation.
func (cb *CircuitBreaker) recordFailure() {
	cb.failures++
	cb.successes = 0
	cb.lastFailure = time.Now()

	if cb.state != CircuitOpen && cb.shouldTrip() {
		cb.transitionTo(CircuitOpen)
	}
}

// shouldTrip checks if the circuit should open.
func (cb *CircuitBreaker) shouldTrip() bool {
	if cb.hasExceededConsecutiveFailures() {
		return true
	}
	return cb.hasExceededFailureRate()
}

// hasExceededConsecutiveFailures checks the consecutive failure threshold.
func (cb *CircuitBreaker) hasExceededConsecutiveFailures() bool {
	return cb.failures >= cb.config.ConsecutiveFailures
}

// hasExceededFailureRate checks the failure rate threshold.
func (cb *CircuitBreaker) hasExceededFailureRate() bool {
	rate := cb.calculateFailureRate()
	return rate >= cb.config.FailureRateThreshold
}

// calculateFailureRate computes the failure rate from the sliding window.
func (cb *CircuitBreaker) calculateFailureRate() float64 {
	if len(cb.recentResults) == 0 {
		return 0.0
	}

	failures := 0
	for _, success := range cb.recentResults {
		if !success {
			failures++
		}
	}

	return float64(failures) / float64(len(cb.recentResults))
}

// transitionTo changes the circuit state.
func (cb *CircuitBreaker) transitionTo(state CircuitState) {
	cb.state = state
	cb.lastStateChange = time.Now()
	cb.resetCounters(state)
}

// resetCounters resets counters based on the new state.
func (cb *CircuitBreaker) resetCounters(state CircuitState) {
	if state == CircuitClosed {
		cb.failures = 0
		cb.successes = 0
	} else if state == CircuitHalfOpen {
		cb.successes = 0
	}
}

// Allow checks if a request should proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		return cb.checkCooldownExpired()
	case CircuitHalfOpen:
		return true
	default:
		return true
	}
}

// checkCooldownExpired handles the open state cooldown check.
func (cb *CircuitBreaker) checkCooldownExpired() bool {
	if cb.isInCooldown() {
		return false
	}
	cb.transitionTo(CircuitHalfOpen)
	return true
}

// isInCooldown checks if the cooldown period is still active.
func (cb *CircuitBreaker) isInCooldown() bool {
	elapsed := time.Since(cb.lastStateChange)
	return elapsed < cb.config.CooldownDuration
}

// ForceReset manually resets the circuit breaker to closed state.
func (cb *CircuitBreaker) ForceReset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.transitionTo(CircuitClosed)
	cb.initializeWindow()
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetResourceID returns the resource identifier.
func (cb *CircuitBreaker) GetResourceID() string {
	return cb.resourceID
}

// Config returns the circuit breaker configuration.
func (cb *CircuitBreaker) Config() CircuitBreakerConfig {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.config
}

// Failures returns the current consecutive failure count.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

// FailureRate returns the current failure rate.
func (cb *CircuitBreaker) FailureRate() float64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.calculateFailureRate()
}

// LastStateChange returns the time of the last state transition.
func (cb *CircuitBreaker) LastStateChange() time.Time {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.lastStateChange
}
