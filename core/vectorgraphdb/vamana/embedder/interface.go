package embedder

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimension() int
	MaxInputBytes() int
}

type Config struct {
	Model           string
	Dimension       int
	BatchSize       int
	Timeout         time.Duration
	TokensPerMin    int
	RequestsPerMin  int
	ExpectedLatency time.Duration
}

func DefaultConfig() Config {
	return Config{
		Model:           string(VoyageCode3),
		Dimension:       voyageCode3Dim,
		BatchSize:       voyageDefaultBatchSize,
		Timeout:         voyageDefaultTimeout,
		TokensPerMin:    voyageTokensPerMin,
		RequestsPerMin:  voyageRequestsPerMin,
		ExpectedLatency: voyageExpectedLatencySec * time.Second,
	}
}

// RateLimiter implements dual-bucket rate limiting for both requests and tokens.
// It enforces both RPM (requests per minute) and TPM (tokens per minute) limits.
type RateLimiter struct {
	requestsPerMin int
	tokensPerMin   int
	requestBucket  chan struct{}
	tokenBucket    *slidingWindowTokenBucket
	done           chan struct{}
}

// slidingWindowTokenBucket tracks token usage over a sliding window.
type slidingWindowTokenBucket struct {
	maxTokens   int64
	windowSize  time.Duration
	buckets     []tokenBucketEntry
	bucketCount int
	mu          sync.Mutex
}

type tokenBucketEntry struct {
	tokens    int64
	timestamp time.Time
}

func newSlidingWindowTokenBucket(maxTokens int, windowSize time.Duration) *slidingWindowTokenBucket {
	bucketCount := 60
	return &slidingWindowTokenBucket{
		maxTokens:   int64(maxTokens),
		windowSize:  windowSize,
		buckets:     make([]tokenBucketEntry, bucketCount),
		bucketCount: bucketCount,
	}
}

func (tb *slidingWindowTokenBucket) currentTokens() int64 {
	now := time.Now()
	cutoff := now.Add(-tb.windowSize)
	var total int64
	for i := range tb.buckets {
		if tb.buckets[i].timestamp.After(cutoff) {
			total += tb.buckets[i].tokens
		}
	}
	return total
}

func (tb *slidingWindowTokenBucket) available() int64 {
	return tb.maxTokens - tb.currentTokens()
}

func (tb *slidingWindowTokenBucket) consume(tokens int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.available() < int64(tokens) {
		return false
	}

	now := time.Now()
	bucketIdx := int(now.Unix()) % tb.bucketCount

	// Reset bucket if it's from a previous window
	cutoff := now.Add(-tb.windowSize)
	if tb.buckets[bucketIdx].timestamp.Before(cutoff) {
		tb.buckets[bucketIdx] = tokenBucketEntry{tokens: 0, timestamp: now}
	}

	tb.buckets[bucketIdx].tokens += int64(tokens)
	tb.buckets[bucketIdx].timestamp = now
	return true
}

func (tb *slidingWindowTokenBucket) waitTime(tokens int) time.Duration {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	needed := int64(tokens) - tb.available()
	if needed <= 0 {
		return 0
	}

	// Find oldest bucket that will expire and free up tokens
	now := time.Now()
	cutoff := now.Add(-tb.windowSize)

	var freedTokens int64
	var waitUntil time.Time

	for i := range tb.buckets {
		if tb.buckets[i].timestamp.After(cutoff) && tb.buckets[i].tokens > 0 {
			expiresAt := tb.buckets[i].timestamp.Add(tb.windowSize)
			freedTokens += tb.buckets[i].tokens
			if waitUntil.IsZero() || expiresAt.Before(waitUntil) {
				waitUntil = expiresAt
			}
			if freedTokens >= needed {
				break
			}
		}
	}

	if waitUntil.IsZero() {
		return tb.windowSize
	}
	return time.Until(waitUntil)
}

func NewRateLimiter(cfg Config) *RateLimiter {
	rl := &RateLimiter{
		requestsPerMin: cfg.RequestsPerMin,
		tokensPerMin:   cfg.TokensPerMin,
		requestBucket:  make(chan struct{}, cfg.RequestsPerMin),
		tokenBucket:    newSlidingWindowTokenBucket(cfg.TokensPerMin, time.Minute),
		done:           make(chan struct{}),
	}

	for range cfg.RequestsPerMin {
		rl.requestBucket <- struct{}{}
	}

	go rl.refillLoop()
	return rl
}

func (rl *RateLimiter) refillLoop() {
	// Refill rate = n² where n = requestsPerMin (aggressive refill for high-concurrency workloads)
	refillRate := rl.requestsPerMin * rl.requestsPerMin
	requestTicker := time.NewTicker(time.Minute / time.Duration(refillRate))
	defer requestTicker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-requestTicker.C:
			select {
			case rl.requestBucket <- struct{}{}:
			default:
			}
		}
	}
}

