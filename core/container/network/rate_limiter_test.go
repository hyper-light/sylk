package network

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowBurst(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Rate:  100,
		Burst: 10,
	})

	// Should allow burst capacity
	for range 10 {
		if !rl.Allow() {
			t.Fatal("should allow within burst capacity")
		}
	}

	// 11th should fail
	if rl.Allow() {
		t.Fatal("should deny when burst exhausted")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Rate:  1000, // 1000/sec = 1/ms
		Burst: 5,
	})

	// Drain bucket
	for range 5 {
		rl.Allow()
	}

	// Wait for refill (should add ~50 tokens at 1000/sec over 50ms)
	time.Sleep(50 * time.Millisecond)

	if !rl.Allow() {
		t.Fatal("should allow after refill")
	}
}

func TestRateLimiter_AllowN(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Rate:  100,
		Burst: 10,
	})

	if !rl.AllowN(5) {
		t.Fatal("should allow 5 from 10")
	}
	if !rl.AllowN(5) {
		t.Fatal("should allow 5 more from 10")
	}
	if rl.AllowN(1) {
		t.Fatal("should deny when empty")
	}
}

func TestRateLimiter_Available(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Rate:  100,
		Burst: 50,
	})

	before := rl.Available()
	if before < 49 {
		t.Fatalf("expected ~50, got %f", before)
	}

	rl.AllowN(10)
	after := rl.Available()
	if after > before-9 {
		t.Fatalf("expected ~40, got %f", after)
	}
}

func TestRateLimiter_DefaultConfig(t *testing.T) {
	cfg := DefaultRateLimiterConfig()
	if cfg.Rate != defaultRateLimitRate {
		t.Fatalf("expected %f, got %f", defaultRateLimitRate, cfg.Rate)
	}
	if cfg.Burst != defaultRateLimitBurst {
		t.Fatalf("expected %d, got %d", defaultRateLimitBurst, cfg.Burst)
	}
}
