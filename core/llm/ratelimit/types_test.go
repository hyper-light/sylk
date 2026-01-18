package ratelimit

import (
	"testing"
	"time"
)

// TestRateLimitDecision_Fields tests RateLimitDecision struct fields.
func TestRateLimitDecision_Fields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		decision   RateLimitDecision
		wantAllow  bool
		wantWait   time.Duration
		wantReason string
		wantLimit  string
		wantConf   float64
	}{
		{
			name: "allowed decision",
			decision: RateLimitDecision{
				Allowed:    true,
				WaitTime:   0,
				Reason:     "",
				Limiter:    "token_bucket",
				Confidence: 1.0,
			},
			wantAllow:  true,
			wantWait:   0,
			wantReason: "",
			wantLimit:  "token_bucket",
			wantConf:   1.0,
		},
		{
			name: "denied decision with wait",
			decision: RateLimitDecision{
				Allowed:    false,
				WaitTime:   5 * time.Second,
				Reason:     "rate limit exceeded",
				Limiter:    "sliding_window",
				Confidence: 0.8,
			},
			wantAllow:  false,
			wantWait:   5 * time.Second,
			wantReason: "rate limit exceeded",
			wantLimit:  "sliding_window",
			wantConf:   0.8,
		},
		{
			name: "zero confidence",
			decision: RateLimitDecision{
				Allowed:    true,
				Confidence: 0.0,
			},
			wantAllow: true,
			wantConf:  0.0,
		},
		{
			name: "max wait time",
			decision: RateLimitDecision{
				Allowed:  false,
				WaitTime: time.Hour,
			},
			wantAllow: false,
			wantWait:  time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.decision.Allowed != tc.wantAllow {
				t.Errorf("Allowed = %v, want %v", tc.decision.Allowed, tc.wantAllow)
			}
			if tc.decision.WaitTime != tc.wantWait {
				t.Errorf("WaitTime = %v, want %v", tc.decision.WaitTime, tc.wantWait)
			}
			if tc.decision.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", tc.decision.Reason, tc.wantReason)
			}
			if tc.decision.Limiter != tc.wantLimit {
				t.Errorf("Limiter = %q, want %q", tc.decision.Limiter, tc.wantLimit)
			}
			if tc.decision.Confidence != tc.wantConf {
				t.Errorf("Confidence = %v, want %v", tc.decision.Confidence, tc.wantConf)
			}
		})
	}
}

// TestTokenBucketConfig_Fields tests TokenBucketConfig struct fields.
func TestTokenBucketConfig_Fields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     TokenBucketConfig
		wantCap    float64
		wantRefill float64
	}{
		{
			name: "standard config",
			config: TokenBucketConfig{
				Capacity:   100,
				RefillRate: 10,
			},
			wantCap:    100,
			wantRefill: 10,
		},
		{
			name: "high capacity",
			config: TokenBucketConfig{
				Capacity:   10000,
				RefillRate: 100,
			},
			wantCap:    10000,
			wantRefill: 100,
		},
		{
			name: "fractional rates",
			config: TokenBucketConfig{
				Capacity:   1.5,
				RefillRate: 0.1,
			},
			wantCap:    1.5,
			wantRefill: 0.1,
		},
		{
			name:       "zero values",
			config:     TokenBucketConfig{},
			wantCap:    0,
			wantRefill: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.config.Capacity != tc.wantCap {
				t.Errorf("Capacity = %v, want %v", tc.config.Capacity, tc.wantCap)
			}
			if tc.config.RefillRate != tc.wantRefill {
				t.Errorf("RefillRate = %v, want %v", tc.config.RefillRate, tc.wantRefill)
			}
		})
	}
}

