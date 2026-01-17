package llm

import (
	"net/http"
	"strconv"
	"time"
)

// BackoffConfig holds configuration for exponential backoff.
type BackoffConfig struct {
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// DefaultBackoffConfig returns the default backoff configuration.
func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		BaseBackoff: time.Second,
		MaxBackoff:  5 * time.Minute,
	}
}

// CalculateBackoff computes exponential backoff: min(base * 2^attempt, max).
func CalculateBackoff(cfg BackoffConfig, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	backoff := cfg.BaseBackoff << attempt
	if backoff > cfg.MaxBackoff || backoff <= 0 {
		return cfg.MaxBackoff
	}
	return backoff
}

// ParseRetryAfter extracts retry duration from HTTP headers.
// Returns zero duration if header is absent or unparseable.
func ParseRetryAfter(headers http.Header) time.Duration {
	value := headers.Get("Retry-After")
	if value == "" {
		return 0
	}

	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second
	}

	if t, err := http.ParseTime(value); err == nil {
		return time.Until(t)
	}

	return 0
}

// DetermineBackoff returns Retry-After value if present, otherwise calculated backoff.
func DetermineBackoff(cfg BackoffConfig, attempt int, headers http.Header) time.Duration {
	if retryAfter := ParseRetryAfter(headers); retryAfter > 0 {
		return retryAfter
	}
	return CalculateBackoff(cfg, attempt)
}
