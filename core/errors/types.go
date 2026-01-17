// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrorTier represents the classification tier for errors.
// Each tier has defined behavior for retry policy, notification, and escalation.
type ErrorTier int

const (
	// TierTransient indicates temporary errors that should be silently retried.
	// Examples: network timeouts, temporary unavailability.
	TierTransient ErrorTier = iota

	// TierPermanent indicates errors that will not resolve with retry.
	// Examples: invalid input, authentication failure, not found.
	TierPermanent

	// TierUserFixable indicates errors that require user intervention.
	// Examples: missing configuration, invalid credentials provided by user.
	TierUserFixable

	// TierExternalRateLimit indicates rate limiting from external services.
	// Examples: API rate limits, quota exceeded.
	TierExternalRateLimit

	// TierExternalDegrading indicates external service degradation.
	// Examples: 5xx errors, slow responses, partial failures.
	TierExternalDegrading
)

var tierNames = map[ErrorTier]string{
	TierTransient:         "transient",
	TierPermanent:         "permanent",
	TierUserFixable:       "user_fixable",
	TierExternalRateLimit: "external_rate_limit",
	TierExternalDegrading: "external_degrading",
}

func (t ErrorTier) String() string {
	if name, ok := tierNames[t]; ok {
		return name
	}
	return "unknown"
}

// TierBehavior defines the handling behavior for an error tier.
type TierBehavior struct {
	// ShouldRetry indicates whether errors of this tier should be retried.
	ShouldRetry bool

	// MaxRetries is the maximum number of retry attempts.
	MaxRetries int

	// BaseBackoff is the initial backoff duration.
	BaseBackoff time.Duration

	// MaxBackoff is the maximum backoff duration.
	MaxBackoff time.Duration

	// ShouldNotify indicates whether to notify the user.
	ShouldNotify bool

	// ShouldEscalate indicates whether to escalate to a higher-level handler.
	ShouldEscalate bool
}

// DefaultBehaviors returns the default behavior for each error tier.
func DefaultBehaviors() map[ErrorTier]TierBehavior {
	return map[ErrorTier]TierBehavior{
		TierTransient: {
			ShouldRetry:    true,
			MaxRetries:     3,
			BaseBackoff:    100 * time.Millisecond,
			MaxBackoff:     5 * time.Second,
			ShouldNotify:   false,
			ShouldEscalate: false,
		},
		TierPermanent: {
			ShouldRetry:    false,
			MaxRetries:     0,
			BaseBackoff:    0,
			MaxBackoff:     0,
			ShouldNotify:   true,
			ShouldEscalate: true,
		},
		TierUserFixable: {
			ShouldRetry:    false,
			MaxRetries:     0,
			BaseBackoff:    0,
			MaxBackoff:     0,
			ShouldNotify:   true,
			ShouldEscalate: false,
		},
		TierExternalRateLimit: {
			ShouldRetry:    true,
			MaxRetries:     5,
			BaseBackoff:    1 * time.Second,
			MaxBackoff:     60 * time.Second,
			ShouldNotify:   true,
			ShouldEscalate: false,
		},
		TierExternalDegrading: {
			ShouldRetry:    true,
			MaxRetries:     3,
			BaseBackoff:    500 * time.Millisecond,
			MaxBackoff:     30 * time.Second,
			ShouldNotify:   true,
			ShouldEscalate: true,
		},
	}
}

// TieredError wraps an error with tier classification.
type TieredError struct {
	Tier       ErrorTier
	Message    string
	Underlying error
	StatusCode int
	RetryAfter time.Duration
	Context    map[string]string
}

// Error implements the error interface.
func (e *TieredError) Error() string {
	if e.Underlying != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Tier, e.Message, e.Underlying)
	}
	return fmt.Sprintf("[%s] %s", e.Tier, e.Message)
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e *TieredError) Unwrap() error {
	return e.Underlying
}