// TestSlidingWindowConfig_Fields tests SlidingWindowConfig struct fields.
func TestSlidingWindowConfig_Fields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      SlidingWindowConfig
		wantWindow  time.Duration
		wantMaxReqs int
	}{
		{
			name: "one minute window",
			config: SlidingWindowConfig{
				WindowSize:  60 * time.Second,
				MaxRequests: 60,
			},
			wantWindow:  60 * time.Second,
			wantMaxReqs: 60,
		},
		{
			name: "one hour window",
			config: SlidingWindowConfig{
				WindowSize:  time.Hour,
				MaxRequests: 1000,
			},
			wantWindow:  time.Hour,
			wantMaxReqs: 1000,
		},
		{
			name: "subsecond window",
			config: SlidingWindowConfig{
				WindowSize:  100 * time.Millisecond,
				MaxRequests: 5,
			},
			wantWindow:  100 * time.Millisecond,
			wantMaxReqs: 5,
		},
		{
			name:        "zero values",
			config:      SlidingWindowConfig{},
			wantWindow:  0,
			wantMaxReqs: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.config.WindowSize != tc.wantWindow {
				t.Errorf("WindowSize = %v, want %v", tc.config.WindowSize, tc.wantWindow)
			}
			if tc.config.MaxRequests != tc.wantMaxReqs {
				t.Errorf("MaxRequests = %d, want %d", tc.config.MaxRequests, tc.wantMaxReqs)
			}
		})
	}
}

// TestAdaptive429Config_Fields tests Adaptive429Config struct fields.
func TestAdaptive429Config_Fields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      Adaptive429Config
		wantInitial float64
		wantDecay   float64
		wantGrowth  float64
		wantMin     float64
		wantMax     float64
	}{
		{
			name: "standard config",
			config: Adaptive429Config{
				InitialLimit: 50,
				DecayFactor:  0.95,
				GrowthFactor: 1.05,
				MinLimit:     5,
				MaxLimit:     200,
			},
			wantInitial: 50,
			wantDecay:   0.95,
			wantGrowth:  1.05,
			wantMin:     5,
			wantMax:     200,
		},
		{
			name: "aggressive decay",
			config: Adaptive429Config{
				InitialLimit: 100,
				DecayFactor:  0.5,
				GrowthFactor: 1.1,
				MinLimit:     1,
				MaxLimit:     500,
			},
			wantInitial: 100,
			wantDecay:   0.5,
			wantGrowth:  1.1,
			wantMin:     1,
			wantMax:     500,
		},
		{
			name: "conservative growth",
			config: Adaptive429Config{
				InitialLimit: 10,
				DecayFactor:  0.99,
				GrowthFactor: 1.01,
				MinLimit:     1,
				MaxLimit:     20,
			},
			wantInitial: 10,
			wantDecay:   0.99,
			wantGrowth:  1.01,
			wantMin:     1,
			wantMax:     20,
		},
		{
			name:        "zero values",
			config:      Adaptive429Config{},
			wantInitial: 0,
			wantDecay:   0,
			wantGrowth:  0,
			wantMin:     0,
			wantMax:     0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.config.InitialLimit != tc.wantInitial {
				t.Errorf("InitialLimit = %v, want %v", tc.config.InitialLimit, tc.wantInitial)
			}
			if tc.config.DecayFactor != tc.wantDecay {
				t.Errorf("DecayFactor = %v, want %v", tc.config.DecayFactor, tc.wantDecay)
			}
			if tc.config.GrowthFactor != tc.wantGrowth {
				t.Errorf("GrowthFactor = %v, want %v", tc.config.GrowthFactor, tc.wantGrowth)
			}
			if tc.config.MinLimit != tc.wantMin {
				t.Errorf("MinLimit = %v, want %v", tc.config.MinLimit, tc.wantMin)
			}
			if tc.config.MaxLimit != tc.wantMax {
				t.Errorf("MaxLimit = %v, want %v", tc.config.MaxLimit, tc.wantMax)
			}
		})
	}
}

