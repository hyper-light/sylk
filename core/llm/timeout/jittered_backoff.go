package timeout

import (
	"crypto/rand"
	"encoding/binary"
	"math"
	mathrand "math/rand"
	"sync"
	"time"
)

// JitteredBackoff implements exponential backoff with jitter.
// It uses decorrelated jitter to prevent thundering herd problems.
type JitteredBackoff struct {
	config  BackoffConfig
	attempt int

	rng *mathrand.Rand
	mu  sync.Mutex
}

// NewJitteredBackoff creates a new JitteredBackoff with base and max duration.
// Uses default jitter range [0.5, 1.5].
func NewJitteredBackoff(base, max time.Duration) *JitteredBackoff {
	config := DefaultBackoffConfig()
	config.Base = base
	config.Max = max
	return NewJitteredBackoffWithConfig(config)
}

// NewJitteredBackoffWithConfig creates a new JitteredBackoff from BackoffConfig.
func NewJitteredBackoffWithConfig(config BackoffConfig) *JitteredBackoff {
	return &JitteredBackoff{
		config:  config,
		attempt: 0,
		rng:     mathrand.New(mathrand.NewSource(cryptoSeed())),
	}
}

// cryptoSeed generates a cryptographically secure seed for math/rand.
func cryptoSeed() int64 {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return int64(binary.BigEndian.Uint64(buf[:]))
}

// Next returns the next backoff duration with jitter.
// Formula: jittered = min(base * 2^attempt, max) * uniform(minJitter, maxJitter)
func (jb *JitteredBackoff) Next() time.Duration {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	backoff := jb.calculateBackoff()
	jittered := jb.applyJitter(backoff)

	jb.attempt++
	return jittered
}

// calculateBackoff computes base * 2^attempt, capped at max.
func (jb *JitteredBackoff) calculateBackoff() time.Duration {
	multiplier := math.Pow(2, float64(jb.attempt))
	backoff := time.Duration(float64(jb.config.Base) * multiplier)

	if backoff > jb.config.Max {
		return jb.config.Max
	}
	return backoff
}

// applyJitter applies uniform jitter to the backoff duration.
func (jb *JitteredBackoff) applyJitter(backoff time.Duration) time.Duration {
	jitterRange := jb.config.MaxJitter - jb.config.MinJitter
	jitter := jb.config.MinJitter + jb.rng.Float64()*jitterRange
	return time.Duration(float64(backoff) * jitter)
}

// Reset resets the backoff counter to zero.
func (jb *JitteredBackoff) Reset() {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.attempt = 0
}

// Attempt returns the current attempt number.
func (jb *JitteredBackoff) Attempt() int {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.attempt
}
