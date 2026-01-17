package llm

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/signal"
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

type RateLimiterOption func(*ProviderRateLimiter)

func WithSignalBus(bus *signal.SignalBus) RateLimiterOption {
	return func(r *ProviderRateLimiter) {
		r.signalBus = bus
	}
}

func WithBaseBackoff(base time.Duration) RateLimiterOption {
	return func(r *ProviderRateLimiter) {
		r.baseBackoff = base
	}
}

func WithMaxBackoff(max time.Duration) RateLimiterOption {
	return func(r *ProviderRateLimiter) {
		r.maxBackoff = max
	}
}

func WithSoftLimit(limit *UsageLimit) RateLimiterOption {
	return func(r *ProviderRateLimiter) {
		r.softLimit = limit
	}
}

func NewProviderRateLimiter(provider string, softLimit *UsageLimit, bus *signal.SignalBus, opts ...RateLimiterOption) *ProviderRateLimiter {
	r := &ProviderRateLimiter{
		provider:    provider,
		state:       RateLimitOK,
		baseBackoff: time.Second,
		maxBackoff:  5 * time.Minute,
		periodStart: time.Now(),
		softLimit:   softLimit,
		signalBus:   bus,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

type ProviderRateLimiter struct {
	mu sync.RWMutex

	provider       string
	state          RateLimitState
	backoffUntil   time.Time
	backoffAttempt int
	baseBackoff    time.Duration
	maxBackoff     time.Duration

	requestCount int64
	tokenCount   int64
	periodStart  time.Time
	softLimit    *UsageLimit

	signalBus *signal.SignalBus
}

func (r *ProviderRateLimiter) OnResponse(statusCode int, headers http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if statusCode == http.StatusTooManyRequests {
		r.state = RateLimitExceeded
		r.backoffAttempt++

		backoff := r.baseBackoff * time.Duration(1<<r.backoffAttempt)
		if backoff > r.maxBackoff {
			backoff = r.maxBackoff
		}

		if retryAfter := headers.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				backoff = time.Duration(seconds) * time.Second
			}
		}

		r.backoffUntil = time.Now().Add(backoff)
		r.broadcastPause(backoff)
		return
	}

	if statusCode >= 200 && statusCode < 300 {
		if r.backoffAttempt > 0 {
			r.backoffAttempt = 0
			r.state = RateLimitOK
		}
	}
}

func (r *ProviderRateLimiter) CanProceed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state == RateLimitExceeded {
		if time.Now().After(r.backoffUntil) {
			r.state = RateLimitOK
			r.broadcastResume()
			return true
		}
		return false
	}

	if r.softLimit != nil {
		r.checkSoftLimit()
	}

	return true
}

func (r *ProviderRateLimiter) checkSoftLimit() {
	if r.softLimit == nil {
		return
	}

	if time.Since(r.periodStart) > r.softLimit.Period {
		r.requestCount = 0
		r.tokenCount = 0
		r.periodStart = time.Now()
	}

	if r.state == RateLimitExceeded {
		return
	}

	if r.softLimit.Requests > 0 {
		requestRatio := float64(r.requestCount) / float64(r.softLimit.Requests)
		if requestRatio >= r.softLimit.WarnAt {
			if r.state != RateLimitWarning {
				r.state = RateLimitWarning
			}
			r.broadcastWarning(requestRatio, float64(r.tokenCount)/float64(maxInt64(int64(r.softLimit.Tokens))))
			return
		}
	}

	if r.softLimit.Tokens > 0 {
		tokenRatio := float64(r.tokenCount) / float64(r.softLimit.Tokens)
		if tokenRatio >= r.softLimit.WarnAt {
			if r.state != RateLimitWarning {
				r.state = RateLimitWarning
			}
			r.broadcastWarning(float64(r.requestCount)/float64(maxInt64(int64(r.softLimit.Requests))), tokenRatio)
			return
		}
	}

	if r.state == RateLimitWarning {
		r.state = RateLimitOK
	}
}

func (r *ProviderRateLimiter) RecordUsage(tokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.requestCount++
	r.tokenCount += int64(tokens)

	r.checkSoftLimit()
}

func (r *ProviderRateLimiter) State() RateLimitState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *ProviderRateLimiter) broadcastPause(backoff time.Duration) {
	if r.signalBus == nil {
		return
	}

	msg := signal.NewSignalMessage(signal.PauseAll, "", true)
	msg.Reason = fmt.Sprintf("Rate limited by %s", r.provider)
	msg.Payload = signal.PausePayload{
		Provider: r.provider,
		ResumeAt: &r.backoffUntil,
		Attempt:  r.backoffAttempt,
	}
	msg.Timeout = 5 * time.Second

	_ = r.signalBus.Broadcast(*msg)
}

func (r *ProviderRateLimiter) broadcastResume() {
	if r.signalBus == nil {
		return
	}

	msg := signal.NewSignalMessage(signal.ResumeAll, "", true)
	msg.Reason = fmt.Sprintf("Rate limit backoff complete for %s", r.provider)
	msg.Timeout = 5 * time.Second

	_ = r.signalBus.Broadcast(*msg)
}

func (r *ProviderRateLimiter) broadcastWarning(requestRatio, tokenRatio float64) {
	if r.signalBus == nil {
		return
	}

	msg := signal.NewSignalMessage(signal.QuotaWarning, "", false)
	msg.Reason = fmt.Sprintf("Approaching %s quota: %.0f%% requests, %.0f%% tokens",
		r.provider, requestRatio*100, tokenRatio*100)

	_ = r.signalBus.Broadcast(*msg)
}

func maxInt64(value int64) int64 {
	if value <= 0 {
		return 1
	}
	return value
}