// TestMultiLayerConfig_Fields tests MultiLayerConfig struct composition.
func TestMultiLayerConfig_Fields(t *testing.T) {
	t.Parallel()

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

	if config.TokenBucket.Capacity != 100 {
		t.Errorf("TokenBucket.Capacity = %v, want 100", config.TokenBucket.Capacity)
	}
	if config.SlidingWindow.MaxRequests != 60 {
		t.Errorf("SlidingWindow.MaxRequests = %d, want 60", config.SlidingWindow.MaxRequests)
	}
	if config.Adaptive.InitialLimit != 50 {
		t.Errorf("Adaptive.InitialLimit = %v, want 50", config.Adaptive.InitialLimit)
	}
}

// TestDefaultMultiLayerConfig tests the default configuration values.
func TestDefaultMultiLayerConfig(t *testing.T) {
	t.Parallel()

	config := DefaultMultiLayerConfig()

	// Test TokenBucket defaults
	if config.TokenBucket.Capacity != 100 {
		t.Errorf("TokenBucket.Capacity = %v, want 100", config.TokenBucket.Capacity)
	}
	if config.TokenBucket.RefillRate != 10 {
		t.Errorf("TokenBucket.RefillRate = %v, want 10", config.TokenBucket.RefillRate)
	}

	// Test SlidingWindow defaults
	if config.SlidingWindow.WindowSize != 60*time.Second {
		t.Errorf("SlidingWindow.WindowSize = %v, want 60s", config.SlidingWindow.WindowSize)
	}
	if config.SlidingWindow.MaxRequests != 60 {
		t.Errorf("SlidingWindow.MaxRequests = %d, want 60", config.SlidingWindow.MaxRequests)
	}

	// Test Adaptive defaults
	if config.Adaptive.InitialLimit != 50 {
		t.Errorf("Adaptive.InitialLimit = %v, want 50", config.Adaptive.InitialLimit)
	}
	if config.Adaptive.DecayFactor != 0.95 {
		t.Errorf("Adaptive.DecayFactor = %v, want 0.95", config.Adaptive.DecayFactor)
	}
	if config.Adaptive.GrowthFactor != 1.05 {
		t.Errorf("Adaptive.GrowthFactor = %v, want 1.05", config.Adaptive.GrowthFactor)
	}
	if config.Adaptive.MinLimit != 5 {
		t.Errorf("Adaptive.MinLimit = %v, want 5", config.Adaptive.MinLimit)
	}
	if config.Adaptive.MaxLimit != 200 {
		t.Errorf("Adaptive.MaxLimit = %v, want 200", config.Adaptive.MaxLimit)
	}
}

// TestDefaultMultiLayerConfig_Idempotent tests that defaults are consistent.
func TestDefaultMultiLayerConfig_Idempotent(t *testing.T) {
	t.Parallel()

	config1 := DefaultMultiLayerConfig()
	config2 := DefaultMultiLayerConfig()

	if config1 != config2 {
		t.Error("DefaultMultiLayerConfig should return identical values")
	}
}

// TestDefaultMultiLayerConfig_ValidValues tests that defaults are sensible.
func TestDefaultMultiLayerConfig_ValidValues(t *testing.T) {
	t.Parallel()

	config := DefaultMultiLayerConfig()

	// TokenBucket validations
	if config.TokenBucket.Capacity <= 0 {
		t.Error("TokenBucket.Capacity must be positive")
	}
	if config.TokenBucket.RefillRate <= 0 {
		t.Error("TokenBucket.RefillRate must be positive")
	}

	// SlidingWindow validations
	if config.SlidingWindow.WindowSize <= 0 {
		t.Error("SlidingWindow.WindowSize must be positive")
	}
	if config.SlidingWindow.MaxRequests <= 0 {
		t.Error("SlidingWindow.MaxRequests must be positive")
	}

	// Adaptive validations
	if config.Adaptive.InitialLimit <= 0 {
		t.Error("Adaptive.InitialLimit must be positive")
	}
	if config.Adaptive.DecayFactor <= 0 || config.Adaptive.DecayFactor >= 1 {
		t.Error("Adaptive.DecayFactor must be in (0, 1)")
	}
	if config.Adaptive.GrowthFactor <= 1 {
		t.Error("Adaptive.GrowthFactor must be > 1")
	}
	if config.Adaptive.MinLimit <= 0 {
		t.Error("Adaptive.MinLimit must be positive")
	}
	if config.Adaptive.MaxLimit <= config.Adaptive.MinLimit {
		t.Error("Adaptive.MaxLimit must be > MinLimit")
	}
	if config.Adaptive.InitialLimit < config.Adaptive.MinLimit {
		t.Error("Adaptive.InitialLimit must be >= MinLimit")
	}
	if config.Adaptive.InitialLimit > config.Adaptive.MaxLimit {
		t.Error("Adaptive.InitialLimit must be <= MaxLimit")
	}
}

