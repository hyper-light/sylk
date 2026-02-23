package handoff

import (
	"math"
	"sync"
	"time"
)

// ewmaStat tracks a single EWMA-smoothed statistic.
type ewmaStat struct {
	mean        float64
	initialized bool
	alpha       float64
}

// update incorporates a new sample into the EWMA.
func (e *ewmaStat) update(value float64) {
	if !e.initialized {
		e.mean = value
		e.initialized = true
		return
	}
	e.mean = e.alpha*value + (1-e.alpha)*e.mean
}

// SignalBaseline holds per-(agentType, modelID) EWMA baselines
// for TTFT and tokens-per-second.
type SignalBaseline struct {
	ttft            ewmaStat
	tokensPerSecond ewmaStat
	lastUpdated     time.Time
	sampleCount     int
}

// Update incorporates stream signals into the baseline.
func (b *SignalBaseline) Update(s StreamSignals) {
	if s.TimeToFirstToken > 0 {
		b.ttft.update(float64(s.TimeToFirstToken))
	}
	if s.TokensPerSecond > 0 {
		b.tokensPerSecond.update(s.TokensPerSecond)
	}
	b.lastUpdated = time.Now()
	b.sampleCount++
}

// TTFT returns the baseline time-to-first-token, or 0 if uninitialized.
func (b *SignalBaseline) TTFT() time.Duration {
	if !b.ttft.initialized {
		return 0
	}
	return time.Duration(b.ttft.mean)
}

// TokensPerSecond returns the baseline throughput, or 0 if uninitialized.
func (b *SignalBaseline) TokensPerSecond() float64 {
	if !b.tokensPerSecond.initialized {
		return 0
	}
	return b.tokensPerSecond.mean
}

// IsWarmedUp returns true once at least minSamples have been recorded.
func (b *SignalBaseline) IsWarmedUp(minSamples int) bool {
	return b.sampleCount >= minSamples
}

// BaselineRegistry is a thread-safe map of BaselineKey to *SignalBaseline.
type BaselineRegistry struct {
	mu        sync.RWMutex
	baselines map[BaselineKey]*SignalBaseline
	alpha     float64
}

// NewBaselineRegistry creates a registry with EWMA alpha derived from halfLife.
// alpha = 1 - exp(-ln2 / halfLife)
func NewBaselineRegistry(halfLife float64) *BaselineRegistry {
	alpha := 1 - math.Exp(-math.Ln2/halfLife)
	return &BaselineRegistry{
		baselines: make(map[BaselineKey]*SignalBaseline),
		alpha:     alpha,
	}
}

// Get returns the baseline for key, or nil if absent.
func (r *BaselineRegistry) Get(key BaselineKey) *SignalBaseline {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.baselines[key]
}

// Update records stream signals into the baseline for key,
// creating a new entry if needed.
func (r *BaselineRegistry) Update(key BaselineKey, s StreamSignals) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.baselines[key]
	if !ok {
		b = &SignalBaseline{
			ttft:            ewmaStat{alpha: r.alpha},
			tokensPerSecond: ewmaStat{alpha: r.alpha},
		}
		r.baselines[key] = b
	}
	b.Update(s)
}
