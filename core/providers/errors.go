package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
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
	Provider   ProviderType
	Operation  string
	StatusCode int
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Underlying error

	// ContextTooLarge is true when the request exceeded the model's context
	// window. ContextUsedTokens and ContextMaxTokens provide the extracted
	// token counts when available (zero otherwise).
	ContextTooLarge   bool
	ContextUsedTokens int
	ContextMaxTokens  int
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
	if e.Underlying != nil && e.Underlying.Error() != e.Message {
		sb.WriteString(": ")
		sb.WriteString(e.Underlying.Error())
	}
	return sb.String()
}

func (e *ProviderError) Unwrap() error {
	return e.Underlying
}

// UserMessage returns a human-readable error description suitable for display
// in the chat panel. It extracts structured fields from JSON error bodies
// (common with Google/OpenAI/Anthropic APIs), maps HTTP status codes to
// friendly labels, and preserves error type and request ID for debugging.
func (e *ProviderError) UserMessage() string {
	label := httpStatusLabel(e.StatusCode)

	// Try to extract structured detail from embedded JSON.
	if detail := extractJSONErrorDetail(e.Message); detail.Message != "" {
		formatted := detail.format()
		if label != "" {
			return label + " — " + formatted
		}
		return formatted
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

// jsonErrorDetail holds the structured fields extracted from a provider
// error JSON envelope. All three providers (Anthropic, Google, OpenAI)
// use {"error":{"message":"..."}}, with additional fields varying.
type jsonErrorDetail struct {
	Message   string // error.message (all providers)
	ErrorType string // error.type (Anthropic/OpenAI)
	RequestID string // request_id (Anthropic top-level)
}

// format returns a human-readable string that includes all available detail.
// The message is always shown; error type and request ID are appended when present.
func (d jsonErrorDetail) format() string {
	if d.Message == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(d.Message)
	if d.ErrorType != "" {
		b.WriteString(" (")
		b.WriteString(d.ErrorType)
		b.WriteString(")")
	}
	if d.RequestID != "" {
		b.WriteString(" [")
		b.WriteString(d.RequestID)
		b.WriteString("]")
	}
	return b.String()
}

// extractJSONErrorDetail finds an embedded JSON object in s and returns the
// structured error fields. Handles the common Google/OpenAI/Anthropic
// envelope format: {"error":{"message":"...", "type":"..."}, "request_id":"..."}.
// Uses json.Decoder so it tolerates trailing non-JSON text after the object.
func extractJSONErrorDetail(s string) jsonErrorDetail {
	idx := strings.Index(s, "{")
	if idx < 0 {
		return jsonErrorDetail{}
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	dec := json.NewDecoder(strings.NewReader(s[idx:]))
	if dec.Decode(&envelope) == nil && envelope.Error.Message != "" {
		return jsonErrorDetail{
			Message:   envelope.Error.Message,
			ErrorType: envelope.Error.Type,
			RequestID: envelope.RequestID,
		}
	}
	return jsonErrorDetail{}
}

// extractJSONMessage returns just the message field for callers that don't
// need the full detail.
func extractJSONMessage(s string) string {
	return extractJSONErrorDetail(s).Message
}

// FriendlyErrorMessage extracts a human-readable message from any error
// in the chain. Safe to call with any error type.
func FriendlyErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	// 1. ProviderError — already has UserMessage().
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.UserMessage()
	}

	// 2. Anthropic SDK error — has StatusCode and structured fields.
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		label := httpStatusLabel(ae.StatusCode)
		if detail := extractJSONErrorDetail(ae.Error()); detail.Message != "" {
			formatted := detail.format()
			if label != "" {
				return label + " — " + formatted
			}
			return formatted
		}
		if label != "" {
			return label
		}
	}

	// 3. Scan raw text for embedded JSON error envelope.
	raw := err.Error()
	if detail := extractJSONErrorDetail(raw); detail.Message != "" {
		return detail.format()
	}

	// 4. Strip internal wrapping prefixes for a cleaner fallback.
	return cleanErrorPrefixes(raw)
}

// cleanErrorPrefixes strips internal wrapping prefixes like
// "planning protocol: architect llm: anthropic stream: " that are
// meaningless to users.
func cleanErrorPrefixes(s string) string {
	prefixes := []string{
		"planning protocol: ",
		"architect llm: ",
		"architect planner: ",
		"anthropic stream: ",
		"received error while streaming: ",
	}
	cleaned := s
	for _, p := range prefixes {
		cleaned = strings.TrimPrefix(cleaned, p)
	}
	// Strip the "(conversation unavailable: ...)" suffix.
	if idx := strings.Index(cleaned, " (conversation unavailable:"); idx > 0 {
		cleaned = cleaned[:idx]
	}
	return strings.TrimSpace(cleaned)
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
		e.Retryable = isRetryableHTTPStatus(anthropicErr.StatusCode) || isAnthropicOverloadedError(anthropicErr)
		if anthropicErr.Response != nil {
			if d, ok := parseRetryAfterHeader(anthropicErr.Response.Header); ok {
				e.RetryAfter = d
			}
		}
		parseContextTooLarge(e.Message, e)
		return
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		e.Retryable = netErr.Timeout() || isRetryableTransportError(err)
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

// googleContextPattern matches Google API error messages reporting input token
// count exceeding the model's maximum. Captures the used and max token counts.
var googleContextPattern = regexp.MustCompile(
	`(?i)input token count[:\s]+(\d+).*?exceed[s]?.*?maximum[:\s]+(\d+)`,
)

// anthropicContextPattern matches Anthropic API error messages reporting
// prompt length exceeding the model's context window. Example:
// "prompt is too long: 123456 tokens > 100000 maximum"
var anthropicContextPattern = regexp.MustCompile(
	`(?i)prompt is too long[:\s]+(\d+)\s+tokens?\s*>\s*(\d+)`,
)

// parseContextTooLarge checks an error message for context window overflow
// and populates the ProviderError fields when detected. Checks both Google
// and Anthropic error message formats.
func parseContextTooLarge(message string, pe *ProviderError) {
	if matches := googleContextPattern.FindStringSubmatch(message); matches != nil {
		pe.ContextTooLarge = true
		pe.ContextUsedTokens, _ = strconv.Atoi(matches[1])
		pe.ContextMaxTokens, _ = strconv.Atoi(matches[2])
		return
	}
	if matches := anthropicContextPattern.FindStringSubmatch(message); matches != nil {
		pe.ContextTooLarge = true
		pe.ContextUsedTokens, _ = strconv.Atoi(matches[1])
		pe.ContextMaxTokens, _ = strconv.Atoi(matches[2])
		return
	}
}

// IsContextTooLarge returns true if the error chain contains a ProviderError
// with the ContextTooLarge flag set.
func IsContextTooLarge(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.ContextTooLarge
	}
	return false
}
