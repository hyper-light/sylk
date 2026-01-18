package ratelimit

import (
	"testing"
	"time"
)

func TestNewMultiLayerRateLimiter(t *testing.T) {
	config := DefaultMultiLayerConfig()
	ml := NewMultiLayerRateLimiter("openai", config)

	if ml.provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", ml.provider)
	}

	if ml.tokenBucket == nil {
		t.Error("expected tokenBucket to be initialized")
	}

	if ml.slidingWindow == nil {
		t.Error("expected slidingWindow to be initialized")
	}

	if ml.adaptive429 == nil {
		t.Error("expected adaptive429 to be initialized")
	}
}

func TestMultiLayerRateLimiter_Check_AllAllowed(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   100,
			RefillRate: 10,
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Minute,
			MaxRequests: 60,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)
	decision := ml.Check()

	if !decision.Allowed {
		t.Error("expected decision to be allowed when all limiters allow")
	}
}

func TestMultiLayerRateLimiter_Check_TokenBucketDenies(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   1,
			RefillRate: 0.1, // Very slow refill
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Minute,
			MaxRequests: 1000, // Won't deny
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Exhaust token bucket
	ml.tokenBucket.Consume(2)

	decision := ml.Check()

	if decision.Allowed {
		t.Error("expected decision to be denied when token bucket is exhausted")
	}

	if decision.Limiter != "token_bucket" {
		t.Errorf("expected limiter 'token_bucket', got %q", decision.Limiter)
	}
}

func TestMultiLayerRateLimiter_Check_SlidingWindowDenies(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   1000,
			RefillRate: 100, // Won't deny
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Minute,
			MaxRequests: 2,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Fill sliding window
	ml.slidingWindow.Record()
	ml.slidingWindow.Record()

	decision := ml.Check()

	if decision.Allowed {
		t.Error("expected decision to be denied when sliding window is full")
	}

	if decision.Limiter != "sliding_window" {
		t.Errorf("expected limiter 'sliding_window', got %q", decision.Limiter)
	}
}

func TestMultiLayerRateLimiter_Check_Adaptive429Denies(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   1000,
			RefillRate: 100,
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Minute,
			MaxRequests: 1000,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Trigger 429 backoff
	ml.adaptive429.Record429(time.Second)

	decision := ml.Check()

	if decision.Allowed {
		t.Error("expected decision to be denied during 429 backoff")
	}

	if decision.Limiter != "adaptive_429" {
		t.Errorf("expected limiter 'adaptive_429', got %q", decision.Limiter)
	}
}

func TestMultiLayerRateLimiter_Check_MostRestrictiveWins(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   1,
			RefillRate: 0.1, // Will give short wait time
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Hour, // Will give long wait time
			MaxRequests: 2,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Exhaust token bucket (short wait ~10s)
	ml.tokenBucket.Consume(2)

	// Fill sliding window (long wait ~1h)
	ml.slidingWindow.Record()
	ml.slidingWindow.Record()

	decision := ml.Check()

	if decision.Allowed {
		t.Error("expected decision to be denied")
	}

	// Sliding window should win because it has longer wait time
	if decision.Limiter != "sliding_window" {
		t.Errorf("expected most restrictive limiter 'sliding_window', got %q", decision.Limiter)
	}

	if decision.WaitTime < time.Minute {
		t.Errorf("expected wait time > 1 minute, got %v", decision.WaitTime)
	}
}

func TestMultiLayerRateLimiter_Check_Adaptive429MostRestrictive(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   1,
			RefillRate: 10, // Fast refill, short wait
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  100 * time.Millisecond, // Short window
			MaxRequests: 2,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Exhaust token bucket (short wait ~0.1s)
	ml.tokenBucket.Consume(2)

	// Trigger 429 backoff (5s wait)
	ml.adaptive429.Record429(time.Second)

	decision := ml.Check()

	if decision.Allowed {
		t.Error("expected decision to be denied")
	}

	// Adaptive should win because 5s > token bucket wait
	if decision.Limiter != "adaptive_429" {
		t.Errorf("expected most restrictive limiter 'adaptive_429', got %q", decision.Limiter)
	}
}

func TestMultiLayerRateLimiter_RecordRequest(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   10,
			RefillRate: 1,
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Minute,
			MaxRequests: 10,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Record multiple requests
	for i := 0; i < 5; i++ {
		ml.RecordRequest()
	}

	// Token bucket should have fewer tokens
	tbDecision := ml.tokenBucket.Check()
	if !tbDecision.Allowed {
		t.Error("expected token bucket to still allow after 5 requests")
	}

	// Record more to exhaust
	for i := 0; i < 6; i++ {
		ml.RecordRequest()
	}

	// Now should be denied
	decision := ml.Check()
	if decision.Allowed {
		t.Error("expected decision to be denied after exhausting limits")
	}
}

