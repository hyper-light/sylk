package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
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

// UserMessage returns a concise, human-readable error description suitable for
// display in the chat panel. It extracts the nested "message" field from JSON
// error bodies (common with Google/OpenAI APIs) and maps HTTP status codes to
// friendly labels.
func (e *ProviderError) UserMessage() string {
	label := httpStatusLabel(e.StatusCode)

	// Try to extract a human-readable message from embedded JSON.
	if extracted := extractJSONMessage(e.Message); extracted != "" {
		if label != "" {
			return label + " — " + extracted
		}
		return extracted
	}

	// Strip any trailing "(status NNN)" since we include the label.
	msg := e.Message
	if suffix := fmt.Sprintf(" (status %d)", e.StatusCode); strings.HasSuffix(msg, suffix) {
		msg = strings.TrimSuffix(msg, suffix)
	}

	// Strip the JSON body if we couldn't parse it but have a prefix.
	if idx := strings.Index(msg, "{"); idx > 0 {
		msg = strings.TrimSpace(msg[:idx])
	}

	if label != "" && msg != "" {
		return label + " — " + msg
	}
	if label != "" {
		return label
	}
	return msg
}

// httpStatusLabel returns a short human label for common HTTP error codes.
func httpStatusLabel(code int) string {
	switch code {
	case http.StatusTooManyRequests:
		return "Rate limited"
	case http.StatusUnauthorized:
		return "Authentication failed"
	case http.StatusForbidden:
		return "Access denied"
	case http.StatusPaymentRequired:
		return "Quota exceeded"
	case http.StatusBadRequest:
		return "Bad request"
	case http.StatusNotFound:
		return "Endpoint not found"
	case http.StatusServiceUnavailable:
		return "Service unavailable"
	case http.StatusGatewayTimeout:
		return "Gateway timeout"
	case http.StatusInternalServerError:
		return "Server error"
	default:
		if code >= 500 {
			return "Server error"
		}
		return ""
	}
}

// extractJSONMessage finds an embedded JSON object in s and returns the
// "error.message" field if present. Handles the common Google/OpenAI
// envelope format: {"error":{"message":"..."}}.
func extractJSONMessage(s string) string {
	idx := strings.Index(s, "{")
	if idx < 0 {
		return ""
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s[idx:]), &envelope) == nil && envelope.Error.Message != "" {
		return envelope.Error.Message
	}
	return ""
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

// parseError extracts information from provider-specific errors using typed
// error inspection rather than string matching.
func (e *ProviderError) parseError(err error) {
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		e.StatusCode = anthropicErr.StatusCode
		e.Retryable = isRetryableHTTPStatus(anthropicErr.StatusCode)
		return
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		e.Retryable = netErr.Timeout()
		return
	}
}

// IsRetryable checks if an error is retryable using typed error inspection.
func IsRetryable(err error) bool {
	return isRetryableError(err)
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
