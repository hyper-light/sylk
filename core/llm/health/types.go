package health

import (
	"context"
	"strings"
	"time"
)

// HealthConfig configures health monitoring behavior.
type HealthConfig struct {
	// Active probing
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	ProbeEndpoint string // Lightweight endpoint to probe

	// Passive monitoring
	WindowSize time.Duration
	MinSamples int

	// Score weights
	ActiveWeight  float64 // 0-1
	PassiveWeight float64 // 0-1, should sum to 1 with ActiveWeight

	// Thresholds
	RejectThreshold  float64 // Score below this -> reject requests
	WarnThreshold    float64 // Score below this -> warn
	MonitorThreshold float64 // Score below this -> increased monitoring
}

// DefaultHealthConfig returns a HealthConfig with sensible defaults.
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{
		ProbeInterval:    30 * time.Second,
		ProbeTimeout:     5 * time.Second,
		ActiveWeight:     0.4,
		PassiveWeight:    0.6,
		WindowSize:       5 * time.Minute,
		MinSamples:       10,
		RejectThreshold:  0.3,
		WarnThreshold:    0.5,
		MonitorThreshold: 0.7,
	}
}

// HealthDecision represents the result of a health check.
type HealthDecision struct {
	Proceed bool
	Score   float64
	Status  HealthStatus
	Reason  string
}

// HealthStatus represents the health state of a provider.
type HealthStatus int

const (
	HealthHealthy HealthStatus = iota
	HealthMonitored
	HealthDegraded
	HealthDead
)

// healthStatusNames maps HealthStatus values to their string representations.
var healthStatusNames = map[HealthStatus]string{
	HealthHealthy:   "healthy",
	HealthMonitored: "monitored",
	HealthDegraded:  "degraded",
	HealthDead:      "dead",
}

// String returns a string representation of the HealthStatus.
func (s HealthStatus) String() string {
	if name, ok := healthStatusNames[s]; ok {
		return name
	}
	return "unknown"
}

// ProbeFunc performs a lightweight health check.
type ProbeFunc func(ctx context.Context) error

// ErrorCategory for categorizing errors.
type ErrorCategory int

const (
	ErrNone ErrorCategory = iota
	ErrTransient
	ErrRateLimit
	ErrAuth
	ErrPermanent
)

// errorCategoryNames maps ErrorCategory values to their string representations.
var errorCategoryNames = map[ErrorCategory]string{
	ErrNone:      "none",
	ErrTransient: "transient",
	ErrRateLimit: "rate_limit",
	ErrAuth:      "auth",
	ErrPermanent: "permanent",
}

// String returns a string representation of the ErrorCategory.
func (c ErrorCategory) String() string {
	if name, ok := errorCategoryNames[c]; ok {
		return name
	}
	return "unknown"
}

// errorCategorizer defines a function that checks if an error string matches a category.
type errorCategorizer struct {
	check    func(string) bool
	category ErrorCategory
}

// isRateLimitError checks if the error string indicates rate limiting.
func isRateLimitError(errStr string) bool {
	return strings.Contains(errStr, "429") || strings.Contains(errStr, "rate")
}

// isAuthError checks if the error string indicates authentication failure.
func isAuthError(errStr string) bool {
	return strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "auth") ||
		strings.Contains(errStr, "key")
}

// isTransientError checks if the error string indicates a transient failure.
func isTransientError(errStr string) bool {
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "503")
}

// errorCategorizers defines the order of error categorization checks.
// Rate limit is checked first, then auth, then transient.
var errorCategorizers = []errorCategorizer{
	{isRateLimitError, ErrRateLimit},
	{isAuthError, ErrAuth},
	{isTransientError, ErrTransient},
}

// categorizeErrorString categorizes a lowercase error string.
func categorizeErrorString(errStr string) ErrorCategory {
	for _, ec := range errorCategorizers {
		if ec.check(errStr) {
			return ec.category
		}
	}
	return ErrPermanent
}

// CategorizeError categorizes an error for health tracking.
func CategorizeError(err error) ErrorCategory {
	if err == nil {
		return ErrNone
	}
	return categorizeErrorString(strings.ToLower(err.Error()))
}
