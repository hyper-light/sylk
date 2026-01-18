// Package bulkhead provides isolation and circuit breaking for LLM operations.
// It implements a hierarchical bulkhead system with session, provider, and model levels.
package bulkhead

import (
	"errors"
	"time"
)

// BulkheadLevel represents the isolation hierarchy.
type BulkheadLevel int

const (
	// LevelSession provides per-session isolation.
	LevelSession BulkheadLevel = iota
	// LevelProvider provides per-provider isolation (e.g., OpenAI, Anthropic).
	LevelProvider
	// LevelModel provides per-model isolation within a provider.
	LevelModel
)

// String returns the string representation of the BulkheadLevel.
func (l BulkheadLevel) String() string {
	switch l {
	case LevelSession:
		return "session"
	case LevelProvider:
		return "provider"
	case LevelModel:
		return "model"
	default:
		return "unknown"
	}
}

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	// CircuitClosed means the circuit is operating normally.
	CircuitClosed CircuitState = iota
	// CircuitOpen means the circuit has tripped and requests are being rejected.
	CircuitOpen
	// CircuitHalfOpen means the circuit is testing if the service has recovered.
	CircuitHalfOpen
)

// String returns the string representation of the CircuitState.
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

// CircuitConfig configures per-bulkhead circuit breakers.
type CircuitConfig struct {
	// FailureThreshold is the number of consecutive failures required to open the circuit.
	FailureThreshold int
	// SuccessThreshold is the number of successes in half-open state required to close the circuit.
	SuccessThreshold int
	// Timeout is the duration to wait before transitioning from open to half-open.
	Timeout time.Duration
}

// BulkheadConfig configures a single bulkhead level.
type BulkheadConfig struct {
	// MaxConcurrent is the maximum number of concurrent requests (semaphore size).
	MaxConcurrent int
	// MaxQueueSize is the maximum number of pending requests in the queue.
	MaxQueueSize int
	// QueueTimeout is the maximum time a request can wait in the queue.
	QueueTimeout time.Duration
	// CircuitConfig provides circuit breaker configuration for this level.
	CircuitConfig CircuitConfig
}

// DefaultBulkheadConfigs returns production defaults for all bulkhead levels.
func DefaultBulkheadConfigs() map[BulkheadLevel]BulkheadConfig {
	return map[BulkheadLevel]BulkheadConfig{
		LevelSession:  defaultSessionConfig(),
		LevelProvider: defaultProviderConfig(),
		LevelModel:    defaultModelConfig(),
	}
}

// defaultSessionConfig returns the default configuration for session-level bulkheads.
func defaultSessionConfig() BulkheadConfig {
	return BulkheadConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  50,
		QueueTimeout:  30 * time.Second,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          60 * time.Second,
		},
	}
}

// defaultProviderConfig returns the default configuration for provider-level bulkheads.
func defaultProviderConfig() BulkheadConfig {
	return BulkheadConfig{
		MaxConcurrent: 3,
		MaxQueueSize:  20,
		QueueTimeout:  20 * time.Second,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 5,
			SuccessThreshold: 3,
			Timeout:          45 * time.Second,
		},
	}
}

// defaultModelConfig returns the default configuration for model-level bulkheads.
func defaultModelConfig() BulkheadConfig {
	return BulkheadConfig{
		MaxConcurrent: 2,
		MaxQueueSize:  10,
		QueueTimeout:  15 * time.Second,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 3,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		},
	}
}

// BulkheadStats provides metrics for a bulkhead.
type BulkheadStats struct {
	// ID is the unique identifier for this bulkhead.
	ID string
	// Level indicates which bulkhead level this represents.
	Level BulkheadLevel
	// Active is the number of currently executing requests.
	Active int
	// Queued is the number of requests waiting in the queue.
	Queued int
	// Available is the number of available semaphore slots.
	Available int
	// CircuitState is the current state of the circuit breaker.
	CircuitState CircuitState
	// TotalRequests is the total number of requests processed.
	TotalRequests int64
	// TotalFailures is the total number of failed requests.
	TotalFailures int64
}

// AcquireResult represents the result of acquiring a bulkhead slot.
type AcquireResult struct {
	// Acquired indicates whether the slot was successfully acquired.
	Acquired bool
	// Error contains any error that occurred during acquisition.
	Error error
}

// Error types for bulkhead operations.
var (
	// ErrCircuitOpen is returned when the circuit breaker is open.
	ErrCircuitOpen = errors.New("circuit breaker is open")
	// ErrQueueFull is returned when the bulkhead queue is at capacity.
	ErrQueueFull = errors.New("bulkhead queue is full")
	// ErrQueueTimeout is returned when a request times out while waiting in the queue.
	ErrQueueTimeout = errors.New("request timed out in queue")
)
