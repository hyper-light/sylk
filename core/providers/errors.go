package providers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Common provider errors
var (
	ErrProviderNotFound    = errors.New("provider not found")
	ErrModelNotSupported   = errors.New("model not supported")
	ErrInvalidConfig       = errors.New("invalid configuration")
	ErrRateLimited         = errors.New("rate limited")
	ErrContextCanceled     = errors.New("context canceled")
	ErrStreamInterrupted   = errors.New("stream interrupted")
	ErrInvalidResponse     = errors.New("invalid response from provider")
	ErrAuthenticationError = errors.New("authentication failed")
	ErrQuotaExceeded       = errors.New("quota exceeded")
	ErrContentFiltered     = errors.New("content filtered by safety settings")
)

// ProviderError wraps errors with provider-specific context
type ProviderError struct {
	Provider    ProviderType
	Operation   string
	StatusCode  int
	Message     string
	Retryable   bool
	RetryAfter  time.Duration
	Underlying  error
}

func (e *ProviderError) Error() string {
	var sb strings.Builder
	sb.WriteString(string(e.Provider))
	sb.WriteString(" ")
	sb.WriteString(e.Operation)
	sb.WriteString(": ")
	sb.WriteString(e.Message)
	if e.StatusCode > 0 {
		sb.WriteString(fmt.Sprintf(" (status %d)", e.StatusCode))
	}
	if e.Underlying != nil {
		sb.WriteString(": ")
		sb.WriteString(e.Underlying.Error())
	}
	return sb.String()
}

func (e *ProviderError) Unwrap() error {
	return e.Underlying
}

// Is implements errors.Is for common error types
func (e *ProviderError) Is(target error) bool {
	switch {
	case errors.Is(target, ErrRateLimited):
		return e.StatusCode == http.StatusTooManyRequests
	case errors.Is(target, ErrAuthenticationError):
		return e.StatusCode == http.StatusUnauthorized
	case errors.Is(target, ErrQuotaExceeded):
		return e.StatusCode == http.StatusPaymentRequired || e.StatusCode == http.StatusForbidden
	}
	return false
}

// NewProviderError creates a new provider error
func NewProviderError(provider ProviderType, operation string, err error) *ProviderError {
	pe := &ProviderError{
		Provider:   provider,
		Operation:  operation,
		Message:    err.Error(),
		Underlying: err,
	}

	// Attempt to extract status code and retry info from error
	pe.parseError(err)

	return pe
}

// parseError extracts information from provider-specific errors
func (e *ProviderError) parseError(err error) {
	errStr := err.Error()

	// Check for common patterns
	switch {
	case strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429"):
		e.StatusCode = http.StatusTooManyRequests
		e.Retryable = true
		e.RetryAfter = 60 * time.Second // Default retry delay

	case strings.Contains(errStr, "unauthorized") || strings.Contains(errStr, "401"):
		e.StatusCode = http.StatusUnauthorized
		e.Retryable = false

	case strings.Contains(errStr, "quota") || strings.Contains(errStr, "402"):
		e.StatusCode = http.StatusPaymentRequired
		e.Retryable = false

	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline"):
		e.Retryable = true
		e.RetryAfter = 5 * time.Second

	case strings.Contains(errStr, "500") || strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") || strings.Contains(errStr, "504"):
		e.Retryable = true
		e.RetryAfter = 10 * time.Second

	case strings.Contains(errStr, "safety") || strings.Contains(errStr, "content_filter"):
		e.Retryable = false
	}
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable
	}

	// Check for common retryable patterns
	errStr := err.Error()
	return strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "unavailable")
}

// GetRetryAfter returns the suggested retry delay for an error
func GetRetryAfter(err error) time.Duration {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.RetryAfter
	}
	return 0
}

// WrapError wraps an error with provider context
func WrapError(provider ProviderType, operation string, err error) error {
	if err == nil {
		return nil
	}

	// Don't double-wrap
	var pe *ProviderError
	if errors.As(err, &pe) {
		return err
	}

	return NewProviderError(provider, operation, err)
}