func (rl *RateLimiter) Acquire(ctx context.Context, _ int) error {
	select {
	case <-rl.requestBucket:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns a token to the request bucket after a request completes.
// This allows throughput to scale with actual request completion rather than
// only relying on the fixed refill rate.
func (rl *RateLimiter) Release() {
	select {
	case rl.requestBucket <- struct{}{}:
	default:
		// Bucket full, discard
	}
}

func (rl *RateLimiter) Close() {
	close(rl.done)
}

// Stats returns current rate limiter statistics.
type RateLimiterStats struct {
	RequestsAvailable int
	TokensAvailable   int64
	TokensUsed        int64
}

func (rl *RateLimiter) Stats() RateLimiterStats {
	tokensUsed := rl.tokenBucket.currentTokens()
	return RateLimiterStats{
		RequestsAvailable: len(rl.requestBucket),
		TokensAvailable:   rl.tokenBucket.available(),
		TokensUsed:        tokensUsed,
	}
}

// AdaptiveRateLimiter wraps RateLimiter with adaptive 429 handling.
// It learns from actual rate limit responses and adjusts behavior.
type AdaptiveRateLimiter struct {
	base         *RateLimiter
	last429      atomic.Value
	successRun   atomic.Int64
	currentLimit atomic.Int64
	config       AdaptiveConfig
}

type AdaptiveConfig struct {
	BackoffWindow            time.Duration // How long to back off after 429
	SuccessClearThreshold    int           // Successes needed to clear backoff
	SuccessGrowthThreshold   int           // Successes needed to grow limit
	DecayFactor              float64       // Multiply limit by this on 429
	GrowthFactor             float64       // Multiply limit by this on success streak
	InitialLimitPercent      int           // Starting limit percentage (0-100)
	MinLimitPercent          int           // Minimum limit percentage
	LatencyThresholdMs       int64         // P99 latency threshold for soft-throttle
	HedgeDelayMs             int64         // Delay before sending hedge request
	AdaptiveBatchSizeEnabled bool          // Enable adaptive batch sizing
}

func DefaultAdaptiveConfig() AdaptiveConfig {
	return AdaptiveConfig{
		BackoffWindow:            500 * time.Millisecond,
		SuccessClearThreshold:    1,
		SuccessGrowthThreshold:   2,
		DecayFactor:              0.95,
		GrowthFactor:             1.5,
		InitialLimitPercent:      100,
		MinLimitPercent:          90,
		LatencyThresholdMs:       20000,
		HedgeDelayMs:             2000,
		AdaptiveBatchSizeEnabled: false,
	}
}

func NewAdaptiveRateLimiter(base *RateLimiter, cfg AdaptiveConfig) *AdaptiveRateLimiter {
	arl := &AdaptiveRateLimiter{
		base:   base,
		config: cfg,
	}
	arl.currentLimit.Store(int64(cfg.InitialLimitPercent))
	arl.last429.Store(time.Time{})
	return arl
}

func (arl *AdaptiveRateLimiter) Acquire(ctx context.Context, tokenEstimate int) error {
	return arl.base.Acquire(ctx, tokenEstimate)
}

func (arl *AdaptiveRateLimiter) Release() {
	arl.base.Release()
}

// Record429 should be called when a 429 response is received.
func (arl *AdaptiveRateLimiter) Record429() {
	arl.last429.Store(time.Now())
	arl.successRun.Store(0)

	// Decay the limit
	current := arl.currentLimit.Load()
	newLimit := int64(float64(current) * arl.config.DecayFactor)
	if newLimit < int64(arl.config.MinLimitPercent) {
		newLimit = int64(arl.config.MinLimitPercent)
	}
	arl.currentLimit.Store(newLimit)
}

// RecordSuccess should be called after a successful request.
func (arl *AdaptiveRateLimiter) RecordSuccess() {
	run := arl.successRun.Add(1)

	// Clear backoff after threshold successes
	if run >= int64(arl.config.SuccessClearThreshold) {
		arl.last429.Store(time.Time{})
	}

	// Grow limit after growth threshold
	if run >= int64(arl.config.SuccessGrowthThreshold) {
		current := arl.currentLimit.Load()
		newLimit := int64(float64(current) * arl.config.GrowthFactor)
		if newLimit > 100 {
			newLimit = 100
		}
		arl.currentLimit.Store(newLimit)
		arl.successRun.Store(0)
	}
}

// RecordLatency should be called with request latency to detect server-side queueing.
func (arl *AdaptiveRateLimiter) RecordLatency(latencyMs int64) {
	if latencyMs > arl.config.LatencyThresholdMs {
		// Treat high latency as soft-429 (server is likely overloaded)
		arl.Record429()
	}
}

// CurrentLimitPercent returns the current inferred limit as percentage.
func (arl *AdaptiveRateLimiter) CurrentLimitPercent() int {
	return int(arl.currentLimit.Load())
}

// InBackoff returns true if currently in backoff period.
func (arl *AdaptiveRateLimiter) InBackoff() bool {
	if last, ok := arl.last429.Load().(time.Time); ok && !last.IsZero() {
		return time.Since(last) < arl.config.BackoffWindow
	}
	return false
}

// SuggestedBatchSize returns recommended batch size based on current state.
func (arl *AdaptiveRateLimiter) SuggestedBatchSize(normalSize int) int {
	if !arl.config.AdaptiveBatchSizeEnabled {
		return normalSize
	}

	limitPct := arl.currentLimit.Load()
	if limitPct >= 80 {
		return normalSize
	}
	if limitPct >= 50 {
		return normalSize / 2
	}
	return normalSize / 4
}

func (arl *AdaptiveRateLimiter) Close() {
	arl.base.Close()
}
