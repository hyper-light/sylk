package handoff

import (
	"github.com/adalundhe/sylk/core/providers"
)

// ExtractProviderSignals extracts quality-relevant signals from a provider response.
func ExtractProviderSignals(resp *providers.Response) ProviderSignals {
	if resp == nil {
		return ProviderSignals{StopQual: StopQualityFromReason("")}
	}
	return ProviderSignals{
		StopReason:       resp.StopReason,
		StopQual:         StopQualityFromReason(resp.StopReason),
		OutputTokens:     resp.Usage.OutputTokens,
		InputTokens:      resp.Usage.InputTokens,
		CacheReadTokens:  resp.Usage.CacheReadTokens,
		CacheWriteTokens: resp.Usage.CacheWriteTokens,
	}
}

// ExtractStreamSignals extracts latency/throughput signals from stream metrics.
func ExtractStreamSignals(metrics *providers.StreamMetrics) StreamSignals {
	if metrics == nil {
		return StreamSignals{}
	}
	return StreamSignals{
		TimeToFirstToken:   metrics.TimeToFirstToken,
		TotalDuration:      metrics.TotalDuration,
		TokensPerSecond:    metrics.TokensPerSecond,
		BackpressureEvents: metrics.BackpressureEvents,
		EarlyAbort:         metrics.EarlyAborts > 0,
		ChunksReceived:     metrics.ChunksReceived,
	}
}

// ExtractBehaviorSignals converts a QualitySignal into typed behavior signals.
func ExtractBehaviorSignals(sig QualitySignal) BehaviorSignals {
	return BehaviorSignals{
		Feedback:     sig.FeedbackType,
		Rating:       clamp(sig.Score, 0.0, 1.0),
		IsRetry:      sig.FeedbackType == FeedbackRetry,
		IsCorrection: sig.FeedbackType == FeedbackCorrection,
	}
}

// ComputeStressSignal derives a stress signal from stream metrics relative
// to a learned baseline. Takes the MAX severity across sources.
func ComputeStressSignal(stream StreamSignals, baseline *SignalBaseline) StressSignal {
	var best StressSignal

	best = maxStress(best, ttftStress(stream, baseline))
	best = maxStress(best, backpressureStress(stream))
	best = maxStress(best, earlyAbortStress(stream))
	best = maxStress(best, throughputStress(stream, baseline))

	return best
}

// ttftStress computes TTFT deviation as a fraction of baseline.
func ttftStress(stream StreamSignals, baseline *SignalBaseline) StressSignal {
	if baseline == nil || baseline.TTFT() <= 0 || stream.TimeToFirstToken <= 0 {
		return StressSignal{}
	}
	base := float64(baseline.TTFT())
	actual := float64(stream.TimeToFirstToken)
	severity := clamp((actual-base)/base, 0.0, 1.0)
	return StressSignal{Severity: severity, Source: "ttft"}
}

// backpressureStress returns a categorical stress when backpressure occurs.
// Severity = 1 - StopQualityFromReason(stop_sequence): backpressure is the
// complement of reaching a clean boundary — a minor but definite issue.
func backpressureStress(stream StreamSignals) StressSignal {
	if stream.BackpressureEvents <= 0 {
		return StressSignal{}
	}
	severity := 1.0 - float64(StopQualityFromReason(providers.StopReasonStopSequence))
	return StressSignal{Severity: severity, Source: "backpressure"}
}

// earlyAbortStress returns high stress when the provider couldn't finish.
// Severity = 1 - StopQualityFromReason(max_tokens): early abort is as bad
// as truncation — the complement of the max_tokens quality score.
func earlyAbortStress(stream StreamSignals) StressSignal {
	if !stream.EarlyAbort {
		return StressSignal{}
	}
	severity := 1.0 - float64(StopQualityFromReason(providers.StopReasonMaxTokens))
	return StressSignal{Severity: severity, Source: "early_abort"}
}

// throughputStress computes throughput drop as a fraction of baseline.
func throughputStress(stream StreamSignals, baseline *SignalBaseline) StressSignal {
	if baseline == nil || baseline.TokensPerSecond() <= 0 || stream.TokensPerSecond <= 0 {
		return StressSignal{}
	}
	base := baseline.TokensPerSecond()
	severity := clamp((base-stream.TokensPerSecond)/base, 0.0, 1.0)
	return StressSignal{Severity: severity, Source: "throughput"}
}

// maxStress returns the StressSignal with higher severity.
func maxStress(a, b StressSignal) StressSignal {
	if b.Severity > a.Severity {
		return b
	}
	return a
}

// CompositeQuality computes the fused quality target using MIN aggregation.
// One bad signal tanks the score.
func CompositeQuality(stopQ StopQuality, toolCalls, toolSuccesses int, behaviorRating float64, hasBehavior bool) float64 {
	toolRatio := 1.0
	if toolCalls > 0 {
		toolRatio = float64(toolSuccesses) / float64(toolCalls)
	}

	result := min(float64(stopQ), toolRatio)
	if hasBehavior {
		result = min(result, behaviorRating)
	}
	return clamp(result, 0.0, 1.0)
}
