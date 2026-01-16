package guide

import (
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Circuit Breaker
// =============================================================================
//
// CircuitBreaker implements the circuit breaker pattern for agent communication.
// It tracks failures and prevents cascading failures by "opening" the circuit
// when an agent becomes unhealthy.
//
// States:
// - Closed: Normal operation, requests pass through
// - Open: Agent is failing, requests are rejected immediately
// - HalfOpen: Testing if agent has recovered
//
// Transitions:
// - Closed → Open: Failure threshold exceeded
// - Open → HalfOpen: Reset timeout elapsed
// - HalfOpen → Closed: Successful request
// - HalfOpen → Open: Failed request

// CircuitState represents the state of a circuit breaker
type CircuitState int32

const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Failing, reject requests
	CircuitHalfOpen                     // Testing recovery
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker tracks the health of a single agent
type CircuitBreaker struct {
	// State (atomic for lock-free reads)
	state int32

	// Configuration
	failureThreshold  int           // Failures before opening
	successThreshold  int           // Successes in half-open before closing
	resetTimeout      time.Duration // Time before trying half-open
	halfOpenMaxCalls  int           // Max concurrent calls in half-open

	// Counters
	mu                sync.Mutex
	failures          int
	successes         int
	consecutiveFails  int
	halfOpenCalls     int
	lastFailure       time.Time
	lastStateChange   time.Time
	openedAt          time.Time

	// Callbacks
	onStateChange func(from, to CircuitState)
}

// CircuitBreakerConfig configures a circuit breaker
type CircuitBreakerConfig struct {
	FailureThreshold int           // Default: 5
	SuccessThreshold int           // Default: 2
	ResetTimeout     time.Duration // Default: 30s
	HalfOpenMaxCalls int           // Default: 3
	OnStateChange    func(from, to CircuitState)
}

// DefaultCircuitBreakerConfig returns sensible defaults
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		ResetTimeout:     30 * time.Second,
		HalfOpenMaxCalls: 3,
	}
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 2
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxCalls <= 0 {
		cfg.HalfOpenMaxCalls = 3
	}

	return &CircuitBreaker{
		state:            int32(CircuitClosed),
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		resetTimeout:     cfg.ResetTimeout,
		halfOpenMaxCalls: cfg.HalfOpenMaxCalls,
		onStateChange:    cfg.OnStateChange,
		lastStateChange:  time.Now(),
	}
}

// State returns the current circuit state
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(atomic.LoadInt32(&cb.state))
}