// Is checks if the target error matches this TieredError's tier.
func (e *TieredError) Is(target error) bool {
	var te *TieredError
	if errors.As(target, &te) {
		return e.Tier == te.Tier
	}
	return false
}

// NewTieredError creates a new TieredError with the given tier and message.
func NewTieredError(tier ErrorTier, message string, underlying error) *TieredError {
	return &TieredError{
		Tier:       tier,
		Message:    message,
		Underlying: underlying,
		Context:    make(map[string]string),
	}
}

// WithStatusCode adds an HTTP status code to the error.
func (e *TieredError) WithStatusCode(code int) *TieredError {
	e.StatusCode = code
	return e
}

// WithRetryAfter adds a retry-after duration to the error.
func (e *TieredError) WithRetryAfter(d time.Duration) *TieredError {
	e.RetryAfter = d
	return e
}

// WithContext adds context key-value pairs to the error.
func (e *TieredError) WithContext(key, value string) *TieredError {
	e.Context[key] = value
	return e
}

// GetTier extracts the ErrorTier from an error, defaulting to Permanent.
func GetTier(err error) ErrorTier {
	var te *TieredError
	if errors.As(err, &te) {
		return te.Tier
	}
	return TierPermanent
}

// GetBehavior returns the behavior for an error's tier.
func GetBehavior(err error) TierBehavior {
	tier := GetTier(err)
	behaviors := DefaultBehaviors()
	return behaviors[tier]
}

// IsRetryable checks if an error should be retried based on its tier.
func IsRetryable(err error) bool {
	return GetBehavior(err).ShouldRetry
}

// Common sentinel errors for each tier.
var (
	// Transient errors
	ErrTimeout          = NewTieredError(TierTransient, "operation timed out", nil)
	ErrTemporaryFailure = NewTieredError(TierTransient, "temporary failure", nil)
	ErrConnectionReset  = NewTieredError(TierTransient, "connection reset", nil)

	// Permanent errors
	ErrNotFound     = NewTieredError(TierPermanent, "not found", nil)
	ErrInvalidInput = NewTieredError(TierPermanent, "invalid input", nil)
	ErrUnauthorized = NewTieredError(TierPermanent, "unauthorized", nil)
	ErrForbidden    = NewTieredError(TierPermanent, "forbidden", nil)

	// User-fixable errors
	ErrMissingConfig      = NewTieredError(TierUserFixable, "missing configuration", nil)
	ErrInvalidCredentials = NewTieredError(TierUserFixable, "invalid credentials", nil)
	ErrMissingAPIKey      = NewTieredError(TierUserFixable, "missing API key", nil)

	// Rate limit errors
	ErrRateLimited   = NewTieredError(TierExternalRateLimit, "rate limited", nil).WithStatusCode(http.StatusTooManyRequests)
	ErrQuotaExceeded = NewTieredError(TierExternalRateLimit, "quota exceeded", nil)

	// Degrading errors
	ErrServiceUnavailable = NewTieredError(TierExternalDegrading, "service unavailable", nil).WithStatusCode(http.StatusServiceUnavailable)
	ErrBadGateway         = NewTieredError(TierExternalDegrading, "bad gateway", nil).WithStatusCode(http.StatusBadGateway)
	ErrGatewayTimeout     = NewTieredError(TierExternalDegrading, "gateway timeout", nil).WithStatusCode(http.StatusGatewayTimeout)
)

// WrapWithTier wraps an error with a tier classification.
func WrapWithTier(tier ErrorTier, message string, err error) error {
	if err == nil {
		return nil
	}

	// Don't double-wrap TieredErrors
	var te *TieredError
	if errors.As(err, &te) {
		// Preserve existing tier if wrapping
		return &TieredError{
			Tier:       te.Tier,
			Message:    message,
			Underlying: err,
			StatusCode: te.StatusCode,
			RetryAfter: te.RetryAfter,
			Context:    te.Context,
		}
	}

	return NewTieredError(tier, message, err)
}
