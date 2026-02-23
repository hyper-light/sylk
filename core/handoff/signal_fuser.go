package handoff

import (
	"sync"
)

// SignalFuser accumulates per-turn signals and flushes them into a
// FusedTurnQuality (for the GP target) and a StressSignal (for the
// elastic sizer sideband). One fuser per bridge.
//
// Usage: SetProviderSignals → SetStreamSignals → SetBehaviorSignals → Flush.
// Each Set is optional; Flush resets all to nil.
type SignalFuser struct {
	mu          sync.Mutex
	provider    *ProviderSignals
	stream      *StreamSignals
	behavior    *BehaviorSignals
	baselineKey BaselineKey
	registry    *BaselineRegistry
}

// NewSignalFuser creates a fuser wired to the given baseline registry.
func NewSignalFuser(key BaselineKey, registry *BaselineRegistry) *SignalFuser {
	return &SignalFuser{
		baselineKey: key,
		registry:    registry,
	}
}

// SetProviderSignals stores provider-level signals for this turn.
func (f *SignalFuser) SetProviderSignals(p ProviderSignals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.provider = &p
}

// SetStreamSignals stores stream-level signals for this turn.
func (f *SignalFuser) SetStreamSignals(s StreamSignals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stream = &s
}

// SetBehaviorSignals stores behavior-level signals for this turn.
func (f *SignalFuser) SetBehaviorSignals(b BehaviorSignals) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.behavior = &b
}

// FlushResult is the output of a Flush call.
type FlushResult struct {
	Quality FusedTurnQuality
	Stress  StressSignal
	HasData bool
}

// Flush computes the fused quality and stress signal from accumulated
// signals, then resets all internal pointers. If no signals were set,
// HasData is false and the caller should use legacy quality.
func (f *SignalFuser) Flush(toolCalls, toolSuccesses int) FlushResult {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.provider == nil && f.stream == nil && f.behavior == nil {
		return FlushResult{}
	}

	result := FlushResult{HasData: true}

	// Provider signals → StopQuality, cache efficiency, output ratio.
	stopQ := StopQualityFromReason("") // neutral prior — derived from unknown reason
	if f.provider != nil {
		stopQ = f.provider.StopQual
		result.Quality.StopReason = f.provider.StopReason
		result.Quality.CacheEfficiency = f.provider.CacheEfficiency()
		result.Quality.OutputRatio = f.provider.OutputRatio()
	}

	// Behavior signals → optional behavioral rating.
	hasBehavior := f.behavior != nil
	behaviorRating := float64(StopQualityFromReason("")) // neutral prior
	if hasBehavior {
		behaviorRating = f.behavior.Rating
	}

	result.Quality.CompositeQuality = CompositeQuality(
		stopQ, toolCalls, toolSuccesses, behaviorRating, hasBehavior,
	)

	// Stream signals → update baseline + compute stress.
	if f.stream != nil && f.registry != nil {
		f.registry.Update(f.baselineKey, *f.stream)
		baseline := f.registry.Get(f.baselineKey)
		result.Stress = ComputeStressSignal(*f.stream, baseline)
	}

	// Reset for next turn.
	f.provider = nil
	f.stream = nil
	f.behavior = nil

	return result
}