// Allow checks if a request should be allowed through.
// Returns true if allowed, false if circuit is open.
// If in half-open state, may limit concurrent calls.
func (cb *CircuitBreaker) Allow() bool {
	state := cb.State()

	switch state {
	case CircuitClosed:
		return true

	case CircuitOpen:
		// Check if we should transition to half-open
		cb.mu.Lock()
		if time.Since(cb.openedAt) >= cb.resetTimeout {
			cb.transitionTo(CircuitHalfOpen)
			cb.halfOpenCalls++
			cb.mu.Unlock()
			return true
		}
		cb.mu.Unlock()
		return false

	case CircuitHalfOpen:
		cb.mu.Lock()
		if cb.halfOpenCalls >= cb.halfOpenMaxCalls {
			cb.mu.Unlock()
			return false
		}
		cb.halfOpenCalls++
		cb.mu.Unlock()
		return true

	default:
		return false
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0

	switch CircuitState(atomic.LoadInt32(&cb.state)) {
	case CircuitHalfOpen:
		cb.successes++
		cb.halfOpenCalls--
		if cb.successes >= cb.successThreshold {
			cb.transitionTo(CircuitClosed)
		}

	case CircuitClosed:
		// Reset failure count on success
		cb.failures = 0
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()
	cb.consecutiveFails++

	switch CircuitState(atomic.LoadInt32(&cb.state)) {
	case CircuitClosed:
		cb.failures++
		if cb.failures >= cb.failureThreshold {
			cb.transitionTo(CircuitOpen)
		}

	case CircuitHalfOpen:
		cb.halfOpenCalls--
		// Any failure in half-open reopens the circuit
		cb.transitionTo(CircuitOpen)
	}
}

// transitionTo changes the circuit state (must hold lock)
func (cb *CircuitBreaker) transitionTo(newState CircuitState) {
	oldState := CircuitState(atomic.LoadInt32(&cb.state))
	if oldState == newState {
		return
	}

	atomic.StoreInt32(&cb.state, int32(newState))
	cb.lastStateChange = time.Now()

	switch newState {
	case CircuitOpen:
		cb.openedAt = time.Now()
		cb.successes = 0
	case CircuitHalfOpen:
		cb.successes = 0
		cb.halfOpenCalls = 0
	case CircuitClosed:
		cb.failures = 0
		cb.successes = 0
	}

	if cb.onStateChange != nil {
		go cb.onStateChange(oldState, newState)
	}
}

// Reset forces the circuit to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	cb.transitionTo(CircuitClosed)
	cb.mu.Unlock()
}

// ForceOpen forces the circuit to open state
func (cb *CircuitBreaker) ForceOpen() {
	cb.mu.Lock()
	cb.transitionTo(CircuitOpen)
	cb.mu.Unlock()
}

// Stats returns circuit breaker statistics
func (cb *CircuitBreaker) Stats() CircuitBreakerStats {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return CircuitBreakerStats{
		State:            CircuitState(atomic.LoadInt32(&cb.state)),
		Failures:         cb.failures,
		Successes:        cb.successes,
		ConsecutiveFails: cb.consecutiveFails,
		LastFailure:      cb.lastFailure,
		LastStateChange:  cb.lastStateChange,
	}
}

// CircuitBreakerStats contains circuit breaker statistics
type CircuitBreakerStats struct {
	State            CircuitState `json:"state"`
	Failures         int          `json:"failures"`
	Successes        int          `json:"successes"`
	ConsecutiveFails int          `json:"consecutive_fails"`
	LastFailure      time.Time    `json:"last_failure"`
	LastStateChange  time.Time    `json:"last_state_change"`
}

// =============================================================================
// Circuit Breaker Registry
// =============================================================================

// CircuitBreakerRegistry manages circuit breakers for multiple agents
type CircuitBreakerRegistry struct {
	breakers *ShardedMap[string, *CircuitBreaker]
	config   CircuitBreakerConfig
}

// NewCircuitBreakerRegistry creates a new registry
func NewCircuitBreakerRegistry(cfg CircuitBreakerConfig) *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		breakers: NewStringMap[*CircuitBreaker](DefaultShardCount),
		config:   cfg,
	}
}

// Get returns the circuit breaker for an agent, creating one if needed
func (r *CircuitBreakerRegistry) Get(agentID string) *CircuitBreaker {
	cb, ok := r.breakers.Get(agentID)
	if ok {
		return cb
	}

	// Create new breaker
	newCB := NewCircuitBreaker(r.config)
	existing, loaded := r.breakers.GetOrSet(agentID, newCB)
	if loaded {
		return existing
	}
	return newCB
}

// Allow checks if a request to an agent should be allowed
func (r *CircuitBreakerRegistry) Allow(agentID string) bool {
	return r.Get(agentID).Allow()
}

// RecordSuccess records a successful request to an agent
func (r *CircuitBreakerRegistry) RecordSuccess(agentID string) {
	r.Get(agentID).RecordSuccess()
}

// RecordFailure records a failed request to an agent
func (r *CircuitBreakerRegistry) RecordFailure(agentID string) {
	r.Get(agentID).RecordFailure()
}

// Remove removes the circuit breaker for an agent
func (r *CircuitBreakerRegistry) Remove(agentID string) {
	r.breakers.Delete(agentID)
}

// Stats returns statistics for all circuit breakers
func (r *CircuitBreakerRegistry) Stats() map[string]CircuitBreakerStats {
	result := make(map[string]CircuitBreakerStats)
	r.breakers.Range(func(agentID string, cb *CircuitBreaker) bool {
		result[agentID] = cb.Stats()
		return true
	})
	return result
}

// OpenCircuits returns the IDs of agents with open circuits
func (r *CircuitBreakerRegistry) OpenCircuits() []string {
	var open []string
	r.breakers.Range(func(agentID string, cb *CircuitBreaker) bool {
		if cb.State() == CircuitOpen {
			open = append(open, agentID)
		}
		return true
	})
	return open
}