func TestMultiLayerRateLimiter_Record429(t *testing.T) {
	config := DefaultMultiLayerConfig()
	ml := NewMultiLayerRateLimiter("openai", config)

	// Should be allowed initially
	decision := ml.Check()
	if !decision.Allowed {
		t.Error("expected initial decision to be allowed")
	}

	// Record a 429
	ml.Record429(time.Second)

	// Should now be denied due to backoff
	decision = ml.Check()
	if decision.Allowed {
		t.Error("expected decision to be denied after 429")
	}

	if decision.Limiter != "adaptive_429" {
		t.Errorf("expected limiter 'adaptive_429', got %q", decision.Limiter)
	}
}

func TestMultiLayerRateLimiter_RecordSuccess(t *testing.T) {
	config := DefaultMultiLayerConfig()
	ml := NewMultiLayerRateLimiter("openai", config)

	// Record a 429 first
	ml.Record429(time.Second)

	// Record enough successes to clear backoff (5 needed)
	for i := 0; i < 5; i++ {
		ml.RecordSuccess()
	}

	// Should be allowed again
	decision := ml.Check()
	if !decision.Allowed {
		t.Error("expected decision to be allowed after success run clears backoff")
	}
}

func TestMultiLayerRateLimiter_Provider(t *testing.T) {
	config := DefaultMultiLayerConfig()
	ml := NewMultiLayerRateLimiter("anthropic", config)

	if ml.Provider() != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", ml.Provider())
	}
}

func TestMultiLayerRateLimiter_CombinedLimiting(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   5,
			RefillRate: 1,
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Second,
			MaxRequests: 3,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Sliding window will fill first (max 3)
	for i := 0; i < 3; i++ {
		decision := ml.Check()
		if !decision.Allowed {
			t.Errorf("request %d should be allowed", i+1)
		}
		ml.RecordRequest()
	}

	// Fourth request should be denied by sliding window
	decision := ml.Check()
	if decision.Allowed {
		t.Error("expected fourth request to be denied")
	}

	if decision.Limiter != "sliding_window" {
		t.Errorf("expected sliding_window to deny, got %q", decision.Limiter)
	}
}

func TestMultiLayerRateLimiter_ConcurrentAccess(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   100,
			RefillRate: 10,
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  time.Minute,
			MaxRequests: 100,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	done := make(chan bool)

	// Concurrent Check calls
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				ml.Check()
			}
			done <- true
		}()
	}

	// Concurrent RecordRequest calls
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				ml.RecordRequest()
			}
			done <- true
		}()
	}

	// Concurrent Record429 calls
	for i := 0; i < 5; i++ {
		go func() {
			ml.Record429(time.Second)
			done <- true
		}()
	}

	// Concurrent RecordSuccess calls
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				ml.RecordSuccess()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 30; i++ {
		<-done
	}
}

func TestMultiLayerRateLimiter_AllDeniedLongestWins(t *testing.T) {
	config := MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   1,
			RefillRate: 1, // 1 token/sec, ~1s wait
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  10 * time.Second, // ~10s wait
			MaxRequests: 1,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}

	ml := NewMultiLayerRateLimiter("openai", config)

	// Exhaust token bucket
	ml.tokenBucket.Consume(2)

	// Fill sliding window
	ml.slidingWindow.Record()

	// Trigger 429 backoff (5s)
	ml.adaptive429.Record429(time.Second)

	decision := ml.Check()

	if decision.Allowed {
		t.Error("expected decision to be denied")
	}

	// Sliding window should win with ~10s wait
	if decision.Limiter != "sliding_window" {
		t.Errorf("expected sliding_window (longest wait), got %q", decision.Limiter)
	}

	if decision.WaitTime < 5*time.Second {
		t.Errorf("expected wait time > 5s, got %v", decision.WaitTime)
	}
}

func TestMultiLayerRateLimiter_CompareDecisions(t *testing.T) {
	ml := NewMultiLayerRateLimiter("test", DefaultMultiLayerConfig())

	// Test nil current
	candidate := &RateLimitDecision{Allowed: true}
	result := ml.compareDecisions(nil, candidate)
	if result != candidate {
		t.Error("expected candidate when current is nil")
	}

	// Test denied beats allowed
	allowed := &RateLimitDecision{Allowed: true}
	denied := &RateLimitDecision{Allowed: false, WaitTime: time.Second}
	result = ml.compareDecisions(allowed, denied)
	if result != denied {
		t.Error("expected denied to beat allowed")
	}

	// Test longer wait wins among denied
	shortWait := &RateLimitDecision{Allowed: false, WaitTime: time.Second}
	longWait := &RateLimitDecision{Allowed: false, WaitTime: time.Minute}
	result = ml.compareDecisions(shortWait, longWait)
	if result != longWait {
		t.Error("expected longer wait to win")
	}

	// Test shorter wait keeps current
	result = ml.compareDecisions(longWait, shortWait)
	if result != longWait {
		t.Error("expected current (longer wait) to be kept")
	}
}
