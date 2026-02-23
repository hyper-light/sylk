package handoff

import (
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

// StopQuality maps a providers.StopReason to a [0,1] quality score.
type StopQuality float64

// StopQualityFromReason returns the quality score for the given stop reason.
// Exhaustive over all providers.StopReason variants.
func StopQualityFromReason(reason providers.StopReason) StopQuality {
	switch reason {
	case providers.StopReasonEndTurn:
		return 1.0 // Model completed naturally
	case providers.StopReasonToolUse:
		return 1.0 // Intentional delegation
	case providers.StopReasonStopSequence:
		return 0.9 // Stopped at known boundary
	case providers.StopReasonMaxTokens:
		return 0.3 // Truncated = incomplete
	case providers.StopReasonError:
		return 0.0 // Generation failed
	default:
		return 0.5 // Unknown — neutral prior
	}
}

// ProviderSignals are extracted from a providers.Response after each turn.
type ProviderSignals struct {
	StopReason       providers.StopReason
	StopQual         StopQuality
	OutputTokens     int
	InputTokens      int
	CacheReadTokens  int
	CacheWriteTokens int
}

// CacheEfficiency returns the fraction of input tokens served from cache.
// Returns 0 when InputTokens is zero.
func (p *ProviderSignals) CacheEfficiency() float64 {
	if p.InputTokens <= 0 {
		return 0
	}
	return float64(p.CacheReadTokens) / float64(p.InputTokens)
}

// OutputRatio returns the ratio of output to input tokens.
// Returns 0 when InputTokens is zero.
func (p *ProviderSignals) OutputRatio() float64 {
	if p.InputTokens <= 0 {
		return 0
	}
	return float64(p.OutputTokens) / float64(p.InputTokens)
}

// StreamSignals are extracted from providers.StreamMetrics.
type StreamSignals struct {
	TimeToFirstToken   time.Duration
	TotalDuration      time.Duration
	TokensPerSecond    float64
	BackpressureEvents int
	EarlyAbort         bool
	ChunksReceived     int
}

// BehaviorSignals are extracted from QualitySignal / FeedbackType.
type BehaviorSignals struct {
	Feedback     FeedbackType
	Rating       float64 // [0,1]
	IsRetry      bool
	IsCorrection bool
}

// FusedTurnQuality is the output of SignalFuser.Flush, consumed by
// the GP (quality target) and the quality hook.
type FusedTurnQuality struct {
	CompositeQuality float64              // GP target: min(stopQ, toolRatio, behavior)
	StopReason       providers.StopReason // Forwarded to quality hook
	CacheEfficiency  float64              // Forwarded to quality hook
	OutputRatio      float64              // Forwarded to quality hook
}

// StressSignal is a sideband input to the elastic sizer.
type StressSignal struct {
	Severity float64 // [0,1]
	Source   string  // "ttft", "backpressure", "early_abort", "throughput"
}

// BaselineKey partitions baselines per (agentType, modelID).
type BaselineKey struct {
	AgentType string
	ModelID   string
}
