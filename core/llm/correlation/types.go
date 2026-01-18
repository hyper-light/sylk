package correlation

import "time"

// ErrorCategory categorizes errors for correlation analysis.
type ErrorCategory int

const (
	// ErrCategoryNone indicates no error or uncategorized.
	ErrCategoryNone ErrorCategory = iota
	// ErrCategoryTransient indicates a temporary error that may resolve.
	ErrCategoryTransient
	// ErrCategoryRateLimit indicates rate limiting from the provider.
	ErrCategoryRateLimit
	// ErrCategoryAuth indicates authentication/authorization failure.
	ErrCategoryAuth
	// ErrCategoryPermanent indicates a non-recoverable error.
	ErrCategoryPermanent
)

// errorCategoryStrings maps ErrorCategory values to their string representations.
var errorCategoryStrings = map[ErrorCategory]string{
	ErrCategoryNone:      "none",
	ErrCategoryTransient: "transient",
	ErrCategoryRateLimit: "rate_limit",
	ErrCategoryAuth:      "auth",
	ErrCategoryPermanent: "permanent",
}

// String returns the string representation of an ErrorCategory.
func (ec ErrorCategory) String() string {
	if s, ok := errorCategoryStrings[ec]; ok {
		return s
	}
	return "unknown"
}

// IsRetryable returns whether errors in this category can be retried.
func (ec ErrorCategory) IsRetryable() bool {
	return ec == ErrCategoryTransient || ec == ErrCategoryRateLimit
}

// CorrelationConfig configures failure correlation behavior.
type CorrelationConfig struct {
	// CorrelationWindow is the time window to detect correlation.
	CorrelationWindow time.Duration
	// MinFailuresForGlobal is the minimum failures across sessions to trigger global backoff.
	MinFailuresForGlobal int
	// GlobalBackoffBase is the base duration for global backoff.
	GlobalBackoffBase time.Duration
	// GlobalBackoffMax is the maximum global backoff duration.
	GlobalBackoffMax time.Duration
	// RecoveryRampUp is the percentage of traffic to allow after backoff (0.0-1.0).
	RecoveryRampUp float64
}

// DefaultCorrelationConfig returns sensible default configuration values.
func DefaultCorrelationConfig() CorrelationConfig {
	return CorrelationConfig{
		CorrelationWindow:    10 * time.Second,
		MinFailuresForGlobal: 3,
		GlobalBackoffBase:    5 * time.Second,
		GlobalBackoffMax:     60 * time.Second,
		RecoveryRampUp:       0.1, // Allow 10% of traffic initially
	}
}

// Validate checks if the configuration values are valid.
func (c CorrelationConfig) Validate() error {
	if err := c.validateWindow(); err != nil {
		return err
	}
	if err := c.validateBackoff(); err != nil {
		return err
	}
	return c.validateRecovery()
}

// validateWindow validates the correlation window configuration.
func (c CorrelationConfig) validateWindow() error {
	if c.CorrelationWindow < 0 {
		return ErrInvalidCorrelationWindow
	}
	if c.MinFailuresForGlobal < 1 {
		return ErrInvalidMinFailures
	}
	return nil
}

// validateBackoff validates the backoff configuration.
func (c CorrelationConfig) validateBackoff() error {
	if c.GlobalBackoffBase < 0 {
		return ErrInvalidBackoffBase
	}
	if c.GlobalBackoffMax < c.GlobalBackoffBase {
		return ErrInvalidBackoffMax
	}
	return nil
}

// validateRecovery validates the recovery configuration.
func (c CorrelationConfig) validateRecovery() error {
	if c.RecoveryRampUp < 0 || c.RecoveryRampUp > 1 {
		return ErrInvalidRecoveryRampUp
	}
	return nil
}

// FailureEvent represents a single failure occurrence.
type FailureEvent struct {
	// Timestamp is when the failure occurred.
	Timestamp time.Time
	// SessionID identifies the session where the failure occurred.
	SessionID string
	// ErrorType categorizes the failure.
	ErrorType ErrorCategory
}

// NewFailureEvent creates a new FailureEvent with the current timestamp.
func NewFailureEvent(sessionID string, errorType ErrorCategory) FailureEvent {
	return FailureEvent{
		Timestamp: time.Now(),
		SessionID: sessionID,
		ErrorType: errorType,
	}
}

// IsWithinWindow checks if the event occurred within the given duration from now.
func (fe FailureEvent) IsWithinWindow(window time.Duration) bool {
	cutoff := time.Now().Add(-window)
	return fe.Timestamp.After(cutoff)
}

// SignalType represents the type of coordination signal.
type SignalType int

const (
	// SignalGlobalBackoff indicates provider-wide backoff should be applied.
	SignalGlobalBackoff SignalType = iota
	// SignalGlobalRecovery indicates the provider has recovered.
	SignalGlobalRecovery
)

// String returns the string representation of a SignalType.
func (st SignalType) String() string {
	switch st {
	case SignalGlobalBackoff:
		return "global_backoff"
	case SignalGlobalRecovery:
		return "global_recovery"
	default:
		return "unknown"
	}
}

// Signal is the interface for coordination signals.
type Signal interface {
	// Type returns the signal type.
	Type() SignalType
	// Provider returns the provider this signal applies to.
	Provider() string
}

// SignalDispatcher broadcasts backoff and recovery signals.
type SignalDispatcher interface {
	// SendSignal broadcasts a signal to all listeners.
	SendSignal(signal Signal)
}

// GlobalBackoffSignal indicates provider-wide backoff.
type GlobalBackoffSignal struct {
	provider string
	// Duration is how long the backoff should last.
	Duration time.Duration
}

// NewGlobalBackoffSignal creates a new GlobalBackoffSignal.
func NewGlobalBackoffSignal(provider string, duration time.Duration) *GlobalBackoffSignal {
	return &GlobalBackoffSignal{
		provider: provider,
		Duration: duration,
	}
}

// Type returns SignalGlobalBackoff.
func (s *GlobalBackoffSignal) Type() SignalType {
	return SignalGlobalBackoff
}

// Provider returns the provider this signal applies to.
func (s *GlobalBackoffSignal) Provider() string {
	return s.provider
}

// GlobalRecoverySignal indicates provider recovery.
type GlobalRecoverySignal struct {
	provider string
}

// NewGlobalRecoverySignal creates a new GlobalRecoverySignal.
func NewGlobalRecoverySignal(provider string) *GlobalRecoverySignal {
	return &GlobalRecoverySignal{provider: provider}
}

// Type returns SignalGlobalRecovery.
func (s *GlobalRecoverySignal) Type() SignalType {
	return SignalGlobalRecovery
}

// Provider returns the provider this signal applies to.
func (s *GlobalRecoverySignal) Provider() string {
	return s.provider
}
