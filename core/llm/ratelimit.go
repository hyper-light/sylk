package llm

import (
	"net/http"
	"sync"
	"time"
)

type RateLimitState int

const (
	RateLimitOK RateLimitState = iota
	RateLimitWarning
	RateLimitExceeded
)

func (s RateLimitState) String() string {
	switch s {
	case RateLimitOK:
		return "OK"
	case RateLimitWarning:
		return "Warning"
	case RateLimitExceeded:
		return "Exceeded"
	default:
		return "Unknown"
	}
}

type SignalBus interface {
	Emit(signal string, payload any)
}

type SoftLimitConfig struct {
	MaxRequests   int
	MaxTokens     int
	Period        time.Duration
	WarnThreshold float64
}

func DefaultSoftLimitConfig() SoftLimitConfig {
	return SoftLimitConfig{
		MaxRequests:   1000,
		MaxTokens:     100000,
		Period:        time.Hour,
		WarnThreshold: 0.8,
	}
}

type ProviderRateLimiter struct {
	mu sync.Mutex

	state         RateLimitState
	backoffConfig BackoffConfig
	softLimit     SoftLimitConfig

	attempt      int
	backoffUntil time.Time

	periodStart  time.Time
	requestCount int
	tokenCount   int

	signalBus    SignalBus
	providerName string
}

type RateLimiterOption func(*ProviderRateLimiter)

func WithBackoffConfig(cfg BackoffConfig) RateLimiterOption {
	return func(r *ProviderRateLimiter) {
		r.backoffConfig = cfg
	}
}

func WithSoftLimitConfig(cfg SoftLimitConfig) RateLimiterOption {
	return func(r *ProviderRateLimiter) {
		r.softLimit = cfg
	}
}

func WithSignalBus(bus SignalBus) RateLimiterOption {
	return func(r *ProviderRateLimiter) {
		r.signalBus = bus
	}
}

func NewProviderRateLimiter(providerName string, opts ...RateLimiterOption) *ProviderRateLimiter {
	r := &ProviderRateLimiter{
		state:         RateLimitOK,
		backoffConfig: DefaultBackoffConfig(),
		softLimit:     DefaultSoftLimitConfig(),
		periodStart:   time.Now(),
		providerName:  providerName,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func (r *ProviderRateLimiter) OnResponse(statusCode int, headers http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if statusCode == http.StatusTooManyRequests {
		r.handleRateLimitExceeded(headers)
		return
	}

	if statusCode >= 200 && statusCode < 300 {
		r.handleSuccess()
	}
}

func (r *ProviderRateLimiter) handleRateLimitExceeded(headers http.Header) {
	r.state = RateLimitExceeded
	backoff := DetermineBackoff(r.backoffConfig, r.attempt, headers)
	r.backoffUntil = time.Now().Add(backoff)
	r.attempt++

	r.emitPauseSignal(backoff)
}

func (r *ProviderRateLimiter) handleSuccess() {
	r.attempt = 0

	if r.state == RateLimitExceeded {
		r.state = RateLimitOK
		r.emitResumeSignal()
	}
}

func (r *ProviderRateLimiter) CanProceed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != RateLimitExceeded {
		return true
	}

	if time.Now().After(r.backoffUntil) {
		r.state = RateLimitOK
		r.emitResumeSignal()
		return true
	}

	return false
}

func (r *ProviderRateLimiter) RecordUsage(tokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resetPeriodIfExpired()

	r.requestCount++
	r.tokenCount += tokens

	r.updateWarningState()
}

func (r *ProviderRateLimiter) resetPeriodIfExpired() {
	if time.Since(r.periodStart) >= r.softLimit.Period {
		r.periodStart = time.Now()
		r.requestCount = 0
		r.tokenCount = 0
		if r.state == RateLimitWarning {
			r.state = RateLimitOK
		}
	}
}

func (r *ProviderRateLimiter) updateWarningState() {
	if r.state == RateLimitExceeded {
		return
	}

	requestRatio := float64(r.requestCount) / float64(r.softLimit.MaxRequests)
	tokenRatio := float64(r.tokenCount) / float64(r.softLimit.MaxTokens)

	if requestRatio >= r.softLimit.WarnThreshold || tokenRatio >= r.softLimit.WarnThreshold {
		if r.state != RateLimitWarning {
			r.state = RateLimitWarning
			r.emitWarningSignal()
		}
	}
}

func (r *ProviderRateLimiter) State() RateLimitState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *ProviderRateLimiter) BackoffRemaining() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != RateLimitExceeded {
		return 0
	}

	remaining := time.Until(r.backoffUntil)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (r *ProviderRateLimiter) emitPauseSignal(backoff time.Duration) {
	if r.signalBus == nil {
		return
	}
	r.signalBus.Emit("SignalPauseAll", map[string]any{
		"provider": r.providerName,
		"resumeAt": r.backoffUntil,
		"attempt":  r.attempt,
		"backoff":  backoff,
	})
}

func (r *ProviderRateLimiter) emitResumeSignal() {
	if r.signalBus == nil {
		return
	}
	r.signalBus.Emit("SignalResumeAll", map[string]any{
		"provider": r.providerName,
	})
}

func (r *ProviderRateLimiter) emitWarningSignal() {
	if r.signalBus == nil {
		return
	}
	r.signalBus.Emit("SignalQuotaWarning", map[string]any{
		"provider":     r.providerName,
		"requestCount": r.requestCount,
		"tokenCount":   r.tokenCount,
		"maxRequests":  r.softLimit.MaxRequests,
		"maxTokens":    r.softLimit.MaxTokens,
	})
}