// mockRateLimiter is a test implementation of RateLimiter interface.
type mockRateLimiter struct {
	decision RateLimitDecision
}

func (m *mockRateLimiter) Check() RateLimitDecision {
	return m.decision
}

// TestRateLimiter_Interface tests that the interface works correctly.
func TestRateLimiter_Interface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decision RateLimitDecision
	}{
		{
			name: "allowed",
			decision: RateLimitDecision{
				Allowed:    true,
				Limiter:    "mock",
				Confidence: 1.0,
			},
		},
		{
			name: "denied",
			decision: RateLimitDecision{
				Allowed:    false,
				WaitTime:   time.Second,
				Reason:     "test denial",
				Limiter:    "mock",
				Confidence: 0.5,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var limiter RateLimiter = &mockRateLimiter{decision: tc.decision}
			result := limiter.Check()

			if result.Allowed != tc.decision.Allowed {
				t.Errorf("Allowed = %v, want %v", result.Allowed, tc.decision.Allowed)
			}
			if result.WaitTime != tc.decision.WaitTime {
				t.Errorf("WaitTime = %v, want %v", result.WaitTime, tc.decision.WaitTime)
			}
		})
	}
}

// TestRateLimitDecision_ZeroValue tests the zero value behavior.
func TestRateLimitDecision_ZeroValue(t *testing.T) {
	t.Parallel()

	var decision RateLimitDecision

	if decision.Allowed != false {
		t.Error("zero value Allowed should be false")
	}
	if decision.WaitTime != 0 {
		t.Error("zero value WaitTime should be 0")
	}
	if decision.Reason != "" {
		t.Error("zero value Reason should be empty")
	}
	if decision.Limiter != "" {
		t.Error("zero value Limiter should be empty")
	}
	if decision.Confidence != 0 {
		t.Error("zero value Confidence should be 0")
	}
}

// TestConfidence_Bounds tests confidence value edge cases.
func TestConfidence_Bounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		confidence float64
	}{
		{"zero", 0.0},
		{"one", 1.0},
		{"half", 0.5},
		{"near_zero", 0.001},
		{"near_one", 0.999},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision := RateLimitDecision{Confidence: tc.confidence}
			if decision.Confidence != tc.confidence {
				t.Errorf("Confidence = %v, want %v", decision.Confidence, tc.confidence)
			}
		})
	}
}

// TestWaitTime_EdgeCases tests various wait time values.
func TestWaitTime_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		waitTime time.Duration
	}{
		{"zero", 0},
		{"nanosecond", time.Nanosecond},
		{"millisecond", time.Millisecond},
		{"second", time.Second},
		{"minute", time.Minute},
		{"hour", time.Hour},
		{"day", 24 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision := RateLimitDecision{WaitTime: tc.waitTime}
			if decision.WaitTime != tc.waitTime {
				t.Errorf("WaitTime = %v, want %v", decision.WaitTime, tc.waitTime)
			}
		})
	}
}
