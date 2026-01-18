// Package timeout provides timeout management for LLM streaming responses.
package timeout

import (
	"errors"
	"time"
)

// StreamingTimeoutConfig configures timeout behavior for streaming responses.
type StreamingTimeoutConfig struct {
	FirstTokenTimeout time.Duration // Max wait for first token
	InterTokenTimeout time.Duration // Max gap between tokens
	TotalTimeout      time.Duration // Hard cap on entire request
}

// DefaultStreamingTimeoutConfig returns sensible default timeout configuration.
func DefaultStreamingTimeoutConfig() StreamingTimeoutConfig {
	return StreamingTimeoutConfig{
		FirstTokenTimeout: 30 * time.Second,
		InterTokenTimeout: 10 * time.Second,
		TotalTimeout:      5 * time.Minute,
	}
}

// Validate checks if the configuration values are valid.
func (c StreamingTimeoutConfig) Validate() error {
	if c.FirstTokenTimeout <= 0 {
		return ErrInvalidFirstTokenTimeout
	}
	if c.InterTokenTimeout <= 0 {
		return ErrInvalidInterTokenTimeout
	}
	if c.TotalTimeout <= 0 {
		return ErrInvalidTotalTimeout
	}
	return nil
}

// StreamingStats holds timing statistics for streaming responses.
type StreamingStats struct {
	Started          time.Time
	FirstTokenAt     time.Time
	LastTokenAt      time.Time
	TimeToFirstToken time.Duration
	TotalDuration    time.Duration
}

// HasReceivedFirstToken returns true if the first token has been received.
func (s StreamingStats) HasReceivedFirstToken() bool {
	return !s.FirstTokenAt.IsZero()
}

// TokenCount returns an estimate based on timing (not actual count).
// For actual count, use a separate counter in the monitor.
func (s StreamingStats) IsComplete() bool {
	return s.TotalDuration > 0
}

// Error types for timeout conditions.
var (
	ErrFirstTokenTimeout       = errors.New("timeout waiting for first token")
	ErrInterTokenTimeout       = errors.New("timeout waiting for next token")
	ErrInvalidFirstTokenTimeout = errors.New("first token timeout must be positive")
	ErrInvalidInterTokenTimeout = errors.New("inter-token timeout must be positive")
	ErrInvalidTotalTimeout      = errors.New("total timeout must be positive")
)

// BackoffConfig configures jittered exponential backoff behavior.
type BackoffConfig struct {
	Base      time.Duration // Base delay duration
	Max       time.Duration // Maximum delay cap
	MinJitter float64       // Minimum jitter multiplier (e.g., 0.5)
	MaxJitter float64       // Maximum jitter multiplier (e.g., 1.5)
}

// DefaultBackoffConfig returns sensible default backoff configuration.
func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       30 * time.Second,
		MinJitter: 0.5,
		MaxJitter: 1.5,
	}
}

// Validate checks if the backoff configuration values are valid.
func (c BackoffConfig) Validate() error {
	if err := c.validateDurations(); err != nil {
		return err
	}
	return c.validateJitter()
}

// validateDurations checks if Base and Max durations are valid.
func (c BackoffConfig) validateDurations() error {
	if c.Base <= 0 {
		return ErrInvalidBaseBackoff
	}
	if c.Max <= 0 {
		return ErrInvalidMaxBackoff
	}
	return nil
}

// validateJitter checks if jitter values are valid.
func (c BackoffConfig) validateJitter() error {
	if c.MinJitter < 0 {
		return ErrInvalidMinJitter
	}
	if c.MaxJitter <= c.MinJitter {
		return ErrInvalidMaxJitter
	}
	return nil
}

// BackoffConfig validation errors.
var (
	ErrInvalidBaseBackoff = errors.New("base backoff must be positive")
	ErrInvalidMaxBackoff  = errors.New("max backoff must be positive")
	ErrInvalidMinJitter   = errors.New("min jitter must be non-negative")
	ErrInvalidMaxJitter   = errors.New("max jitter must be greater than min jitter")
)
